package server

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-files/internal/store"
	"github.com/abjelosevic88/pocket-homelab-files/internal/vfs"
)

// reserved refuses the names the WebDAV root uses for shares and mounts
// at the top of someone's home.
func reserved(sp *vfs.Space, rel string) error {
	if sp.ID != "home" {
		return nil
	}
	rel = vfs.Clean(rel)
	if path.Dir(rel) == "/" && (strings.EqualFold(path.Base(rel), davShared) || strings.EqualFold(path.Base(rel), davMounts)) {
		return badRequest(fmt.Sprintf("%q is kept for the WebDAV view of shares and mounts; pick another name", path.Base(rel)))
	}
	return nil
}

type listing struct {
	Space    spaceView   `json:"space"`
	Path     string      `json:"path"`
	Entry    *vfs.Entry  `json:"entry"`
	Entries  []vfs.Entry `json:"entries"`
	Writable bool        `json:"writable"`
}

func (s *Server) viewOf(ctx context.Context, uid int64, sp *vfs.Space) spaceView {
	v := spaceView{ID: sp.ID, Name: sp.Name, Perm: sp.Perm.String(), IsDir: sp.File == ""}
	switch {
	case sp.ID == "home":
		v.Kind = "home"
	case strings.HasPrefix(sp.ID, "mount:"):
		v.Kind = "mount"
	default:
		v.Kind = "share"
		if u, err := s.store.GetUser(ctx, sp.OwnerID); err == nil && u.ID != uid {
			v.Owner = firstNonEmpty(u.Name, u.Email)
		}
	}
	return v
}

func (s *Server) showHidden(r *http.Request) bool {
	if r.URL.Query().Has("hidden") {
		return queryBool(r, "hidden")
	}
	return s.store.UserSetting(r.Context(), me(r), "show_hidden") == "1"
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sp, err := s.spaceFromQuery(r)
	if err != nil {
		s.fail(w, err, "open")
		return
	}
	rel := vfs.Clean(r.URL.Query().Get("path"))
	entry, err := sp.Stat(rel)
	if err != nil {
		s.fail(w, err, "stat")
		return
	}
	var entries []vfs.Entry
	if entry.IsDir {
		if entries, err = sp.List(rel, s.showHidden(r)); err != nil {
			s.fail(w, err, "list")
			return
		}
	}
	s.annotate(ctx, me(r), sp, entries)
	writeJSON(w, http.StatusOK, listing{Space: s.viewOf(ctx, me(r), sp), Path: rel, Entry: entry, Entries: nonNilEntries(entries), Writable: sp.Writable()})
}

func nonNilEntries(e []vfs.Entry) []vfs.Entry {
	if e == nil {
		return []vfs.Entry{}
	}
	return e
}

// annotate marks starred entries and, in the person's own home, shared ones.
func (s *Server) annotate(ctx context.Context, uid int64, sp *vfs.Space, entries []vfs.Entry) {
	if len(entries) == 0 {
		return
	}
	stars := s.store.StarredSet(ctx, uid, sp.ID)
	var shared map[string]int
	if sp.ID == "home" {
		shared = s.store.SharedPaths(ctx, uid)
	}
	for i := range entries {
		entries[i].Starred = stars[entries[i].Path]
		if shared != nil {
			entries[i].Shared = shared[entries[i].Path]
		}
	}
}

func (s *Server) handleStat(w http.ResponseWriter, r *http.Request) {
	sp, err := s.spaceFromQuery(r)
	if err != nil {
		s.fail(w, err, "open")
		return
	}
	e, err := sp.Stat(r.URL.Query().Get("path"))
	if err != nil {
		s.fail(w, err, "stat")
		return
	}
	writeJSON(w, http.StatusOK, e)
}

