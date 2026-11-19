package server

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-files/internal/vfs"
)

// Pocket Move (docs/move.md in pocket-homelab-core): a person's own files go
// out as one zip and come in from one. Only "My files" — never a mount, a
// connected drive's own place, or what others share with the person.

// Limits for a zip coming in: the upload, what it unpacks to, and how many
// files it holds.
const (
	moveImportMaxBytes   int64  = 32 << 30
	moveImportMaxUnpack  uint64 = 64 << 30
	moveImportMaxEntries        = 200000
	moveImportSpare      uint64 = 1 << 30 // left free on the disk after unpacking
)

var portable = &core.Portable{
	Exports: []core.PortableExport{
		{Path: "/api/v1/export.zip", File: "files.zip", Kind: core.KindPocket, Title: "Everything in My files, folders kept"},
	},
	Imports: []core.PortableImport{
		{Path: "/api/v1/import", Kind: core.KindPocket, Form: "file", MaxBytes: moveImportMaxBytes, Title: "Unpacks the files into a new folder “Moved in …” in My files; files already there are left alone"},
		{Path: "/api/v1/import", Kind: core.KindZip, Form: "file", MaxBytes: moveImportMaxBytes, Title: "Unpacks any zip into a new folder “Moved in …” in My files"},
	},
}

// handleExportZip streams the person's whole home folder as a zip: hidden
// files too (they are the person's), unfinished uploads not.
func (s *Server) handleExportZip(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	s.seedUser(ctx, uid)
	sp := s.homeSpace(uid)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", contentDisposition("attachment", "pocket-files-"+time.Now().Format("2006-01-02")+".zip"))
	zw := zip.NewWriter(w)
	err := sp.Walk("/", true, func(e *vfs.Entry) error {
		if ctx.Err() != nil {
			return io.EOF
		}
		if vfs.IsTemp(e.Name) {
			return nil
		}
		h := &zip.FileHeader{Name: strings.TrimPrefix(e.Path, "/"), Method: zip.Store, Modified: e.Modified}
		if e.IsDir {
			h.Name += "/"
			_, err := zw.CreateHeader(h)
			return err
		}
		f, _, err := sp.Open(e.Path)
		if err != nil {
			return nil // gone meanwhile
		}
		defer f.Close()
		out, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, f)
		return err
	})
	if err != nil {
		s.log.Warn("export: stopped", "user", uid, "err", err)
		return // a broken zip tells the client something went wrong
	}
	_ = zw.Close()
}

// handleImportZip unpacks a zip (multipart field "file") into a folder
// "Moved in YYYY-MM-DD" in My files. It only adds: a file already there with
// the same content is skipped, one with other content keeps its place and the
// new one gets a free name. The same zip again goes into the folder it went
// into the first time, so a second run adds nothing.
func (s *Server) handleImportZip(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	s.seedUser(ctx, uid)
	sp := s.homeSpace(uid)
	r.Body = http.MaxBytesReader(w, r.Body, moveImportMaxBytes)
	tmp, sum, err := s.spoolPart(r, "file")
	if tmp != nil {
		defer os.Remove(tmp.Name())
		defer tmp.Close()
	}
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		writeErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("the zip is bigger than %d GB", moveImportMaxBytes>>30))
		return
	case err != nil:
		writeErr(w, http.StatusBadRequest, "send the zip as multipart form data (field \"file\")")
		return
	}
	fi, err := tmp.Stat()
	if err != nil {
		s.fail(w, err, "import")
		return
	}
	zr, err := zip.NewReader(tmp, fi.Size())
	if err != nil {
		writeErr(w, http.StatusBadRequest, "that is not a readable zip")
		return
	}
	if len(zr.File) > moveImportMaxEntries {
		writeErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("the zip holds more than %d files", moveImportMaxEntries))
		return
	}
	var total uint64
	for _, f := range zr.File {
		total += f.UncompressedSize64
	}
	if total > moveImportMaxUnpack {
		writeErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("the zip unpacks to more than %d GB", moveImportMaxUnpack>>30))
		return
	}
	if free, _ := diskFree(s.homeDir(uid)); free > 0 && total+moveImportSpare > free {
		writeErr(w, http.StatusInsufficientStorage, "there is not enough room on the server's disk for this zip")
		return
	}

	key := "move_import:" + sum
	folder := s.store.UserSetting(ctx, uid, key)
	if e, err := sp.Stat(folder); folder == "" || err != nil || !e.IsDir {
		folder = "/Moved in " + time.Now().Format("2006-01-02")
	}
	if err := sp.Mkdir(folder); err != nil && !errors.Is(err, vfs.ErrExists) {
		s.fail(w, err, "import")
		return
	}
	_ = s.store.SetUserSetting(ctx, uid, key, folder)

	imported, skipped := 0, 0
	problems := []string{}
	for _, f := range zr.File {
		if ctx.Err() != nil {
			break
		}
		name, ok := zipEntryName(f.Name)
		if !ok {
			if f.Name != "" && !strings.HasPrefix(f.Name, "__MACOSX/") && path.Base(f.Name) != ".DS_Store" {
				problems = append(problems, f.Name+": skipped (not a usable name)")
			}
			continue
		}
		if f.FileInfo().IsDir() {
			if err := sp.Mkdir(vfs.Join(folder, name)); err != nil && !errors.Is(err, vfs.ErrExists) {
				problems = append(problems, f.Name+": "+err.Error())
			}
			continue
		}
		if !f.Mode().IsRegular() { // symlinks and the like stay out
			problems = append(problems, f.Name+": skipped (not a plain file)")
			continue
		}
		added, err := importZipFile(sp, vfs.Join(folder, path.Dir(name)), path.Base(name), f)
		switch {
		case err != nil:
			problems = append(problems, f.Name+": "+err.Error())
		case added:
			imported++
		default:
			skipped++
		}
	}
	s.touch()
	s.indexTouch(sp, folder)
	if imported > 0 {
		s.publish(ctx, "files.uploaded", "file:home:"+folder, uid, map[string]any{"space": "home", "path": folder, "name": path.Base(folder), "files": imported})
	}
	if len(problems) > 50 {
		problems = append(problems[:50], fmt.Sprintf("… and %d more", len(problems)-50))
	}
	writeJSON(w, http.StatusOK, map[string]any{"imported": imported, "skipped": skipped, "folder": folder, "problems": problems})
}

