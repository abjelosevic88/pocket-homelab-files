// Package server wires the HTTP API, the WebDAV server, share links (sends),
// upload links (requests), auth and the embedded SPA.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"golang.org/x/crypto/bcrypt"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-files/internal/config"
	"github.com/abjelosevic88/pocket-homelab-files/internal/store"
	"github.com/abjelosevic88/pocket-homelab-files/internal/vfs"
)

// Version is injected at build time.
var Version = "dev"

// Server holds dependencies.
type Server struct {
	cfg   *config.Config
	store *store.Store
	log   *slog.Logger
	webFS fs.FS

	started  time.Time
	requests atomic.Int64
	errors   atomic.Int64
	changed  atomic.Int64 // bumps on every write; GET /changes

	core    *core.Client
	coreMu  sync.Mutex
	coreOK  bool
	coreErr string

	oidc   *oidcProvider
	oidcMu sync.Mutex

	limiter *limiter
	davLock *davLocks
	pending *notifyQueue

	secretOnce sync.Once
	secretKey  []byte
}

// New creates a server.
func New(cfg *config.Config, st *store.Store, log *slog.Logger, webFS fs.FS) *Server {
	ua := "PocketFiles/" + Version + " (+https://github.com/abjelosevic88/pocket-homelab-files)"
	s := &Server{cfg: cfg, store: st, log: log, webFS: webFS, started: time.Now(), limiter: newLimiter(), davLock: newDavLocks()}
	s.core = core.New(cfg.CoreURL, core.Options{DB: st.DB, TokenFile: cfg.DataDir + "/core.token", UserAgent: ua, ServiceCache: time.Minute, ServiceStale: 15 * time.Minute})
	s.oidc = &oidcProvider{issuerURL: cfg.OIDCIssuer}
	s.pending = newNotifyQueue(s)
	return s
}

// Bootstrap seeds the first admin (if configured), demo data and starts the workers.
func (s *Server) Bootstrap(ctx context.Context) error {
	if err := s.core.Start(ctx); err != nil {
		return err
	}
	for _, d := range []string{s.cfg.FilesDir, s.cfg.SendsDir, s.cfg.TrashDir, s.cfg.ThumbsDir, filepath.Join(s.cfg.DataDir, "uploads")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", d, err)
		}
	}
	n, err := s.store.CountUsers(ctx)
	if err != nil {
		return err
	}
	if n == 0 && s.cfg.AuthMode == "local" && s.cfg.AdminEmail != "" && s.cfg.AdminPassword != "" {
		if len(s.cfg.AdminPassword) < 8 {
			return errors.New("ADMIN_PASSWORD must be at least 8 characters")
		}
		h, _ := bcrypt.GenerateFromPassword([]byte(s.cfg.AdminPassword), bcrypt.DefaultCost)
		u := &store.User{Email: s.cfg.AdminEmail, Name: s.cfg.AdminName, PasswordHash: string(h), Role: "admin"}
		if err := s.store.CreateUser(ctx, u); err != nil {
			return err
		}
		s.seedUser(ctx, u.ID)
		st, _ := s.store.GetSettings(ctx, 0)
		st.SetupComplete = true
		if err := s.store.SaveBoxSettings(ctx, st); err != nil {
			return err
		}
		s.log.Info("admin user created from environment", "email", u.Email)
	}
	s.seedOwnerSMTP(ctx)
	if s.cfg.DemoData {
		if err := s.seedDemo(ctx); err != nil {
			return err
		}
	}
	_ = s.store.PurgeSessions(ctx)
	s.touch()
	go s.housekeeping(ctx)
	go s.pending.run(ctx)
	if s.core.Configured() {
		go s.coreLoop(ctx)
	}
	return nil
}

