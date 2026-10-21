package server

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-files/internal/store"
	"github.com/abjelosevic88/pocket-homelab-files/internal/vfs"
)

// handlePeople lists everyone on the box (name and email only) so people
// can share with each other.
func (s *Server) handlePeople(w http.ResponseWriter, r *http.Request) {
	people, err := s.people(r.Context())
	if err != nil {
		s.fail(w, err, "people")
		return
	}
	writeJSON(w, http.StatusOK, people)
}

// handleShares lists what I shared and what others shared with me.
func (s *Server) handleShares(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	by, err := s.store.SharesBy(ctx, me(r))
	if err != nil {
		s.fail(w, err, "shares")
		return
	}
	with, err := s.store.SharesWith(ctx, me(r))
	if err != nil {
		s.fail(w, err, "shares")
		return
	}
	type withView struct {
		store.Share
		Space string `json:"space"`
		Name  string `json:"name"`
	}
	wv := make([]withView, 0, len(with))
	for i := range with {
		sp := s.shareSpace(&with[i])
		name := sp.Name
		if sp.File != "" {
			name = sp.File
		}
		with[i].Path = "" // the owner's folder layout is theirs
		wv = append(wv, withView{Share: with[i], Space: sp.ID, Name: name})
	}
	writeJSON(w, http.StatusOK, map[string]any{"by_me": by, "with_me": wv})
}

// handleShareOfPath returns my share of one path in my home (or an empty one).
func (s *Server) handleShareOfPath(w http.ResponseWriter, r *http.Request) {
	p := vfs.Clean(r.URL.Query().Get("path"))
	sh, err := s.store.ShareByPath(r.Context(), me(r), p)
	if err != nil {
		writeJSON(w, http.StatusOK, store.Share{OwnerID: me(r), Path: p, Members: []store.Access{}})
		return
	}
	writeJSON(w, http.StatusOK, sh)
}

