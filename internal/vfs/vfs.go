// Package vfs maps the places a person can open (their home, mounts, folders
// shared with them) onto real folders, and does every file operation inside
// them without ever leaving the folder: paths are cleaned, and symlinks that
// point outside are refused.
package vfs

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Errors the server maps to HTTP answers.
var (
	ErrNotFound  = errors.New("not found")
	ErrExists    = errors.New("already exists")
	ErrForbidden = errors.New("not allowed here")
	ErrBadName   = errors.New("that name cannot be used")
	ErrOutside   = errors.New("that path leads outside this folder")
	ErrNotDir    = errors.New("not a folder")
	ErrIsDir     = errors.New("is a folder")
)

// Perm is what a person may do in a space.
type Perm int

const (
	None Perm = iota
	View
	Edit
)

// ParsePerm reads "view" / "edit".
func ParsePerm(s string) Perm {
	switch s {
	case "edit":
		return Edit
	case "view":
		return View
	}
	return None
}

func (p Perm) String() string {
	switch p {
	case Edit:
		return "edit"
	case View:
		return "view"
	}
	return ""
}

// Space is one place: a person's home, a mount or a share.
type Space struct {
	ID   string // home | mount:<id> | share:<id>
	Name string
	Root string // absolute folder on disk
	// File is set for a share of a single file: the file's name inside Root.
	File    string
	Perm    Perm
	OwnerID int64  // whose home this is (home and share), 0 for mounts
	Base    string // path of Root inside the owner's home ("" for home itself)
}

// Writable reports whether the person may change things here.
func (s *Space) Writable() bool { return s.Perm == Edit }

// HomePath turns a path inside this space into a path inside the owner's home.
func (s *Space) HomePath(rel string) string {
	if s.File != "" {
		return s.Base
	}
	return Join(s.Base, rel)
}

