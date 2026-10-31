package server

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/webdav"

	"github.com/abjelosevic88/pocket-homelab-files/internal/store"
	"github.com/abjelosevic88/pocket-homelab-files/internal/vfs"
)

// WebDAV: a person's files at /dav/ (and the Nextcloud-shaped
// /remote.php/dav/files/<user>/ and /remote.php/webdav/). The root is their
// home; "Shared with me" and "Mounts" are folders made of what others shared
// and what admins mounted. Sign in with Basic auth: any user name, an API
// token as the password (or the account password, or a Pocket Core token —
// other Pocket apps use a link token here).

type davLocks struct {
	mu sync.Mutex
	ls map[int64]webdav.LockSystem
}

func newDavLocks() *davLocks { return &davLocks{ls: map[int64]webdav.LockSystem{}} }

func (d *davLocks) For(uid int64) webdav.LockSystem {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ls[uid] == nil {
		d.ls[uid] = webdav.NewMemLS()
	}
	return d.ls[uid]
}

var ncFilesPrefix = regexp.MustCompile(`^/remote\.php/dav/files/[^/]+`)

func (s *Server) handleDAV(w http.ResponseWriter, r *http.Request) {
	u, err := s.davUser(r)
	if errors.Is(err, errLinkScope) {
		writeErr(w, http.StatusForbidden, "this Pocket Core link does not allow "+r.Method)
		return
	}
	if err != nil || u == nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="Pocket Files", charset="UTF-8"`)
		http.Error(w, "sign in with your email and an app password (Settings → WebDAV)", http.StatusUnauthorized)
		return
	}
	ctx := context.WithValue(r.Context(), userKey, u)
	prefix := "/dav"
	switch {
	case strings.HasPrefix(r.URL.Path, "/remote.php/webdav"):
		prefix = "/remote.php/webdav"
	case strings.HasPrefix(r.URL.Path, "/remote.php/dav/files/"):
		prefix = ncFilesPrefix.FindString(r.URL.Path)
	}
	shares, mounts := s.davNames(ctx, u.ID)
	dfs := &davFS{s: s, uid: u.ID, home: s.homeSpace(u.ID), shares: shares, mounts: mounts}
	h := &webdav.Handler{
		Prefix:     prefix,
		FileSystem: dfs,
		LockSystem: s.davLock.For(u.ID),
		Logger: func(r *http.Request, err error) {
			if err != nil && !os.IsNotExist(err) {
				s.log.Debug("webdav", "method", r.Method, "path", r.URL.Path, "err", err)
			}
		},
	}
	s.seedUser(ctx, u.ID)
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		w.Header().Set("Content-Security-Policy", "sandbox")
		w.Header().Set("X-Content-Type-Options", "nosniff")
	}
	switch r.Method {
	case "PUT", "DELETE", "MKCOL", "MOVE", "COPY", "PROPPATCH":
		defer s.touch()
		// Once the request is done, the search index looks at the folders it changed.
		defer func() {
			names := []string{strings.TrimPrefix(r.URL.Path, prefix)}
			if dst, err := url.Parse(r.Header.Get("Destination")); err == nil && dst.Path != "" {
				names = append(names, strings.TrimPrefix(dst.Path, prefix))
			}
			for _, n := range names {
				if sp, rel, virtual, err := dfs.resolve(n); err == nil && virtual == "" && sp != nil {
					s.indexTouch(sp, path.Dir(vfs.Clean(rel)))
				}
			}
		}()
	}
	h.ServeHTTP(w, r.WithContext(ctx))
}

// davUser accepts only explicit credentials (never the browser's session
// cookie, so other sites cannot drive WebDAV through it).
func (s *Server) davUser(r *http.Request) (*store.User, error) {
	if r.Header.Get("Authorization") == "" && r.Header.Get("X-API-Key") == "" {
		return nil, http.ErrNoCookie
	}
	return s.resolveUser(r)
}

type davFS struct {
	s      *Server
	uid    int64
	home   *vfs.Space
	shares map[string]*vfs.Space
	mounts map[string]*vfs.Space
}

func notExist(name string) error { return &os.PathError{Op: "open", Path: name, Err: os.ErrNotExist} }

func davErr(name string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, vfs.ErrNotFound), errors.Is(err, fs.ErrNotExist):
		return notExist(name)
	case errors.Is(err, vfs.ErrExists):
		return &os.PathError{Op: "create", Path: name, Err: os.ErrExist}
	case errors.Is(err, vfs.ErrForbidden), errors.Is(err, vfs.ErrOutside), errors.Is(err, vfs.ErrBadName):
		return &os.PathError{Op: "write", Path: name, Err: os.ErrPermission}
	}
	return err
}

// resolve maps a WebDAV name to a space and a path in it; virt names the
// made-up folders ("root", "shared", "mounts").
func (d *davFS) resolve(name string) (*vfs.Space, string, string, error) {
	name = vfs.Clean(name)
	if name == "/" {
		return d.home, "/", "root", nil
	}
	parts := strings.SplitN(strings.TrimPrefix(name, "/"), "/", 3)
	var set map[string]*vfs.Space
	switch parts[0] {
	case davShared:
		set = d.shares
	case davMounts:
		set = d.mounts
	default:
		return d.home, name, "", nil
	}
	if len(parts) == 1 {
		if parts[0] == davShared {
			return nil, "", "shared", nil
		}
		return nil, "", "mounts", nil
	}
	sp := set[parts[1]]
	if sp == nil {
		return nil, "", "", notExist(name)
	}
	rel := "/"
	if len(parts) == 3 {
		rel = "/" + parts[2]
	}
	return sp, rel, "", nil
}

func (d *davFS) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	sp, rel, virt, err := d.resolve(name)
	if err != nil {
		return err
	}
	if virt != "" || rel == "/" {
		return davErr(name, vfs.ErrExists)
	}
	if !sp.Writable() {
		return davErr(name, vfs.ErrForbidden)
	}
	if err := reserved(sp, rel); err != nil {
		return davErr(name, vfs.ErrForbidden)
	}
	if err := vfs.ValidName(path.Base(rel)); err != nil {
		return davErr(name, err)
	}
	abs, err := sp.Abs(rel)
	if err != nil {
		return davErr(name, err)
	}
	return davErr(name, os.Mkdir(abs, 0o755))
}

func (d *davFS) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	sp, rel, virt, err := d.resolve(name)
	if err != nil {
		return nil, err
	}
	write := flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC|os.O_APPEND) != 0
	switch virt {
	case "shared", "mounts":
		if write {
			return nil, davErr(name, vfs.ErrForbidden)
		}
		return d.virtualDir(virt), nil
	}
	if write {
		if !sp.Writable() || (rel == "/" && sp.File == "") {
			return nil, davErr(name, vfs.ErrForbidden)
		}
		if err := reserved(sp, rel); err != nil {
			return nil, davErr(name, vfs.ErrForbidden)
		}
		if err := vfs.ValidName(path.Base(rel)); err != nil && sp.File == "" {
			return nil, davErr(name, err)
		}
	}
	abs, err := sp.Resolve(rel)
	if err != nil {
		return nil, davErr(name, err)
	}
	f, err := os.OpenFile(abs, flag, 0o644)
	if err != nil {
		return nil, davErr(name, err)
	}
	if fi, err := f.Stat(); err == nil && fi.IsDir() {
		var extra []fs.FileInfo
		if virt == "root" {
			if len(d.shares) > 0 {
				extra = append(extra, fakeDir(davShared))
			}
			if len(d.mounts) > 0 {
				extra = append(extra, fakeDir(davMounts))
			}
		}
		return &davDir{File: f, extra: extra, root: virt == "root"}, nil
	}
	if sp.File != "" {
		return &renamedFile{File: f, name: sp.File}, nil
	}
	return f, nil
}

func (d *davFS) RemoveAll(ctx context.Context, name string) error {
	sp, rel, virt, err := d.resolve(name)
	if err != nil {
		return err
	}
	if virt != "" || (rel == "/" && sp.File == "") || !sp.Writable() {
		return davErr(name, vfs.ErrForbidden)
	}
	if sp.OwnerID != 0 {
		if sp.File != "" {
			return davErr(name, vfs.ErrForbidden)
		}
		return davErr(name, d.s.toTrash(ctx, sp, rel, d.uid))
	}
	return davErr(name, sp.Remove(rel))
}

func (d *davFS) Rename(ctx context.Context, oldName, newName string) error {
	src, srel, sv, err := d.resolve(oldName)
	if err != nil {
		return err
	}
	dst, drel, dv, err := d.resolve(newName)
	if err != nil {
		return err
	}
	if sv != "" || dv != "" {
		return davErr(newName, vfs.ErrForbidden)
	}
	if err := reserved(dst, drel); err != nil {
		return davErr(newName, vfs.ErrForbidden)
	}
	got, err := vfs.Move(src, srel, dst, drel, false, vfs.Fail)
	if err != nil {
		return davErr(newName, err)
	}
	d.s.followMove(ctx, src, srel, dst, got)
	return nil
}

func (d *davFS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	sp, rel, virt, err := d.resolve(name)
	if err != nil {
		return nil, err
	}
	switch virt {
	case "shared":
		return fakeDir(davShared), nil
	case "mounts":
		return fakeDir(davMounts), nil
	}
	abs, err := sp.Resolve(rel)
	if err != nil {
		return nil, davErr(name, err)
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return nil, davErr(name, err)
	}
	if sp.File != "" {
		return renamedInfo{fi, sp.File}, nil
	}
	return fi, nil
}

func (d *davFS) virtualDir(kind string) webdav.File {
	set := d.shares
	name := davShared
	if kind == "mounts" {
		set, name = d.mounts, davMounts
	}
	var infos []fs.FileInfo
	for _, k := range sortedKeys(set) {
		sp := set[k]
		if sp.File != "" {
			if fi, err := d.Stat(context.Background(), "/"+name+"/"+k); err == nil {
				infos = append(infos, renamedInfo{fi, k})
			}
			continue
		}
		infos = append(infos, fakeDir(k))
	}
	return &virtualDir{info: fakeDir(name), entries: infos}
}

// ---------- made-up files ----------

type fakeInfo struct {
	name string
}

func fakeDir(name string) fakeInfo    { return fakeInfo{name} }
func (f fakeInfo) Name() string       { return f.name }
func (f fakeInfo) Size() int64        { return 0 }
func (f fakeInfo) Mode() fs.FileMode  { return fs.ModeDir | 0o555 }
func (f fakeInfo) ModTime() time.Time { return time.Now().Truncate(time.Hour) }
func (f fakeInfo) IsDir() bool        { return true }
func (f fakeInfo) Sys() any           { return nil }
func (r renamedInfo) Name() string    { return r.name }
func (f *renamedFile) Stat() (fs.FileInfo, error) {
	fi, err := f.File.Stat()
	if err != nil {
		return nil, err
	}
	return renamedInfo{fi, f.name}, nil
}

type renamedInfo struct {
	fs.FileInfo
	name string
}

type renamedFile struct {
	*os.File
	name string
}

type virtualDir struct {
	info    fs.FileInfo
	entries []fs.FileInfo
	read    bool
}

func (v *virtualDir) Close() error                   { return nil }
func (v *virtualDir) Read([]byte) (int, error)       { return 0, io.EOF }
func (v *virtualDir) Seek(int64, int) (int64, error) { return 0, nil }
func (v *virtualDir) Write([]byte) (int, error)      { return 0, os.ErrPermission }
func (v *virtualDir) Stat() (fs.FileInfo, error)     { return v.info, nil }
func (v *virtualDir) Readdir(count int) ([]fs.FileInfo, error) {
	if v.read && count > 0 {
		return nil, io.EOF
	}
	v.read = true
	return v.entries, nil
}

// davDir hides unfinished uploads and adds the made-up folders to the root.
type davDir struct {
	*os.File
	extra []fs.FileInfo
	root  bool
	done  bool
}

func (d *davDir) Readdir(count int) ([]fs.FileInfo, error) {
	if d.done && count > 0 {
		return nil, io.EOF
	}
	d.done = true
	all, err := d.File.Readdir(-1)
	if err != nil {
		return nil, err
	}
	out := make([]fs.FileInfo, 0, len(all)+len(d.extra))
	for _, fi := range all {
		if vfs.IsTemp(fi.Name()) {
			continue
		}
		if d.root && (strings.EqualFold(fi.Name(), davShared) || strings.EqualFold(fi.Name(), davMounts)) && len(d.extra) > 0 {
			continue
		}
		out = append(out, fi)
	}
	return append(out, d.extra...), nil
}