// handleSaveShare sets who sees one of my files or folders. Only things in
// my own home can be shared this way; mounts are shared by an admin.
func (s *Server) handleSaveShare(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		Path         string         `json:"path"`
		EveryonePerm string         `json:"everyone_perm"`
		Note         string         `json:"note"`
		Members      []store.Access `json:"members"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	home := s.homeSpace(me(r))
	p := vfs.Clean(in.Path)
	e, err := home.Stat(p)
	if err != nil {
		s.fail(w, err, "share")
		return
	}
	if in.EveryonePerm != "" && in.EveryonePerm != "view" && in.EveryonePerm != "edit" {
		writeErr(w, http.StatusBadRequest, "everyone_perm is view, edit or empty")
		return
	}
	sh := &store.Share{OwnerID: me(r), Path: p, IsDir: e.IsDir, EveryonePerm: in.EveryonePerm, Note: strings.TrimSpace(in.Note)}
	seen := map[int64]bool{}
	for _, m := range in.Members {
		if m.Perm != "edit" {
			m.Perm = "view"
		}
		u, err := s.personFor(ctx, m.UserID, m.Email)
		if err != nil {
			s.fail(w, err, "share")
			return
		}
		m.UserID = u.ID
		if m.UserID == me(r) || seen[m.UserID] {
			continue
		}
		seen[m.UserID] = true
		sh.Members = append(sh.Members, m)
	}
	saved, err := s.store.SaveShare(ctx, sh)
	if err != nil {
		s.fail(w, err, "share")
		return
	}
	s.touch()
	if saved == nil {
		writeJSON(w, http.StatusOK, store.Share{OwnerID: me(r), Path: p, IsDir: e.IsDir, Members: []store.Access{}})
		return
	}
	emails := []string{}
	for _, m := range saved.Members {
		emails = append(emails, m.Email)
	}
	s.publish(ctx, "files.shared", "share:"+strconv.FormatInt(saved.ID, 10), me(r), map[string]any{"path": p, "name": e.Name, "is_dir": e.IsDir, "with": emails, "everyone": saved.EveryonePerm})
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleDeleteShare(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteShare(r.Context(), me(r), idParam(r, "id")); err != nil {
		s.fail(w, err, "share")
		return
	}
	s.touch()
	w.WriteHeader(http.StatusNoContent)
}

// ---------- mounts (admins) ----------

func (s *Server) handleListMounts(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	ms, err := s.store.ListMounts(r.Context())
	if err != nil {
		s.fail(w, err, "mounts")
		return
	}
	type view struct {
		store.Mount
		Missing bool   `json:"missing"`
		Allowed bool   `json:"allowed"`
		Error   string `json:"error,omitempty"`
	}
	out := []view{}
	for _, m := range ms {
		v := view{Mount: m, Allowed: s.cfg.MountAllowed(m.Path)}
		if fi, err := os.Stat(m.Path); err != nil {
			v.Missing, v.Error = true, err.Error()
		} else if !fi.IsDir() {
			v.Missing, v.Error = true, "not a folder"
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"mounts": out, "roots": s.cfg.MountRoots})
}

func (s *Server) handleSaveMount(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	var in store.Mount
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	in.ID = idParam(r, "id")
	in.Name = strings.TrimSpace(in.Name)
	in.Path = filepath.Clean(strings.TrimSpace(in.Path))
	if len(s.cfg.MountRoots) == 0 {
		writeErr(w, http.StatusBadRequest, "mounting is off on this box: set MOUNT_ROOTS (and mount those folders into the container)")
		return
	}
	if in.Name == "" || vfs.ValidName(in.Name) != nil || strings.EqualFold(in.Name, davShared) {
		writeErr(w, http.StatusBadRequest, "give the mount a name (no slashes)")
		return
	}
	if !filepath.IsAbs(in.Path) || !s.cfg.MountAllowed(in.Path) {
		writeErr(w, http.StatusBadRequest, "the folder must be inside one of: "+strings.Join(s.cfg.MountRoots, ", "))
		return
	}
	if fi, err := os.Stat(in.Path); err != nil || !fi.IsDir() {
		writeErr(w, http.StatusBadRequest, "that folder does not exist on the server (inside the container)")
		return
	}
	if real, err := filepath.EvalSymlinks(in.Path); err != nil || !s.cfg.MountAllowed(real) {
		writeErr(w, http.StatusBadRequest, "that folder leads outside MOUNT_ROOTS")
		return
	}
	if in.EveryonePerm != "" && in.EveryonePerm != "view" && in.EveryonePerm != "edit" {
		in.EveryonePerm = ""
	}
	var access []store.Access
	seen := map[int64]bool{}
	for _, a := range in.Access {
		u, err := s.personFor(ctx, a.UserID, a.Email)
		if err != nil {
			s.fail(w, err, "save mount")
			return
		}
		a.UserID = u.ID
		if seen[a.UserID] {
			continue
		}
		if a.Perm != "edit" {
			a.Perm = "view"
		}
		seen[a.UserID] = true
		access = append(access, a)
	}
	if in.ID == 0 {
		in.CreatedBy = me(r)
		if !seen[me(r)] && in.EveryonePerm == "" {
			access = append(access, store.Access{UserID: me(r), Perm: "edit"})
		}
	}
	in.Access = access
	if err := s.store.SaveMount(ctx, &in); err != nil {
		s.fail(w, err, "save mount")
		return
	}
	s.log.Info("mount saved", "name", in.Name, "path", in.Path, "by", userFrom(ctx).Email)
	s.touch()
	m, _ := s.store.GetMount(ctx, in.ID)
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) handleDeleteMount(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if err := s.store.DeleteMount(r.Context(), idParam(r, "id")); err != nil {
		s.fail(w, err, "delete mount")
		return
	}
	s.touch()
	w.WriteHeader(http.StatusNoContent)
}

// handleBrowseServer lists sub folders below MOUNT_ROOTS for the mount picker.
func (s *Server) handleBrowseServer(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	p := r.URL.Query().Get("path")
	type dir struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	if p == "" {
		out := []dir{}
		for _, root := range s.cfg.MountRoots {
			out = append(out, dir{Name: root, Path: root})
		}
		writeJSON(w, http.StatusOK, map[string]any{"path": "", "dirs": out})
		return
	}
	p = filepath.Clean(p)
	if !s.cfg.MountAllowed(p) {
		writeErr(w, http.StatusForbidden, "outside MOUNT_ROOTS")
		return
	}
	des, err := os.ReadDir(p)
	if err != nil {
		s.fail(w, vfs.ErrNotFound, "browse")
		return
	}
	out := []dir{}
	for _, de := range des {
		full := filepath.Join(p, de.Name())
		if strings.HasPrefix(de.Name(), ".") {
			continue
		}
		if fi, err := os.Stat(full); err == nil && fi.IsDir() {
			out = append(out, dir{Name: de.Name(), Path: full})
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	parent := filepath.Dir(p)
	if !s.cfg.MountAllowed(parent) {
		parent = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": p, "parent": parent, "dirs": out})
}