// housekeeping empties old trash, drops dead sends and stale uploads.
func (s *Server) housekeeping(ctx context.Context) {
	t := time.NewTicker(15 * time.Minute)
	defer t.Stop()
	for {
		for _, uid := range s.store.UserIDs(ctx) {
			st, err := s.store.GetSettings(ctx, uid)
			if err != nil || st.TrashDays <= 0 {
				continue
			}
			for _, it := range s.store.OldTrash(ctx, uid, st.TrashDays) {
				s.purgeTrashItem(ctx, &it)
			}
		}
		s.store.PurgeTickets(ctx)
		for _, id := range s.store.DeadSends(ctx) {
			s.removeSend(ctx, id)
		}
		for _, u := range s.store.StaleUploads(ctx) {
			_ = os.Remove(u.Temp)
			_ = s.store.DeleteUpload(ctx, u.ID)
		}
		s.cleanThumbs()
		_ = s.store.PurgeSessions(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// touch records that something changed (the UI polls /changes).
func (s *Server) touch() { s.changed.Store(time.Now().UnixNano()) }

func init() {
	for _, m := range []string{"PROPFIND", "PROPPATCH", "MKCOL", "MOVE", "COPY", "LOCK", "UNLOCK", "REPORT"} {
		chi.RegisterMethod(m)
	}
}

func (s *Server) baseMiddleware(r chi.Router) {
	if s.cfg.TrustProxy {
		r.Use(middleware.RealIP)
	}
	r.Use(middleware.RequestID)
	r.Use(s.logging)
	r.Use(middleware.Recoverer)
	r.Use(securityHeaders)
}

// securityHeaders keeps the app out of other sites' frames (Pocket Hub's
// Caddy rule adds its own frame-ancestors) and stops type sniffing.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// Router builds the main router (everything).
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	s.baseMiddleware(r)

	r.Get("/healthz", s.handleHealth)
	r.Get("/readyz", s.handleReady)
	r.Get("/api/health", s.handleHealth)
	if s.cfg.MetricsEnabled {
		r.Get("/metrics", s.handleMetrics)
	}

	// WebDAV: /dav/… plus the Nextcloud-shaped paths many clients expect.
	r.Handle("/.well-known/webdav", http.RedirectHandler("/dav/", http.StatusMovedPermanently))
	r.Handle("/dav", http.HandlerFunc(s.handleDAV))
	r.Handle("/dav/*", http.HandlerFunc(s.handleDAV))
	r.Handle("/remote.php/webdav", http.HandlerFunc(s.handleDAV))
	r.Handle("/remote.php/webdav/*", http.HandlerFunc(s.handleDAV))
	r.Handle("/remote.php/dav/files/*", http.HandlerFunc(s.handleDAV))
	r.Get("/status.php", s.handleNCStatus)

	r.Route("/api/v1", func(r chi.Router) {
		r.Route("/public", s.publicRoutes)
		r.Group(func(r chi.Router) {
			r.Use(middleware.Timeout(180 * time.Second))
			r.Post("/auth/login", s.handleLogin)
			r.Post("/auth/setup", s.handleSetup)
			r.Get("/auth/status", s.handleAuthStatus)
			r.Get("/auth/oidc/start", s.handleOIDCStart)
			r.Get("/auth/oidc/callback", s.handleOIDCCallback)
			r.Post("/client-error", s.handleClientError)
		})

		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Group(func(r chi.Router) {
				r.Use(middleware.Timeout(180 * time.Second))
				r.Post("/auth/logout", s.handleLogout)
				r.Get("/auth/me", s.handleMe)
				r.Put("/auth/me", s.handleUpdateMe)
				r.Get("/changes", s.handleChanges)

				r.Get("/spaces", s.handleSpaces)
				r.Get("/fs/list", s.handleList)
				r.Get("/fs/stat", s.handleStat)
				r.Post("/fs/mkdir", s.handleMkdir)
				r.Post("/fs/move", s.handleMove)
				r.Post("/fs/copy", s.handleCopy)
				r.Post("/fs/rename", s.handleRename)
				r.Post("/fs/delete", s.handleDelete)
				r.Get("/fs/search", s.handleSearch)
				r.Get("/fs/usage", s.handleUsage)
				r.Get("/fs/thumb", s.handleThumb)
				r.Post("/fs/text", s.handleSaveText)
				r.Get("/stars", s.handleStars)
				r.Put("/stars", s.handleSetStar)

				r.Get("/trash", s.handleTrash)
				r.Post("/trash/{id}/restore", s.handleRestore)
				r.Delete("/trash/{id}", s.handlePurge)
				r.Delete("/trash", s.handleEmptyTrash)

				r.Get("/people", s.handlePeople)
				r.Get("/shares", s.handleShares)
				r.Get("/shares/path", s.handleShareOfPath)
				r.Put("/shares/path", s.handleSaveShare)
				r.Delete("/shares/{id}", s.handleDeleteShare)

				r.Get("/mounts", s.handleListMounts)
				r.Post("/mounts", s.handleSaveMount)
				r.Put("/mounts/{id}", s.handleSaveMount)
				r.Delete("/mounts/{id}", s.handleDeleteMount)
				r.Get("/mounts/browse", s.handleBrowseServer)

				r.Get("/sends", s.handleListSends)
				r.Post("/sends", s.handleCreateSend)
				r.Get("/sends/{id}", s.handleGetSend)
				r.Put("/sends/{id}", s.handleUpdateSend)
				r.Delete("/sends/{id}", s.handleDeleteSend)
				r.Post("/sends/{id}/finish", s.handleFinishSend)

				r.Get("/requests", s.handleListRequests)
				r.Post("/requests", s.handleSaveRequest)
				r.Put("/requests/{id}", s.handleSaveRequest)
				r.Delete("/requests/{id}", s.handleDeleteRequest)

				r.Get("/users", s.handleListUsers)
				r.Post("/users", s.handleCreateUser)
				r.Put("/users/{id}", s.handleUpdateUser)
				r.Delete("/users/{id}", s.handleDeleteUser)

				r.Get("/settings", s.handleGetSettings)
				r.Put("/settings", s.handlePutSettings)
				r.Get("/settings/system", s.handleSystemInfo)
				r.Post("/notify/test", s.handleTestNotify)
				r.Post("/email/test", s.handleTestEmail)
				r.Get("/email/templates", s.handleMailTemplates)
				r.Post("/email/preview", s.handleMailPreview)
				r.Post("/sends/{id}/email", s.handleEmailSend)
				r.Post("/requests/{id}/email", s.handleEmailRequest)

				r.Get("/tokens", s.handleListTokens)
				r.Post("/tokens", s.handleCreateToken)
				r.Delete("/tokens/{id}", s.handleDeleteToken)

				r.Get("/backup", s.handleBackup)
			})
			// Streams may run for hours: no timeout.
			r.Get("/fs/raw", s.handleRaw)
			r.Get("/fs/zip", s.handleZip)
			r.Put("/fs/upload", s.handleUpload)
			r.Put("/sends/{id}/files/{idx}", s.handleSendChunk)
		})
	})

	r.NotFound(s.handleSPA)
	return r
}

