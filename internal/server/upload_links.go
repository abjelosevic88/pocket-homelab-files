package server

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"path"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-files/internal/vfs"
)

// One-time upload links. A signed-in caller (an AI agent through Pocket Core's tools, a script) asks for a link
// that writes exactly one file; the file's bytes then go straight to Files with a plain PUT instead of through the
// caller's JSON API, so a build of any size can be uploaded. A link works once, for one hour, for the one path it
// was made for, and writes as the person who asked for it. Links live in memory: a restart forgets them.

const uploadLinkLife = time.Hour

type uploadLink struct {
	user     int64
	space    string
	dir      string
	name     string
	conflict string
	expires  time.Time
}

type uploadLinks struct {
	mu    sync.Mutex
	links map[string]*uploadLink
}

func (l *uploadLinks) add(link *uploadLink) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.links == nil {
		l.links = map[string]*uploadLink{}
	}
	now := time.Now()
	for t, old := range l.links {
		if now.After(old.expires) {
			delete(l.links, t)
		}
	}
	l.links[token] = link
	return token, nil
}

// take hands out a link once: whatever happens to the upload, the token is spent.
func (l *uploadLinks) take(token string) *uploadLink {
	l.mu.Lock()
	defer l.mu.Unlock()
	link := l.links[token]
	delete(l.links, token)
	if link == nil || time.Now().After(link.expires) {
		return nil
	}
	return link
}

// POST /api/v1/fs/upload-link {space, path, overwrite}: a link to PUT one file's bytes to.
func (s *Server) handleCreateUploadLink(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Space     string `json:"space"`
		Path      string `json:"path"`
		Overwrite bool   `json:"overwrite"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	sp, err := s.space(r.Context(), me(r), in.Space)
	if err != nil {
		s.fail(w, err, "open")
		return
	}
	if sp.File != "" || !sp.Writable() {
		writeErr(w, http.StatusForbidden, "you can't add files here")
		return
	}
	rel := vfs.Clean(in.Path)
	name := path.Base(rel)
	if err := vfs.ValidName(name); err != nil {
		s.fail(w, err, "upload")
		return
	}
	if err := reserved(sp, rel); err != nil {
		s.fail(w, err, "upload")
		return
	}
	conflict := vfs.Rename
	if in.Overwrite {
		conflict = vfs.Overwrite
	}
	expires := time.Now().Add(uploadLinkLife)
	token, err := s.uploadLinks.add(&uploadLink{user: me(r), space: sp.ID, dir: path.Dir(rel), name: name, conflict: conflict, expires: expires})
	if err != nil {
		s.fail(w, err, "upload link")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"url":        s.cfg.BaseURL + "/api/v1/public/upload-links/" + token,
		"method":     "PUT",
		"space":      sp.ID,
		"path":       rel,
		"conflict":   conflict,
		"expires_at": expires.UTC().Format(time.RFC3339),
		"how":        "PUT the file's bytes to url (curl -T file url). The link works once and expires after an hour.",
	})
}

// PUT /api/v1/public/upload-links/{token}: the file's bytes.
func (s *Server) handleUploadLinkPut(w http.ResponseWriter, r *http.Request) {
	link := s.uploadLinks.take(chi.URLParam(r, "token"))
	if link == nil {
		writeErr(w, http.StatusNotFound, "this upload link was used already or has expired")
		return
	}
	ctx := r.Context()
	sp, err := s.space(ctx, link.user, link.space)
	if err != nil {
		s.fail(w, err, "open")
		return
	}
	e, err := sp.Write(link.dir, link.name, r.Body, link.conflict, time.Time{})
	if err != nil {
		s.fail(w, err, "upload")
		return
	}
	s.touch()
	s.indexTouch(sp, link.dir)
	if e == nil {
		writeJSON(w, http.StatusOK, map[string]any{"skipped": true})
		return
	}
	s.publish(ctx, "files.uploaded", "file:"+sp.ID+":"+e.Path, link.user, map[string]any{"space": sp.ID, "path": e.Path, "name": e.Name, "size": e.Size, "mime": e.Mime})
	writeJSON(w, http.StatusOK, e)
}
