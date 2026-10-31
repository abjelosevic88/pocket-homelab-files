package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-files/internal/search"
	"github.com/abjelosevic88/pocket-homelab-files/internal/vfs"
)

// Search (docs/search.md): one index of every place the app serves, kept in DATA_DIR/search.db.
// People only ever get results from places they can open.

func (s *Server) searchDB() string { return filepath.Join(s.cfg.DataDir, "search.db") }

func (s *Server) openSearch(ctx context.Context) error {
	ix, err := search.Open(s.searchDB(), s.log, s.searchRoots)
	if err != nil {
		return err
	}
	s.search = ix
	go ix.Run(ctx)
	return nil
}

// searchRoots lists every indexed place: homes, mounts (walked less often: often network
// shares) and scheduled drive copies.
func (s *Server) searchRoots(ctx context.Context) []search.Root {
	var out []search.Root
	for _, uid := range s.store.UserIDs(ctx) {
		dir := s.homeDir(uid)
		if _, err := os.Stat(dir); err == nil {
			out = append(out, search.Root{Key: "home:" + strconv.FormatInt(uid, 10), Dir: dir})
		}
	}
	if mounts, err := s.store.ListMounts(ctx); err == nil {
		for _, m := range mounts {
			if s.cfg.MountAllowed(m.Path) {
				out = append(out, search.Root{Key: "mount:" + strconv.FormatInt(m.ID, 10), Dir: m.Path, Slow: true, NamesOnly: !s.cfg.SearchMountText})
			}
		}
	}
	if list, err := s.store.AllClouds(ctx); err == nil {
		for i := range list {
			if c := &list[i]; c.IntervalHours > 0 {
				out = append(out, search.Root{Key: "cloud:" + strconv.FormatInt(c.ID, 10), Dir: s.cloudDir(c)})
			}
		}
	}
	return out
}

// indexKey is where a space's path lives in the index: homes and shares by their owner's
// home, mounts and drives by their own id.
func indexKey(sp *vfs.Space, rel string) (string, string) {
	if sp.OwnerID != 0 {
		return "home:" + strconv.FormatInt(sp.OwnerID, 10), sp.HomePath(rel)
	}
	return sp.ID, vfs.Clean(rel)
}

// indexTouch tells the index a folder changed.
func (s *Server) indexTouch(sp *vfs.Space, rel string) {
	if s.search == nil || sp == nil {
		return
	}
	key, p := indexKey(sp, rel)
	s.search.Touch(key, p)
}

// searchScopes are the places one person may search.
func (s *Server) searchScopes(ctx context.Context, uid int64) []search.Scope {
	out := []search.Scope{{Key: "home:" + strconv.FormatInt(uid, 10), Space: "home"}}
	if mounts, err := s.store.MountsFor(ctx, uid); err == nil {
		for _, m := range mounts {
			id := "mount:" + strconv.FormatInt(m.ID, 10)
			out = append(out, search.Scope{Key: id, Space: id})
		}
	}
	if clouds, err := s.store.CloudsFor(ctx, uid); err == nil {
		for _, c := range clouds {
			if c.IntervalHours > 0 {
				id := "cloud:" + strconv.FormatInt(c.ID, 10)
				out = append(out, search.Scope{Key: id, Space: id})
			}
		}
	}
	if shares, err := s.store.SharesWith(ctx, uid); err == nil {
		for _, sh := range shares {
			if !sh.IsDir || sh.OwnerID == uid {
				continue
			}
			out = append(out, search.Scope{Key: "home:" + strconv.FormatInt(sh.OwnerID, 10), Prefix: vfs.Clean(sh.Path),
				Space: "share:" + strconv.FormatInt(sh.ID, 10), Base: vfs.Clean(sh.Path)})
		}
	}
	return out
}

// handleGlobalSearch searches everywhere the person can go: GET /search?q=&limit=
func (s *Server) handleGlobalSearch(w http.ResponseWriter, r *http.Request) {
	if s.search == nil {
		writeErr(w, http.StatusServiceUnavailable, "search is not available")
		return
	}
	start := time.Now()
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	q := search.Parse(r.URL.Query().Get("q"))
	hits, err := s.search.Search(r.Context(), q, s.searchScopes(r.Context(), me(r)), limit)
	if err != nil {
		s.fail(w, err, "search")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hits": hits, "took_ms": time.Since(start).Milliseconds()})
}

type searchEntry struct {
	vfs.Entry
	Match   string `json:"match,omitempty"`
	Snippet string `json:"snippet,omitempty"`
}

// indexedFolderSearch answers /fs/search (one space, below one folder) from the index.
func (s *Server) indexedFolderSearch(w http.ResponseWriter, r *http.Request, sp *vfs.Space) {
	ctx := r.Context()
	if sp.File != "" {
		writeJSON(w, http.StatusOK, map[string]any{"entries": []searchEntry{}, "partial": false})
		return
	}
	key, prefix := indexKey(sp, r.URL.Query().Get("path"))
	base := ""
	if strings.HasPrefix(sp.ID, "share:") {
		base = vfs.Clean(sp.Base)
	}
	scope := search.Scope{Key: key, Prefix: prefix, Space: sp.ID, Base: base}
	hits, err := s.search.Search(ctx, search.Parse(r.URL.Query().Get("q")), []search.Scope{scope}, 300)
	if err != nil {
		s.fail(w, err, "search")
		return
	}
	out := make([]searchEntry, 0, len(hits))
	for _, h := range hits {
		e := vfs.Entry{Name: h.Name, Path: h.Path, IsDir: h.IsDir, Size: h.Size, Modified: h.Modified}
		if !h.IsDir {
			e.Mime = vfs.MimeOf(h.Name)
		}
		out = append(out, searchEntry{Entry: e, Match: h.Match, Snippet: h.Snippet})
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out, "partial": len(out) >= 300})
}

func (s *Server) handleSearchStatus(w http.ResponseWriter, r *http.Request) {
	if s.search == nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": true, "status": s.search.Status(r.Context(), s.searchDB())})
}

// handleSearchRebuild drops the index and builds it again (admins).
func (s *Server) handleSearchRebuild(w http.ResponseWriter, r *http.Request) {
	if u := userFrom(r.Context()); u == nil || u.Role != "admin" {
		writeErr(w, http.StatusForbidden, "only admins can rebuild the index")
		return
	}
	if s.search == nil {
		writeErr(w, http.StatusServiceUnavailable, "search is not available")
		return
	}
	if err := s.search.Rebuild(r.Context()); err != nil {
		s.fail(w, err, "rebuild")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}