// PublicRouter serves only what a stranger with a link may reach: the share
// and request pages, their API and the static assets. It is meant for a
// tunnel (Cloudflare) while the full app stays private.
func (s *Server) PublicRouter() http.Handler {
	r := chi.NewRouter()
	s.baseMiddleware(r)
	r.Get("/healthz", s.handleHealth)
	r.Route("/api/v1/public", s.publicRoutes)
	r.Post("/api/v1/client-error", s.handleClientError)
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if strings.HasPrefix(p, "/s/") || strings.HasPrefix(p, "/r/") || strings.HasPrefix(p, "/assets/") ||
			p == "/favicon.svg" || p == "/manifest.webmanifest" || p == "/robots.txt" || strings.HasSuffix(p, ".png") {
			s.handleSPA(w, r)
			return
		}
		if p == "/" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("Pocket Files share links live here.\n"))
			return
		}
		http.NotFound(w, r)
	})
	return r
}

// ---------- helpers ----------

func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		s.requests.Add(1)
		if ww.Status() >= 500 {
			s.errors.Add(1)
		}
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			return
		}
		s.log.Debug("http", "method", r.Method, "path", r.URL.Path, "status", ww.Status(), "dur", time.Since(start).String(), "ip", r.RemoteAddr)
	})
}

type apiError struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, apiError{Error: msg})
}

type userError struct{ msg string }

