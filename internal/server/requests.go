package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/abjelosevic88/pocket-homelab-files/internal/store"
	"github.com/abjelosevic88/pocket-homelab-files/internal/vfs"
)

// A request is an upload link: whoever has it can drop files into one of
// the owner's folders, without seeing what is there. Files arrive in pieces
// (Cloudflare caps a request at 100 MB) and land with a free name.

func (s *Server) publicRoutes(r chi.Router) {
	r.Use(s.limiter.middleware)
	r.Get("/sends/{id}", s.handlePublicSend)
	r.Post("/sends/{id}/open", s.handleOpenSend)
	r.Get("/sends/{id}/files/{idx}", s.handleSendFile)
	r.Get("/sends/{id}/live/list", s.handleLiveList)
	r.Get("/sends/{id}/live/file", s.handleLiveFile)
	r.Get("/sends/{id}/live/zip", s.handleLiveZip)

	r.Get("/requests/{id}", s.handlePublicRequest)
	r.Post("/requests/{id}/unlock", s.handleUnlockRequest)
	r.Post("/requests/{id}/uploads", s.handleStartRequestUpload)
	r.Put("/requests/{id}/uploads/{uid}", s.handleRequestChunk)
	r.Post("/requests/{id}/uploads/{uid}/finish", s.handleFinishRequestUpload)
}

type requestView struct {
	store.Request
	URL       string `json:"url"`
	Open      bool   `json:"open"`
	Reason    string `json:"reason,omitempty"`
	SpaceName string `json:"space_name"`
	Missing   bool   `json:"missing,omitempty"`
}

func requestReason(rq *store.Request) string {
	switch {
	case rq.Closed:
		return "closed"
	case rq.ExpiresAt != "" && store.ParseTime(rq.ExpiresAt).Before(time.Now()):
		return "expired"
	case rq.MaxFiles > 0 && rq.FilesReceived >= rq.MaxFiles:
		return "full"
	}
	return ""
}

func (s *Server) requestView(ctx context.Context, rq *store.Request) requestView {
	v := requestView{Request: *rq, URL: s.publicLink("r", rq.ID), Open: store.RequestOpen(rq), Reason: requestReason(rq)}
	if sp, err := s.space(ctx, rq.OwnerID, rq.Space); err == nil {
		v.SpaceName = sp.Name
		if _, err := sp.Stat(rq.Path); err != nil {
			v.Missing = true
		}
	} else {
		v.Missing = true
	}
	return v
}