// contentDisposition builds a header that survives any file name.
func contentDisposition(kind, name string) string {
	ascii := strings.Map(func(r rune) rune {
		if r < 32 || r > 126 || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	return fmt.Sprintf(`%s; filename="%s"; filename*=UTF-8''%s`, kind, ascii, url.PathEscape(name))
}

// serveFile streams one file with ranges. Inline previews of active types
// (HTML, XML, scripts) are sent as text, and everything but PDFs carries a
// sandbox policy, so a shared file can never run script on this site.
func serveFile(w http.ResponseWriter, r *http.Request, f *os.File, fi os.FileInfo, name string, download bool) {
	typ := vfs.MimeOf(name)
	if download {
		w.Header().Set("Content-Disposition", contentDisposition("attachment", name))
	} else {
		w.Header().Set("Content-Disposition", contentDisposition("inline", name))
		// SVG stays an image (the sandbox below keeps its scripts dead)
		if strings.Contains(typ, "html") || (strings.Contains(typ, "xml") && !strings.Contains(typ, "svg")) || strings.HasPrefix(typ, "text/") || strings.Contains(typ, "javascript") {
			typ = "text/plain; charset=utf-8"
		}
	}
	if typ != "application/pdf" {
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self' data:; media-src 'self'; style-src 'unsafe-inline'")
	}
	w.Header().Set("Content-Type", typ)
	w.Header().Set("Cache-Control", "private, max-age=0, must-revalidate")
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

func (s *Server) handleRaw(w http.ResponseWriter, r *http.Request) {
	sp, err := s.spaceFromQuery(r)
	if err != nil {
		s.fail(w, err, "open")
		return
	}
	rel := r.URL.Query().Get("path")
	f, fi, err := sp.Open(rel)
	if err != nil {
		s.fail(w, err, "open")
		return
	}
	defer f.Close()
	if fi.IsDir() {
		writeErr(w, http.StatusBadRequest, "that is a folder; download it as a zip")
		return
	}
	name := fi.Name()
	if sp.File != "" {
		name = sp.File
	}
	serveFile(w, r, f, fi, name, queryBool(r, "dl"))
}

// handleZip streams a zip of the given paths (files and whole folders).
func (s *Server) handleZip(w http.ResponseWriter, r *http.Request) {
	sp, err := s.spaceFromQuery(r)
	if err != nil {
		s.fail(w, err, "open")
		return
	}
	paths := r.URL.Query()["path"]
	if len(paths) == 0 {
		paths = []string{"/"}
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		if len(paths) == 1 {
			name = path.Base(vfs.Clean(paths[0]))
			if name == "/" {
				name = sp.Name
			}
		} else {
			name = "Files"
		}
	}
	if !strings.HasSuffix(strings.ToLower(name), ".zip") {
		name += ".zip"
	}
	for _, p := range paths {
		if _, err := sp.Stat(p); err != nil {
			s.fail(w, err, "zip")
			return
		}
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", contentDisposition("attachment", name))
	writeZip(r.Context(), w, sp, paths, s.showHidden(r))
}

func writeZip(ctx context.Context, w io.Writer, sp *vfs.Space, paths []string, hidden bool) {
	zw := zip.NewWriter(w)
	defer zw.Close()
	add := func(rel, inZip string, e *vfs.Entry) error {
		if ctx.Err() != nil {
			return io.EOF
		}
		h := &zip.FileHeader{Name: inZip, Method: zip.Store, Modified: e.Modified}
		if e.IsDir {
			h.Name += "/"
			_, err := zw.CreateHeader(h)
			return err
		}
		out, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		f, _, err := sp.Open(rel)
		if err != nil {
			return nil
		}
		defer f.Close()
		_, err = io.Copy(out, f)
		return err
	}
	for _, p := range paths {
		p = vfs.Clean(p)
		e, err := sp.Stat(p)
		if err != nil {
			continue
		}
		base := path.Base(p)
		if p == "/" {
			base = ""
		}
		if sp.File != "" {
			base = sp.File
		}
		if !e.IsDir {
			if err := add(p, base, e); err != nil {
				return
			}
			continue
		}
		err = sp.Walk(p, hidden, func(c *vfs.Entry) error {
			rel := strings.TrimPrefix(strings.TrimPrefix(c.Path, p), "/")
			return add(c.Path, strings.TrimPrefix(path.Join(base, rel), "/"), c)
		})
		if err != nil {
			return
		}
	}
}

// handleUpload stores the request body as a file. Folders in name are
// created; conflict is rename (default), overwrite, skip or fail.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sp, err := s.spaceFromQuery(r)
	if err != nil {
		s.fail(w, err, "open")
		return
	}
	q := r.URL.Query()
	dir, name := vfs.Clean(q.Get("path")), q.Get("name")
	if name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := reserved(sp, vfs.Join(dir, strings.Split(strings.Trim(name, "/"), "/")[0])); err != nil {
		s.fail(w, err, "upload")
		return
	}
	var mtime time.Time
	if ms, err := strconv.ParseInt(q.Get("mtime"), 10, 64); err == nil && ms > 0 {
		mtime = time.UnixMilli(ms)
	}
	e, err := sp.Write(dir, name, r.Body, q.Get("conflict"), mtime)
	if err != nil {
		s.fail(w, err, "upload")
		return
	}
	s.touch()
	if e == nil {
		writeJSON(w, http.StatusOK, map[string]any{"skipped": true})
		return
	}
	s.publish(ctx, "files.uploaded", "file:"+sp.ID+":"+e.Path, me(r), map[string]any{"space": sp.ID, "path": e.Path, "name": e.Name, "size": e.Size, "mime": e.Mime})
	writeJSON(w, http.StatusOK, e)
}

func (s *Server) handleMkdir(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Space string `json:"space"`
		Path  string `json:"path"`
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
	rel := vfs.Clean(in.Path)
	if err := vfs.ValidName(path.Base(rel)); err != nil {
		s.fail(w, err, "mkdir")
		return
	}
	if err := reserved(sp, rel); err != nil {
		s.fail(w, err, "mkdir")
		return
	}
	if err := sp.Mkdir(rel); err != nil {
		s.fail(w, err, "mkdir")
		return
	}
	s.touch()
	e, _ := sp.Stat(rel)
	writeJSON(w, http.StatusCreated, e)
}

func (s *Server) handleRename(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Space string `json:"space"`
		Path  string `json:"path"`
		Name  string `json:"name"`
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
	in.Name = strings.TrimSpace(in.Name)
	if err := vfs.ValidName(in.Name); err != nil {
		s.fail(w, err, "rename")
		return
	}
	from := vfs.Clean(in.Path)
	to := vfs.Join(path.Dir(from), in.Name)
	if err := reserved(sp, to); err != nil {
		s.fail(w, err, "rename")
		return
	}
	got, err := vfs.Move(sp, from, sp, to, false, vfs.Fail)
	if err != nil {
		s.fail(w, err, "rename")
		return
	}
	s.followMove(r.Context(), sp, from, sp, got)
	s.touch()
	e, _ := sp.Stat(got)
	writeJSON(w, http.StatusOK, e)
}

// followMove keeps shares and stars pointing at what moved.
func (s *Server) followMove(ctx context.Context, from *vfs.Space, frel string, to *vfs.Space, trel string) {
	if from.OwnerID != 0 && from.OwnerID == to.OwnerID {
		s.store.MoveSharePaths(ctx, from.OwnerID, from.HomePath(frel), to.HomePath(trel))
	} else if from.OwnerID != 0 {
		s.store.DropSharePaths(ctx, from.OwnerID, from.HomePath(frel))
	}
	if from.ID == to.ID {
		s.store.MoveStars(ctx, from.ID, frel, trel)
	} else {
		s.store.DropStars(ctx, from.ID, frel)
	}
}

type moveIn struct {
	Space    string   `json:"space"`
	Paths    []string `json:"paths"`
	ToSpace  string   `json:"to_space"`
	ToPath   string   `json:"to_path"` // folder
	Conflict string   `json:"conflict"`
}

func (s *Server) handleMove(w http.ResponseWriter, r *http.Request) { s.moveOrCopy(w, r, false) }
func (s *Server) handleCopy(w http.ResponseWriter, r *http.Request) { s.moveOrCopy(w, r, true) }

func (s *Server) moveOrCopy(w http.ResponseWriter, r *http.Request, copyOnly bool) {
	ctx := r.Context()
	var in moveIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	src, err := s.space(ctx, me(r), in.Space)
	if err != nil {
		s.fail(w, err, "open")
		return
	}
	if in.ToSpace == "" {
		in.ToSpace = in.Space
	}
	dst, err := s.space(ctx, me(r), in.ToSpace)
	if err != nil {
		s.fail(w, err, "open")
		return
	}
	if in.Conflict == "" {
		in.Conflict = vfs.Rename
	}
	type result struct {
		From  string `json:"from"`
		To    string `json:"to,omitempty"`
		Error string `json:"error,omitempty"`
	}
	out := []result{}
	for _, p := range in.Paths {
		p = vfs.Clean(p)
		target := vfs.Join(in.ToPath, path.Base(p))
		if src.File != "" {
			target = vfs.Join(in.ToPath, src.File)
		}
		if err := reserved(dst, target); err != nil {
			out = append(out, result{From: p, Error: err.Error()})
			continue
		}
		got, err := vfs.Move(src, p, dst, target, copyOnly, in.Conflict)
		if err != nil {
			out = append(out, result{From: p, Error: err.Error()})
			continue
		}
		if !copyOnly && got != "" {
			s.followMove(ctx, src, p, dst, got)
		}
		out = append(out, result{From: p, To: got})
	}
	s.touch()
	writeJSON(w, http.StatusOK, out)
}

// handleDelete moves things from a home (or a share of one) to the owner's
// trash; in a mount, or with permanent, they are deleted for good.
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		Space     string   `json:"space"`
		Paths     []string `json:"paths"`
		Permanent bool     `json:"permanent"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	sp, err := s.space(ctx, me(r), in.Space)
	if err != nil {
		s.fail(w, err, "open")
		return
	}
	if !sp.Writable() {
		s.fail(w, vfs.ErrForbidden, "delete")
		return
	}
	trashed, deleted := 0, 0
	var errs []string
	for _, p := range in.Paths {
		p = vfs.Clean(p)
		var err error
		if sp.OwnerID != 0 && !in.Permanent {
			err = s.toTrash(ctx, sp, p, me(r))
			if err == nil {
				trashed++
			}
		} else {
			err = sp.Remove(p)
			if err == nil {
				deleted++
				if sp.OwnerID != 0 {
					s.store.DropSharePaths(ctx, sp.OwnerID, sp.HomePath(p))
				}
				s.store.DropStars(ctx, sp.ID, p)
			}
		}
		if err != nil {
			errs = append(errs, path.Base(p)+": "+err.Error())
			continue
		}
		s.publish(ctx, "files.deleted", "file:"+sp.ID+":"+p, me(r), map[string]any{"space": sp.ID, "path": p, "trash": sp.OwnerID != 0 && !in.Permanent})
	}
	s.touch()
	writeJSON(w, http.StatusOK, map[string]any{"trashed": trashed, "deleted": deleted, "errors": errs})
}

// toTrash moves a path of someone's home into their trash.
func (s *Server) toTrash(ctx context.Context, sp *vfs.Space, rel string, actor int64) error {
	if (rel == "/" && sp.File == "") || (sp.ID != "home" && rel == "/") {
		return fmt.Errorf("%w: the shared folder itself can only be removed by its owner", vfs.ErrForbidden)
	}
	e, err := sp.Stat(rel)
	if err != nil {
		return err
	}
	size := e.Size
	if e.IsDir {
		_, size = sp.Usage(rel)
	}
	stored := store.NewID(12)
	dir := filepath.Join(s.cfg.TrashDir, strconv.FormatInt(sp.OwnerID, 10))
	if err := vfs.MoveOut(sp, rel, filepath.Join(dir, stored)); err != nil {
		return err
	}
	homePath := sp.HomePath(rel)
	it := &store.TrashItem{UserID: sp.OwnerID, Name: path.Base(homePath), OrigPath: homePath, Stored: stored, IsDir: e.IsDir, Size: size, DeletedBy: actor}
	if err := s.store.AddTrash(ctx, it); err != nil {
		return err
	}
	s.store.DropSharePaths(ctx, sp.OwnerID, homePath)
	s.store.DropStars(ctx, sp.ID, rel)
	if sp.ID != "home" {
		s.store.DropStars(ctx, "home", homePath)
	}
	return nil
}

func (s *Server) handleTrash(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListTrash(r.Context(), me(r))
	if err != nil {
		s.fail(w, err, "trash")
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	it, err := s.store.GetTrash(ctx, me(r), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "trash")
		return
	}
	home := s.homeSpace(me(r))
	got, err := vfs.MoveIn(filepath.Join(s.cfg.TrashDir, strconv.FormatInt(it.UserID, 10), it.Stored), home, it.OrigPath)
	if err != nil {
		s.fail(w, err, "restore")
		return
	}
	_ = s.store.DeleteTrash(ctx, it.ID)
	s.touch()
	writeJSON(w, http.StatusOK, map[string]string{"path": got})
}

func (s *Server) purgeTrashItem(ctx context.Context, it *store.TrashItem) {
	_ = os.RemoveAll(filepath.Join(s.cfg.TrashDir, strconv.FormatInt(it.UserID, 10), it.Stored))
	_ = s.store.DeleteTrash(ctx, it.ID)
}

func (s *Server) handlePurge(w http.ResponseWriter, r *http.Request) {
	it, err := s.store.GetTrash(r.Context(), me(r), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "trash")
		return
	}
	s.purgeTrashItem(r.Context(), it)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleEmptyTrash(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListTrash(r.Context(), me(r))
	if err != nil {
		s.fail(w, err, "trash")
		return
	}
	for i := range items {
		s.purgeTrashItem(r.Context(), &items[i])
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSearch finds names containing q below path (time-boxed).
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	sp, err := s.spaceFromQuery(r)
	if err != nil {
		s.fail(w, err, "open")
		return
	}
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if q == "" {
		writeJSON(w, http.StatusOK, map[string]any{"entries": []vfs.Entry{}, "partial": false})
		return
	}
	terms := strings.Fields(q)
	deadline := time.Now().Add(6 * time.Second)
	out := []vfs.Entry{}
	partial := false
	err = sp.Walk(r.URL.Query().Get("path"), s.showHidden(r), func(e *vfs.Entry) error {
		if time.Now().After(deadline) || r.Context().Err() != nil {
			partial = true
			return io.EOF
		}
		name := strings.ToLower(e.Name)
		for _, t := range terms {
			if !strings.Contains(name, t) {
				return nil
			}
		}
		out = append(out, *e)
		if len(out) >= 300 {
			partial = true
			return io.EOF
		}
		return nil
	})
	if err != nil {
		s.fail(w, err, "search")
		return
	}
	s.annotate(r.Context(), me(r), sp, out)
	writeJSON(w, http.StatusOK, map[string]any{"entries": out, "partial": partial})
}

func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	sp, err := s.spaceFromQuery(r)
	if err != nil {
		s.fail(w, err, "open")
		return
	}
	files, size := sp.Usage(r.URL.Query().Get("path"))
	writeJSON(w, http.StatusOK, map[string]any{"files": files, "size": size})
}

// handleSaveText writes a small text file (the editor in the preview).
func (s *Server) handleSaveText(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Space   string `json:"space"`
		Path    string `json:"path"`
		Content string `json:"content"`
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
	rel := vfs.Clean(in.Path)
	if sp.File != "" {
		writeErr(w, http.StatusBadRequest, "a shared file cannot be edited here")
		return
	}
	e, err := sp.Write(path.Dir(rel), path.Base(rel), strings.NewReader(in.Content), vfs.Overwrite, time.Time{})
	if err != nil {
		s.fail(w, err, "save")
		return
	}
	s.touch()
	writeJSON(w, http.StatusOK, e)
}

// ---------- stars ----------

type starView struct {
	store.Star
	SpaceName string     `json:"space_name"`
	Entry     *vfs.Entry `json:"entry"`
	Missing   bool       `json:"missing"`
}

func (s *Server) handleStars(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	stars, err := s.store.Stars(ctx, me(r))
	if err != nil {
		s.fail(w, err, "stars")
		return
	}
	out := []starView{}
	for _, st := range stars {
		v := starView{Star: st}
		sp, err := s.space(ctx, me(r), st.Space)
		if err != nil {
			v.Missing = true
		} else {
			v.SpaceName = sp.Name
			if e, err := sp.Stat(st.Path); err == nil {
				e.Starred = true
				v.Entry = e
			} else {
				v.Missing = true
			}
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSetStar(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Space string `json:"space"`
		Path  string `json:"path"`
		On    bool   `json:"on"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	isDir := false
	if in.On {
		sp, err := s.space(r.Context(), me(r), in.Space)
		if err != nil {
			s.fail(w, err, "open")
			return
		}
		e, err := sp.Stat(in.Path)
		if err != nil {
			s.fail(w, err, "stat")
			return
		}
		isDir = e.IsDir
	}
	if err := s.store.SetStar(r.Context(), me(r), firstNonEmpty(in.Space, "home"), vfs.Clean(in.Path), isDir, in.On); err != nil {
		s.fail(w, err, "star")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