func (e userError) Error() string { return e.msg }

func badRequest(msg string) error { return userError{msg} }

func (s *Server) fail(w http.ResponseWriter, err error, context string) {
	var ue userError
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, vfs.ErrNotFound):
		msg := "not found"
		if e := err.Error(); strings.Contains(e, "missing on the server") {
			msg = e
		}
		writeErr(w, http.StatusNotFound, msg)
		return
	case errors.Is(err, store.ErrConflict), errors.Is(err, vfs.ErrExists):
		writeErr(w, http.StatusConflict, "something with that name is already there")
		return
	case errors.Is(err, vfs.ErrForbidden):
		writeErr(w, http.StatusForbidden, err.Error())
		return
	case errors.Is(err, vfs.ErrBadName), errors.Is(err, vfs.ErrOutside), errors.Is(err, vfs.ErrNotDir), errors.Is(err, vfs.ErrIsDir):
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	case errors.Is(err, store.ErrGone):
		writeErr(w, http.StatusGone, "this link has expired or was used up")
		return
	case errors.As(err, &ue):
		writeErr(w, http.StatusBadRequest, ue.msg)
		return
	}
	s.log.Error(context, "err", err)
	writeErr(w, http.StatusInternalServerError, context+": "+err.Error())
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 16<<20))
	return dec.Decode(v)
}

func idParam(r *http.Request, name string) int64 {
	n, _ := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	return n
}

func queryBool(r *http.Request, name string) bool {
	v := strings.ToLower(r.URL.Query().Get(name))
	return v == "1" || v == "true" || v == "yes"
}

func me(r *http.Request) int64 { return actorID(r.Context()) }

// ---------- Pocket Core ----------

// coreLoop registers with Core on start (retrying until it answers) and
// re-registers every 30 minutes so version and last-seen stay fresh.
func (s *Server) coreLoop(ctx context.Context) {
	wait := 5 * time.Second
	for {
		err := s.registerWithCore(ctx)
		s.coreMu.Lock()
		s.coreOK, s.coreErr = err == nil, ""
		if err != nil {
			s.coreErr = err.Error()
		}
		s.coreMu.Unlock()
		if err != nil {
			s.log.Warn("pocket core: registration failed, retrying", "err", err, "in", wait.String())
		} else {
			wait = 30 * time.Minute
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if err != nil && wait < 5*time.Minute {
			wait *= 2
		}
	}
}

func (s *Server) registerWithCore(ctx context.Context) error {
	m := core.Manifest{
		Slug: "files", Name: "Pocket Files",
		Description: "Your files on the box: a home folder per person, server folders mounted for the household, folders shared between people, a WebDAV drive for other apps, and SkySend-style links — encrypted in the browser, with expiry, download limits and passwords — plus upload links for others.",
		Icon:        "/favicon.svg", Version: Version,
		URL: s.cfg.CoreAppURL, PublicURL: s.cfg.BaseURL, HealthPath: "/healthz", EnvPrefix: "FILES",
		Publishes: []string{"files.uploaded", "files.deleted", "files.shared", "files.send_created", "files.send_downloaded", "files.request_received"},
		Offers:    linkOffers,
	}
	if s.cfg.BaseURL != "" {
		m.OIDC = &core.OIDCRequest{RedirectURIs: []string{s.cfg.BaseURL + "/api/v1/auth/oidc/callback"}}
	}
	reg, err := s.core.Register(ctx, s.cfg.CoreRegistrationKey, m)
	if err != nil {
		return err
	}
	if reg.Token != "" {
		s.log.Info("pocket core: registered, app token stored", "core", s.cfg.CoreURL)
	} else {
		s.log.Debug("pocket core: re-registered", "core", s.cfg.CoreURL)
	}
	return nil
}

// publish queues a Core event; the SDK retries delivery.
func (s *Server) publish(ctx context.Context, topic, subject string, uid int64, payload map[string]any) {
	if !s.core.Configured() {
		return
	}
	if payload == nil {
		payload = map[string]any{}
	}
	if u, err := s.store.GetUser(ctx, uid); err == nil {
		payload["user_email"] = u.Email
	}
	payload["user_id"] = uid
	if err := core.QueueEvent(context.WithoutCancel(ctx), s.store.DB, topic, subject, payload); err != nil {
		s.log.Warn("pocket core: queue event", "topic", topic, "err", err)
	}
}

// coreStatus summarises the Pocket Core link for the About page.
func (s *Server) coreStatus() map[string]any {
	s.coreMu.Lock()
	defer s.coreMu.Unlock()
	return map[string]any{"configured": s.core.Configured(), "url": s.cfg.CoreURL, "registered": s.coreOK, "error": s.coreErr}
}

// handleClientError writes a crash of the web app into the server's log.
func (s *Server) handleClientError(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Message, Stack, Component, URL, Where, Agent string
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	who := ""
	if u, err := s.resolveUser(r); err == nil && u != nil {
		who = u.Email
	}
	s.log.Warn("web app crashed", "user", who, "where", in.Where, "url", in.URL, "message", in.Message, "stack", in.Stack, "component", in.Component, "agent", in.Agent)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleChanges(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"stamp": s.changed.Load()})
}