// Entry is one file or folder.
type Entry struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	IsDir    bool      `json:"is_dir"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	Mime     string    `json:"mime"`
	Children int       `json:"children,omitempty"`
	Starred  bool      `json:"starred,omitempty"`
	Shared   int       `json:"shared,omitempty"` // people who see it (1000+ = everyone)
}

// Clean turns any user path into "/a/b" form; "" and "." become "/".
func Clean(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	return path.Clean("/" + p)
}

// Join joins clean paths.
func Join(a, b string) string { return Clean(a + "/" + b) }

// ValidName refuses names that cannot be a single path element.
func ValidName(n string) error {
	if n == "" || n == "." || n == ".." || strings.ContainsAny(n, "/\\\x00") || len(n) > 255 {
		return ErrBadName
	}
	return nil
}

func (s *Space) realRoot() (string, error) {
	root, err := filepath.EvalSymlinks(s.Root)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w: the folder %s is missing on the server", ErrNotFound, s.Name)
		}
		return "", err
	}
	return root, nil
}

func inside(root, p string) bool {
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator)) || root == string(filepath.Separator)
}

// Abs returns the real path of rel inside the space: every folder on the
// way is resolved (symlinks included) and must stay inside; the last
// element is kept as it is, so removing a symlink removes the link. The
// path may not exist yet.
func (s *Space) Abs(rel string) (string, error) {
	rel = Clean(rel)
	if s.File != "" {
		if rel != "/" {
			return "", ErrNotFound
		}
		rel = "/" + s.File
	}
	root, err := s.realRoot()
	if err != nil {
		return "", err
	}
	if rel == "/" {
		return root, nil
	}
	parent := filepath.Join(root, filepath.FromSlash(path.Dir(rel)))
	// resolve the longest existing prefix of the parent
	existing, rest := parent, ""
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		up := filepath.Dir(existing)
		if up == existing {
			break
		}
		rest = filepath.Join(filepath.Base(existing), rest)
		existing = up
	}
	real, err := filepath.EvalSymlinks(existing)
	if err != nil || !inside(root, real) {
		return "", ErrOutside
	}
	if rest != "" {
		real = filepath.Join(real, rest)
	}
	return filepath.Join(real, path.Base(rel)), nil
}

// Resolve is Abs that also follows a symlink at the end, which must point
// inside too. Use it to read.
func (s *Space) Resolve(rel string) (string, error) {
	p, err := s.Abs(rel)
	if err != nil {
		return "", err
	}
	fi, err := os.Lstat(p)
	if err != nil || fi.Mode()&fs.ModeSymlink == 0 {
		return p, nil
	}
	root, err := s.realRoot()
	if err != nil {
		return "", err
	}
	t, err := filepath.EvalSymlinks(p)
	if err != nil || !inside(root, t) {
		return "", ErrOutside
	}
	return t, nil
}

// Stat describes one path.
func (s *Space) Stat(rel string) (*Entry, error) {
	abs, err := s.Resolve(rel)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return nil, mapErr(err)
	}
	e := entryOf(fi, Clean(rel))
	if s.File != "" {
		e.Name = s.File
	}
	if Clean(rel) == "/" && s.File == "" {
		e.Name = s.Name
	}
	return e, nil
}

func entryOf(fi fs.FileInfo, p string) *Entry {
	e := &Entry{Name: fi.Name(), Path: p, IsDir: fi.IsDir(), Modified: fi.ModTime().UTC().Truncate(time.Second)}
	if !fi.IsDir() {
		e.Size = fi.Size()
		e.Mime = MimeOf(fi.Name())
	}
	return e
}

// List returns the entries of a folder, folders first, by name.
func (s *Space) List(rel string, hidden bool) ([]Entry, error) {
	if s.File != "" {
		e, err := s.Stat("/")
		if err != nil {
			return nil, err
		}
		return []Entry{*e}, nil
	}
	rel = Clean(rel)
	abs, err := s.Resolve(rel)
	if err != nil {
		return nil, err
	}
	des, err := os.ReadDir(abs)
	if err != nil {
		if errors.Is(err, syscall.ENOTDIR) {
			return nil, ErrNotDir
		}
		return nil, mapErr(err)
	}
	out := make([]Entry, 0, len(des))
	for _, de := range des {
		name := de.Name()
		if !hidden && (strings.HasPrefix(name, ".") || IsTemp(name)) {
			continue
		}
		var fi fs.FileInfo
		if de.Type()&fs.ModeSymlink != 0 {
			p, err := s.Resolve(Join(rel, name))
			if err != nil {
				continue // points outside
			}
			if fi, err = os.Stat(p); err != nil {
				continue
			}
			fi = renamed{fi, name}
		} else if fi, err = de.Info(); err != nil {
			continue
		}
		out = append(out, *entryOf(fi, Join(rel, name)))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

type renamed struct {
	fs.FileInfo
	name string
}

func (r renamed) Name() string { return r.name }

// Open opens a file for reading.
func (s *Space) Open(rel string) (*os.File, fs.FileInfo, error) {
	abs, err := s.Resolve(rel)
	if err != nil {
		return nil, nil, err
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, nil, mapErr(err)
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, fi, nil
}

// Mkdir creates a folder (and its parents).
func (s *Space) Mkdir(rel string) error {
	if !s.Writable() {
		return ErrForbidden
	}
	if Clean(rel) == "/" {
		return ErrExists
	}
	abs, err := s.Abs(rel)
	if err != nil {
		return err
	}
	if fi, err := os.Stat(abs); err == nil {
		if fi.IsDir() {
			return ErrExists
		}
		return ErrExists
	}
	return mapErr(os.MkdirAll(abs, 0o755))
}

// Conflict policy for writes.
const (
	Rename    = "rename"
	Overwrite = "overwrite"
	Skip      = "skip"
	Fail      = "fail"
)

// FreeName returns name, or "name (2).ext" … when it is taken in dir.
func (s *Space) FreeName(dir, name string) (string, error) {
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if ext == name { // ".bashrc"
		stem, ext = name, ""
	}
	cand := name
	for i := 2; i < 10000; i++ {
		abs, err := s.Abs(Join(dir, cand))
		if err != nil {
			return "", err
		}
		if _, err := os.Lstat(abs); os.IsNotExist(err) {
			return cand, nil
		}
		cand = fmt.Sprintf("%s (%d)%s", stem, i, ext)
	}
	return "", ErrExists
}

// Write stores r as dir/name (sub folders in name are created). The data
// lands in a temporary file next to it and replaces the target only when
// complete. It returns the entry written, or nil when skipped.
func (s *Space) Write(dir, name string, r io.Reader, conflict string, mtime time.Time) (*Entry, error) {
	if !s.Writable() {
		return nil, ErrForbidden
	}
	name = strings.Trim(strings.ReplaceAll(name, "\\", "/"), "/")
	parts := strings.Split(name, "/")
	for _, p := range parts {
		if err := ValidName(p); err != nil {
			return nil, err
		}
	}
	if s.File != "" {
		return nil, ErrForbidden
	}
	dir = Join(dir, strings.Join(parts[:len(parts)-1], "/"))
	base := parts[len(parts)-1]
	dabs, err := s.Resolve(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dabs, 0o755); err != nil {
		return nil, mapErr(err)
	}
	target, err := s.Abs(Join(dir, base))
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(target); err == nil {
		switch conflict {
		case Overwrite:
			if fi.IsDir() {
				return nil, ErrIsDir
			}
		case Skip:
			return nil, nil
		case Fail:
			return nil, ErrExists
		default:
			if base, err = s.FreeName(dir, base); err != nil {
				return nil, err
			}
			if target, err = s.Abs(Join(dir, base)); err != nil {
				return nil, err
			}
		}
	}
	tmp, err := os.CreateTemp(dabs, TempPrefix+"*")
	if err != nil {
		return nil, mapErr(err)
	}
	n, err := io.Copy(tmp, r)
	if err == nil {
		err = tmp.Close()
	} else {
		tmp.Close()
	}
	if err != nil {
		os.Remove(tmp.Name())
		return nil, err
	}
	_ = os.Chmod(tmp.Name(), 0o644)
	if !mtime.IsZero() {
		_ = os.Chtimes(tmp.Name(), mtime, mtime)
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		os.Remove(tmp.Name())
		return nil, mapErr(err)
	}
	fi, err := os.Stat(target)
	if err != nil {
		return nil, err
	}
	e := entryOf(fi, Join(dir, base))
	e.Size = n
	return e, nil
}

// TempPrefix marks files still being written.
const TempPrefix = ".pf-upload-"

// IsTemp reports whether a name is an unfinished upload.
func IsTemp(name string) bool { return strings.HasPrefix(name, TempPrefix) }

// Remove deletes a path for good.
func (s *Space) Remove(rel string) error {
	if !s.Writable() {
		return ErrForbidden
	}
	if Clean(rel) == "/" && s.File == "" {
		return ErrForbidden
	}
	abs, err := s.Abs(rel)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(abs); err != nil {
		return mapErr(err)
	}
	return mapErr(os.RemoveAll(abs))
}

// Move moves (or with copy=true copies) src/srel to dst/drel. Inside one
// file system it is a rename; across devices the data is copied.
func Move(src *Space, srel string, dst *Space, drel string, copyOnly bool, conflict string) (string, error) {
	if !dst.Writable() || (!copyOnly && !src.Writable()) {
		return "", ErrForbidden
	}
	srel, drel = Clean(srel), Clean(drel)
	if (srel == "/" && src.File == "") || drel == "/" {
		return "", ErrForbidden
	}
	from, err := src.Abs(srel)
	if err != nil {
		return "", err
	}
	sfi, err := os.Lstat(from)
	if err != nil {
		return "", mapErr(err)
	}
	ddir, dname := path.Dir(drel), path.Base(drel)
	if err := ValidName(dname); err != nil {
		return "", err
	}
	to, err := dst.Abs(drel)
	if err != nil {
		return "", err
	}
	if to == from {
		if copyOnly {
			if dname, err = dst.FreeName(ddir, dname); err != nil {
				return "", err
			}
			if to, err = dst.Abs(Join(ddir, dname)); err != nil {
				return "", err
			}
		} else {
			return drel, nil
		}
	}
	if sfi.IsDir() && (to == from || strings.HasPrefix(to, from+string(filepath.Separator))) {
		return "", fmt.Errorf("%w: a folder cannot go inside itself", ErrForbidden)
	}
	if _, err := os.Lstat(to); err == nil {
		switch conflict {
		case Overwrite:
			if err := os.RemoveAll(to); err != nil {
				return "", mapErr(err)
			}
		case Skip:
			return "", nil
		case Rename:
			if dname, err = dst.FreeName(ddir, dname); err != nil {
				return "", err
			}
			if to, err = dst.Abs(Join(ddir, dname)); err != nil {
				return "", err
			}
		default:
			return "", ErrExists
		}
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return "", mapErr(err)
	}
	if !copyOnly {
		err := os.Rename(from, to)
		if err == nil {
			return Join(ddir, dname), nil
		}
		if !errors.Is(err, syscall.EXDEV) {
			return "", mapErr(err)
		}
	}
	if err := copyTree(from, to); err != nil {
		os.RemoveAll(to)
		return "", err
	}
	if !copyOnly {
		if err := os.RemoveAll(from); err != nil {
			return "", mapErr(err)
		}
	}
	return Join(ddir, dname), nil
}

// MoveOut moves a path of a space to an absolute place outside (the trash).
func MoveOut(src *Space, srel, to string) error {
	from, err := src.Abs(srel)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(from); err != nil {
		return mapErr(err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	if err := os.Rename(from, to); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return mapErr(err)
	}
	if err := copyTree(from, to); err != nil {
		os.RemoveAll(to)
		return err
	}
	return os.RemoveAll(from)
}

// MoveIn moves an absolute path (from the trash) to rel in the space.
func MoveIn(from string, dst *Space, rel string) (string, error) {
	dir, name := path.Dir(Clean(rel)), path.Base(Clean(rel))
	dabs, err := dst.Abs(dir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dabs, 0o755); err != nil {
		return "", mapErr(err)
	}
	if name, err = dst.FreeName(dir, name); err != nil {
		return "", err
	}
	to, err := dst.Abs(Join(dir, name))
	if err != nil {
		return "", err
	}
	if err := os.Rename(from, to); err != nil {
		if !errors.Is(err, syscall.EXDEV) {
			return "", mapErr(err)
		}
		if err := copyTree(from, to); err != nil {
			os.RemoveAll(to)
			return "", err
		}
		os.RemoveAll(from)
	}
	return Join(dir, name), nil
}

// copyTree copies a file or folder; symlinks are skipped.
func copyTree(from, to string) error {
	fi, err := os.Lstat(from)
	if err != nil {
		return err
	}
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		return nil
	case fi.IsDir():
		if err := os.MkdirAll(to, 0o755); err != nil {
			return err
		}
		des, err := os.ReadDir(from)
		if err != nil {
			return err
		}
		for _, de := range des {
			if err := copyTree(filepath.Join(from, de.Name()), filepath.Join(to, de.Name())); err != nil {
				return err
			}
		}
		_ = os.Chtimes(to, fi.ModTime(), fi.ModTime())
		return nil
	}
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chtimes(to, fi.ModTime(), fi.ModTime())
}

// Walk visits every entry below rel (not following symlinks). fn returning
// filepath.SkipDir skips a folder; io.EOF stops the walk.
func (s *Space) Walk(rel string, hidden bool, fn func(e *Entry) error) error {
	abs, err := s.Resolve(rel)
	if err != nil {
		return err
	}
	rel = Clean(rel)
	err = filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == abs {
				return err
			}
			return nil
		}
		if p == abs {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if !hidden && (strings.HasPrefix(d.Name(), ".") || IsTemp(d.Name())) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		r, _ := filepath.Rel(abs, p)
		return fn(entryOf(fi, Join(rel, filepath.ToSlash(r))))
	})
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

// Usage adds up the bytes below rel.
func (s *Space) Usage(rel string) (files int, size int64) {
	_ = s.Walk(rel, true, func(e *Entry) error {
		if !e.IsDir {
			files++
			size += e.Size
		}
		return nil
	})
	return
}

func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrNotExist):
		return ErrNotFound
	case errors.Is(err, fs.ErrExist):
		return ErrExists
	case errors.Is(err, fs.ErrPermission), errors.Is(err, syscall.EROFS):
		return fmt.Errorf("%w: the server refused (%v)", ErrForbidden, err)
	case errors.Is(err, syscall.ENOTDIR):
		return ErrNotDir
	}
	return err
}

var extraTypes = map[string]string{
	".md": "text/markdown", ".markdown": "text/markdown", ".txt": "text/plain", ".log": "text/plain", ".csv": "text/csv",
	".json": "application/json", ".yaml": "text/yaml", ".yml": "text/yaml", ".toml": "text/plain", ".ini": "text/plain",
	".conf": "text/plain", ".env": "text/plain", ".sh": "text/x-shellscript", ".go": "text/x-go", ".py": "text/x-python",
	".js": "text/javascript", ".ts": "text/x-typescript", ".tsx": "text/x-typescript", ".jsx": "text/javascript",
	".css": "text/css", ".html": "text/html", ".htm": "text/html", ".xml": "text/xml", ".sql": "text/x-sql",
	".rs": "text/x-rust", ".c": "text/x-c", ".h": "text/x-c", ".cpp": "text/x-c++", ".java": "text/x-java",
	".nix": "text/plain", ".srt": "text/plain", ".vtt": "text/vtt", ".ass": "text/plain", ".nfo": "text/plain", ".cue": "text/plain",
	".mkv": "video/x-matroska", ".mp4": "video/mp4", ".m4v": "video/mp4", ".mov": "video/quicktime", ".webm": "video/webm",
	".avi": "video/x-msvideo", ".ts_": "video/mp2t", ".flac": "audio/flac", ".mp3": "audio/mpeg", ".m4a": "audio/mp4",
	".m4b": "audio/mp4", ".ogg": "audio/ogg", ".opus": "audio/ogg", ".wav": "audio/wav", ".aac": "audio/aac",
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".gif": "image/gif", ".webp": "image/webp",
	".svg": "image/svg+xml", ".heic": "image/heic", ".heif": "image/heif", ".avif": "image/avif", ".bmp": "image/bmp",
	".tif": "image/tiff", ".tiff": "image/tiff", ".ico": "image/x-icon",
	".pdf": "application/pdf", ".epub": "application/epub+zip", ".cbz": "application/vnd.comicbook+zip",
	".zip": "application/zip", ".7z": "application/x-7z-compressed", ".rar": "application/vnd.rar", ".gz": "application/gzip",
	".tar": "application/x-tar", ".iso": "application/x-iso9660-image",
	".doc": "application/msword", ".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xls": "application/vnd.ms-excel", ".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".ppt": "application/vnd.ms-powerpoint", ".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".odt": "application/vnd.oasis.opendocument.text", ".ods": "application/vnd.oasis.opendocument.spreadsheet",
	".odp": "application/vnd.oasis.opendocument.presentation",
}

// MimeOf guesses a type from the file name.
func MimeOf(name string) string {
	ext := strings.ToLower(path.Ext(name))
	if t, ok := extraTypes[ext]; ok {
		return t
	}
	if t := mime.TypeByExtension(ext); t != "" {
		if i := strings.Index(t, ";"); i > 0 {
			t = t[:i]
		}
		return t
	}
	return "application/octet-stream"
}