func (s *Server) handleListRequests(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListRequests(r.Context(), me(r))
	if err != nil {
		s.fail(w, err, "requests")
		return
	}
	out := make([]requestView, 0, len(list))
	for i := range list {
		out = append(out, s.requestView(r.Context(), &list[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSaveRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		Title     string `json:"title"`
		Message   string `json:"message"`
		Space     string `json:"space"`
		Path      string `json:"path"`
		Expires   string `json:"expires"`
		MaxFiles  int    `json:"max_files"`
		MaxFileMB int64  `json:"max_file_mb"`
		Password  string `json:"password"`
		NoPass    bool   `json:"remove_password"`
		Notify    bool   `json:"notify"`
		Closed    bool   `json:"closed"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	rq := &store.Request{OwnerID: me(r)}
	if id := chi.URLParam(r, "id"); id != "" {
		old, err := s.store.OwnRequest(ctx, me(r), id)
		if err != nil {
			s.fail(w, err, "request")
			return
		}
		rq = old
	}
	sp, err := s.space(ctx, me(r), firstNonEmpty(in.Space, "home"))
	if err != nil {
		s.fail(w, err, "open")
		return
	}
	if !sp.Writable() {
		writeErr(w, http.StatusForbidden, "you cannot add files to that folder")
		return
	}
	e, err := sp.Stat(in.Path)
	if err != nil || !e.IsDir {
		writeErr(w, http.StatusBadRequest, "pick a folder for the files")
		return
	}
	rq.Title, rq.Message, rq.Space, rq.Path = strings.TrimSpace(in.Title), strings.TrimSpace(in.Message), sp.ID, e.Path
	if rq.Title == "" {
		rq.Title = "Send me files"
	}
	if in.Expires != "" || rq.ID == "" {
		if rq.ExpiresAt, err = s.parseExpiry(firstNonEmpty(in.Expires, "7d")); err != nil {
			s.fail(w, err, "request")
			return
		}
	}
	rq.MaxFiles, rq.MaxFileMB, rq.Notify, rq.Closed = max(0, in.MaxFiles), max(0, in.MaxFileMB), in.Notify, in.Closed
	if in.Password != "" {
		h, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "password too long")
			return
		}
		rq.PasswordHash = string(h)
	} else if in.NoPass {
		rq.PasswordHash = ""
	}
	if err := s.store.SaveRequest(ctx, rq); err != nil {
		s.fail(w, err, "request")
		return
	}
	s.touch()
	got, _ := s.store.OwnRequest(ctx, me(r), rq.ID)
	writeJSON(w, http.StatusOK, s.requestView(ctx, got))
}

func (s *Server) handleDeleteRequest(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteRequest(r.Context(), me(r), chi.URLParam(r, "id")); err != nil {
		s.fail(w, err, "request")
		return
	}
	s.touch()
	w.WriteHeader(http.StatusNoContent)
}

// ---------- public side ----------

func (s *Server) requestToken(id string) string {
	exp := time.Now().Add(12 * time.Hour).Unix()
	msg := fmt.Sprintf("%s.%d", id, exp)
	mac := hmac.New(sha256.New, s.secret())
	mac.Write([]byte("request:" + msg))
	return fmt.Sprintf("%d.%s", exp, base64.RawURLEncoding.EncodeToString(mac.Sum(nil)))
}

func (s *Server) requestTokenOK(id, tok string) bool {
	exp, sig, ok := strings.Cut(tok, ".")
	if !ok {
		return false
	}
	n, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || time.Now().Unix() > n {
		return false
	}
	mac := hmac.New(sha256.New, s.secret())
	mac.Write([]byte("request:" + id + "." + exp))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(sig))
}

func (s *Server) handlePublicRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rq, err := s.store.GetRequest(ctx, chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "this upload link does not exist (any more)")
		return
	}
	out := map[string]any{"id": rq.ID, "title": rq.Title, "message": rq.Message, "has_password": rq.PasswordHash != "", "open": store.RequestOpen(rq), "reason": requestReason(rq),
		"max_files": rq.MaxFiles, "max_file_mb": s.requestFileLimitMB(rq), "files_received": rq.FilesReceived, "expires_at": rq.ExpiresAt, "chunk_mb": s.cfg.PublicUploadMB}
	if u, err := s.store.GetUser(ctx, rq.OwnerID); err == nil {
		out["owner"] = firstNonEmpty(u.Name, strings.Split(u.Email, "@")[0])
	}
	if rq.PasswordHash == "" {
		out["token"] = s.requestToken(rq.ID)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) requestFileLimitMB(rq *store.Request) int64 {
	lim := rq.MaxFileMB
	if s.cfg.MaxRequestMB > 0 && (lim == 0 || lim > s.cfg.MaxRequestMB) {
		lim = s.cfg.MaxRequestMB
	}
	return lim
}

func (s *Server) handleUnlockRequest(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !s.limiter.allowAttempt("request:"+id, clientIP(r)) {
		writeErr(w, http.StatusTooManyRequests, "too many wrong attempts — wait a few minutes")
		return
	}
	rq, err := s.store.GetRequest(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "this upload link does not exist (any more)")
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	_ = decode(r, &in)
	if rq.PasswordHash != "" && bcrypt.CompareHashAndPassword([]byte(rq.PasswordHash), []byte(in.Password)) != nil {
		s.limiter.fail("request:"+id, clientIP(r))
		writeErr(w, http.StatusUnauthorized, "wrong password")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": s.requestToken(id)})
}

// openRequest loads a request that takes files and checks the token.
func (s *Server) openRequest(w http.ResponseWriter, r *http.Request, tok string) *store.Request {
	id := chi.URLParam(r, "id")
	rq, err := s.store.GetRequest(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "this upload link does not exist (any more)")
		return nil
	}
	if !s.requestTokenOK(id, tok) {
		writeErr(w, http.StatusUnauthorized, "open the link again")
		return nil
	}
	if !store.RequestOpen(rq) {
		writeErr(w, http.StatusGone, "this upload link no longer takes files")
		return nil
	}
	return rq
}

type reqUploadExtra struct {
	From string `json:"from"`
	Dir  string `json:"dir"`
}

func (s *Server) handleStartRequestUpload(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
		Name  string `json:"name"`
		Size  int64  `json:"size"`
		From  string `json:"from"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	rq := s.openRequest(w, r, in.Token)
	if rq == nil {
		return
	}
	name := path.Base(vfs.Clean(in.Name))
	if vfs.ValidName(name) != nil || strings.HasPrefix(name, ".") {
		writeErr(w, http.StatusBadRequest, "that file name cannot be used")
		return
	}
	if lim := s.requestFileLimitMB(rq); lim > 0 && in.Size > lim<<20 {
		writeErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("files may be up to %d MB here", lim))
		return
	}
	if in.Size < 0 {
		writeErr(w, http.StatusBadRequest, "bad size")
		return
	}
	extra, _ := json.Marshal(reqUploadExtra{From: strings.TrimSpace(in.From)})
	u := &store.Upload{Owner: "request:" + rq.ID, Name: name, Size: in.Size, Extra: string(extra)}
	u.Temp = filepath.Join(s.cfg.DataDir, "uploads", store.NewID(20)+".part")
	if err := s.store.CreateUpload(r.Context(), u); err != nil {
		s.fail(w, err, "upload")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": u.ID, "chunk_mb": s.cfg.PublicUploadMB})
}

func (s *Server) handleRequestChunk(w http.ResponseWriter, r *http.Request) {
	rq := s.openRequest(w, r, r.URL.Query().Get("token"))
	if rq == nil {
		return
	}
	u, err := s.store.GetUpload(r.Context(), "request:"+rq.ID, chi.URLParam(r, "uid"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "no such upload")
		return
	}
	offset, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	body := http.MaxBytesReader(w, r.Body, (s.cfg.PublicUploadMB+1)<<20)
	size, err := appendChunk(u.Temp, offset, u.Size, body)
	if err != nil {
		if ce, ok := err.(chunkErr); ok {
			writeJSON(w, ce.status, map[string]any{"error": ce.msg, "size": size})
			return
		}
		s.fail(w, err, "upload")
		return
	}
	_ = s.store.SetUploadReceived(r.Context(), u.ID, size)
	writeJSON(w, http.StatusOK, map[string]any{"size": size})
}

func (s *Server) handleFinishRequestUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		Token string `json:"token"`
	}
	_ = decode(r, &in)
	rq := s.openRequest(w, r, in.Token)
	if rq == nil {
		return
	}
	u, err := s.store.GetUpload(ctx, "request:"+rq.ID, chi.URLParam(r, "uid"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "no such upload")
		return
	}
	fi, err := os.Stat(u.Temp)
	if (err != nil && u.Size > 0) || (err == nil && fi.Size() != u.Size) {
		writeErr(w, http.StatusConflict, "the file is not complete yet")
		return
	}
	// the owner's access decides, now, whether the folder still takes files
	sp, err := s.space(ctx, rq.OwnerID, rq.Space)
	if err != nil || !sp.Writable() {
		writeErr(w, http.StatusGone, "the folder for these files is gone")
		return
	}
	var f *os.File
	if u.Size == 0 {
		f, err = os.CreateTemp(filepath.Join(s.cfg.DataDir, "uploads"), "empty-*")
		if err == nil {
			u.Temp = f.Name()
		}
	} else {
		f, err = os.Open(u.Temp)
	}
	if err != nil {
		s.fail(w, err, "upload")
		return
	}
	e, err := sp.Write(rq.Path, u.Name, f, vfs.Rename, time.Time{})
	f.Close()
	_ = os.Remove(u.Temp)
	_ = s.store.DeleteUpload(ctx, u.ID)
	if err != nil {
		s.fail(w, err, "upload")
		return
	}
	_ = s.store.CountRequestUpload(ctx, rq.ID, e.Size)
	s.touch()
	s.indexTouch(sp, rq.Path)
	var extra reqUploadExtra
	_ = json.Unmarshal([]byte(u.Extra), &extra)
	s.publish(ctx, "files.request_received", "request:"+rq.ID, rq.OwnerID, map[string]any{"request": rq.ID, "title": rq.Title, "space": sp.ID, "path": e.Path, "name": e.Name, "size": e.Size, "from": extra.From})
	if rq.Notify {
		who := "Someone"
		if extra.From != "" {
			who = extra.From
		}
		s.pending.add(rq.OwnerID, "uploads", "request:"+rq.ID, "Files received", fmt.Sprintf("%s uploaded %%d through “%s”.", who, rq.Title), s.cfg.BaseURL+"/requests")
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": e.Name, "size": e.Size})
}

// secret signs request tokens; made once per box.
func (s *Server) secret() []byte {
	s.secretOnce.Do(func() {
		var v string
		_ = s.store.DB.QueryRow(`SELECT value FROM settings WHERE key = 'secret'`).Scan(&v)
		if v == "" {
			v = store.RandomToken(32)
			_, _ = s.store.DB.Exec(`INSERT INTO settings (key, value) VALUES ('secret', ?) ON CONFLICT(key) DO NOTHING`, v)
			_ = s.store.DB.QueryRow(`SELECT value FROM settings WHERE key = 'secret'`).Scan(&v)
		}
		s.secretKey = []byte(v)
	})
	return s.secretKey
}