// ---------- health / metrics ----------

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"core_outbox": s.coreOutboxHealth(r.Context()), "status": "ok", "version": Version, "uptime": time.Since(s.started).Round(time.Second).String()})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DB.PingContext(r.Context()); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// handleNCStatus answers like Nextcloud so clients that probe status.php
// before using /remote.php/dav accept the server.
func (s *Server) handleNCStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"installed": true, "maintenance": false, "needsDbUpgrade": false, "version": "28.0.0.0", "versionstring": "28.0.0", "edition": "", "productname": "Pocket Files", "extendedSupport": false})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	counts := s.store.Counts(ctx)
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprintf(w, "# HELP pocket_files_info Build info.\n# TYPE pocket_files_info gauge\npocket_files_info{version=%q} 1\n", Version)
	fmt.Fprintf(w, "# HELP pocket_files_uptime_seconds Process uptime.\n# TYPE pocket_files_uptime_seconds gauge\npocket_files_uptime_seconds %d\n", int(time.Since(s.started).Seconds()))
	fmt.Fprintf(w, "# HELP pocket_files_http_requests_total HTTP requests served.\n# TYPE pocket_files_http_requests_total counter\npocket_files_http_requests_total %d\n", s.requests.Load())
	fmt.Fprintf(w, "# HELP pocket_files_http_errors_total HTTP 5xx responses.\n# TYPE pocket_files_http_errors_total counter\npocket_files_http_errors_total %d\n", s.errors.Load())
	fmt.Fprintf(w, "# HELP pocket_files_rows Rows per table.\n# TYPE pocket_files_rows gauge\n")
	for k, v := range counts {
		fmt.Fprintf(w, "pocket_files_rows{table=%q} %d\n", k, v)
	}
}

// ---------- static / SPA ----------

func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/dav") || strings.HasPrefix(r.URL.Path, "/remote.php/") {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if s.webFS == nil {
		http.Error(w, "web UI not built (run `make web`)", http.StatusNotFound)
		return
	}
	p := strings.TrimPrefix(r.URL.Path, "/")
	if p == "" {
		p = "index.html"
	}
	if f, err := s.webFS.Open(p); err == nil {
		f.Close()
		if strings.HasPrefix(p, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		http.FileServerFS(s.webFS).ServeHTTP(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	r.URL.Path = "/"
	http.FileServerFS(s.webFS).ServeHTTP(w, r)
}

// loc returns the person's time zone (their choice, else the box's).
func (s *Server) loc(ctx context.Context, uid int64) *time.Location {
	if tz := s.store.UserSetting(ctx, uid, "timezone"); tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			return l
		}
	}
	l, err := time.LoadLocation(s.cfg.Timezone)
	if err != nil {
		return time.UTC
	}
	return l
}

// publicLink is the address a stranger opens.
func (s *Server) publicLink(kind, id string) string {
	base := s.cfg.PublicURL
	if base == "" {
		base = s.cfg.BaseURL
	}
	return base + "/" + kind + "/" + id
}