// spoolPart copies the multipart field to a temporary file on the data disk
// (not in memory, not in /tmp) and returns it with its SHA-256.
func (s *Server) spoolPart(r *http.Request, field string) (*os.File, string, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, "", err
	}
	for {
		part, err := mr.NextPart()
		if err != nil {
			return nil, "", err // io.EOF: no such field
		}
		if part.FormName() != field {
			_ = part.Close()
			continue
		}
		tmp, err := os.CreateTemp(filepath.Join(s.cfg.DataDir, "uploads"), "move-*.zip")
		if err != nil {
			return nil, "", err
		}
		h := sha256.New()
		_, err = io.Copy(io.MultiWriter(tmp, h), part)
		_ = part.Close()
		return tmp, hex.EncodeToString(h.Sum(nil)), err
	}
}

// zipEntryName turns a name in a zip into a safe relative path: forward
// slashes, no leading slash, every part a usable name (no "..", no ".").
// Mac resource forks and unfinished uploads are left out.
func zipEntryName(n string) (string, bool) {
	n = strings.Trim(strings.ReplaceAll(n, "\\", "/"), "/")
	if n == "" || strings.HasPrefix(n, "__MACOSX/") || path.Base(n) == ".DS_Store" {
		return "", false
	}
	parts := strings.Split(n, "/")
	for _, p := range parts {
		if vfs.ValidName(p) != nil || vfs.IsTemp(p) {
			return "", false
		}
	}
	return strings.Join(parts, "/"), true
}

// importZipFile writes one zip entry as dir/name unless a file with the same
// content is already there (as name, or as "name (2)" … from an earlier run);
// a different file of that name keeps its place and this one takes a free name.
func importZipFile(sp *vfs.Space, dir, name string, f *zip.File) (bool, error) {
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if ext == name {
		stem, ext = name, ""
	}
	for i := 1; i < 10000; i++ {
		cand := name
		if i > 1 {
			cand = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		abs, err := sp.Abs(vfs.Join(dir, cand))
		if err != nil {
			return false, err
		}
		fi, err := os.Lstat(abs)
		if errors.Is(err, fs.ErrNotExist) {
			rc, err := f.Open()
			if err != nil {
				return false, err
			}
			defer rc.Close()
			mtime := f.Modified
			if mtime.Year() < 1981 {
				mtime = time.Time{}
			}
			_, err = sp.Write(dir, cand, rc, vfs.Fail, mtime)
			return err == nil, err
		}
		if err != nil {
			return false, err
		}
		if fi.Mode().IsRegular() && uint64(fi.Size()) == f.UncompressedSize64 && sameContent(abs, f) {
			return false, nil
		}
	}
	return false, vfs.ErrExists
}

// sameContent compares a file on disk with a zip entry: the CRC first (cheap),
// then byte by byte.
func sameContent(abs string, f *zip.File) bool {
	file, err := os.Open(abs)
	if err != nil {
		return false
	}
	defer file.Close()
	c := crc32.NewIEEE()
	if _, err := io.Copy(c, file); err != nil || c.Sum32() != f.CRC32 {
		return false
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return false
	}
	rc, err := f.Open()
	if err != nil {
		return false
	}
	defer rc.Close()
	return sameStream(file, rc)
}

func sameStream(a, b io.Reader) bool {
	ba, bb := make([]byte, 64<<10), make([]byte, 64<<10)
	for {
		na, ea := io.ReadFull(a, ba)
		nb, eb := io.ReadFull(b, bb)
		if na != nb || !bytes.Equal(ba[:na], bb[:nb]) {
			return false
		}
		aEnd := ea == io.EOF || ea == io.ErrUnexpectedEOF
		bEnd := eb == io.EOF || eb == io.ErrUnexpectedEOF
		if aEnd || bEnd {
			return aEnd && bEnd
		}
		if ea != nil || eb != nil {
			return false
		}
	}
}
