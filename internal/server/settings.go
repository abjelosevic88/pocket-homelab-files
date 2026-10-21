package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-files/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-files/internal/store"
)

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.GetSettings(r.Context(), me(r))
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	if st.NotifyToken != "" {
		st.NotifyToken = "••••••"
	}
	st.SMTPPassword = "" // write-only
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	cur, err := s.store.GetSettings(ctx, uid)
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	in := cur
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.NotifyToken == "••••••" {
		in.NotifyToken = cur.NotifyToken
	}
	if in.SMTPPassword == "" {
		in.SMTPPassword = cur.SMTPPassword // never echoed back, so empty means keep
	}
	in.SMTPHost, in.SMTPFrom, in.SMTPUser = strings.TrimSpace(in.SMTPHost), strings.TrimSpace(in.SMTPFrom), strings.TrimSpace(in.SMTPUser)
	switch in.NotifyKind {
	case "apprise", "ntfy", "webhook":
	default:
		in.NotifyKind = "apprise"
	}
	if in.Timezone != "" {
		if _, err := time.LoadLocation(in.Timezone); err != nil {
			writeErr(w, http.StatusBadRequest, "unknown time zone")
			return
		}
	}
	if in.SendExpiry != "" && in.SendExpiry != "never" {
		if _, err := s.parseExpiry(in.SendExpiry); err != nil {
			s.fail(w, err, "settings")
			return
		}
	}
	if in.TrashDays < 0 {
		in.TrashDays = 0
	}
	if err := s.store.SaveUserSettings(ctx, uid, in); err != nil {
		s.fail(w, err, "settings")
		return
	}
	if u := userFrom(ctx); u.Role == "admin" && in.Title != cur.Title {
		box, _ := s.store.GetSettings(ctx, 0)
		box.Title = strings.TrimSpace(in.Title)
		if box.Title == "" {
			box.Title = store.DefaultTitle
		}
		_ = s.store.SaveBoxSettings(ctx, box)
	}
	s.handleGetSettings(w, r)
}

func (s *Server) handleTestNotify(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	st, _ := s.store.GetSettings(ctx, uid)
	url, kind := st.NotifyURL, st.NotifyKind
	if url == "" && s.isOwner(ctx, uid) {
		url, kind = s.cfg.NotifyURL, s.cfg.NotifyKind
	}
	if url == "" {
		writeErr(w, http.StatusBadRequest, "no notification address is set")
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := notify.Send(cctx, kind, url, st.NotifyToken, "Pocket Files", "Downloads of your links and files dropped through upload links will be announced here.", s.cfg.BaseURL); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	var size int64
	if fi, err := os.Stat(s.cfg.DBPath); err == nil {
		size = fi.Size()
	}
	free, total := diskFree(s.cfg.FilesDir)
	base := s.cfg.BaseURL
	if base == "" {
		base = "http://" + r.Host
	}
	home := s.homeSpace(u.ID)
	files, used := home.Usage("/")
	out := map[string]any{
		"version": Version, "go": runtime.Version(), "db_size": size, "base_url": s.cfg.BaseURL, "public_url": s.cfg.PublicURL,
		"public_listener": s.cfg.PublicPort != 0, "core": s.coreStatus(), "timezone": s.cfg.Timezone,
		"webdav":      map[string]string{"url": base + "/dav/", "nextcloud_url": base + "/remote.php/dav/files/" + u.Username + "/", "username": u.Username},
		"home":        map[string]any{"files": files, "size": used},
		"disk":        map[string]any{"free": free, "total": total},
		"max_send_mb": s.cfg.MaxSendMB, "max_send_days": s.cfg.MaxSendDays, "mounting": len(s.cfg.MountRoots) > 0,
	}
	if u.Role == "admin" {
		out["box_counts"] = s.store.Counts(ctx)
		out["data_dir"] = s.cfg.DataDir
		out["mount_roots"] = s.cfg.MountRoots
	}
	writeJSON(w, http.StatusOK, out)
}

// handleBackup hands an admin the database while they are alone on the box;
// with more people it holds their records too, so it stays on the server.
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if n, _ := s.store.CountUsers(r.Context()); n > 1 {
		writeErr(w, http.StatusForbidden, "the database holds other people's records; back up the data folder on the server instead (pocket-files backup)")
		return
	}
	dst := fmt.Sprintf("%s/backups/pocket-files-%s.db", s.cfg.DataDir, time.Now().Format("20060102-150405"))
	if err := s.store.Backup(r.Context(), dst); err != nil {
		s.fail(w, err, "backup")
		return
	}
	defer os.Remove(dst)
	w.Header().Set("Content-Type", "application/x-sqlite3")
	w.Header().Set("Content-Disposition", "attachment; filename=\"pocket-files.db\"")
	http.ServeFile(w, r, dst)
}
