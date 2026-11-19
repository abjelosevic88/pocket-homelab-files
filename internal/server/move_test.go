package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// withMoveCore puts a fake Pocket Core in front of the server that
// introspects Pocket Move's link tokens: pcx_<exp|imp>_<name> acts for
// <name>@example.com with move:export or move:import.
func withMoveCore(t *testing.T, s *Server) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/introspect" {
			_ = json.NewEncoder(w).Encode(map[string]any{})
			return
		}
		var in struct{ Token string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		parts := strings.Split(in.Token, "_")
		if len(parts) != 3 {
			_ = json.NewEncoder(w).Encode(map[string]any{"active": false})
			return
		}
		scope := map[string]string{"exp": core.ScopeMoveExport, "imp": core.ScopeMoveImport}[parts[1]]
		_ = json.NewEncoder(w).Encode(map[string]any{
			"active": true, "kind": "delegated", "scope": scope, "audience": "files", "iss": "https://core.test",
			"exp":  time.Now().Add(time.Hour).Unix(),
			"user": map[string]any{"sub": "sub-" + parts[2], "email": parts[2] + "@example.com", "name": parts[2]},
		})
	}))
	t.Cleanup(fake.Close)
	s.core = core.New(fake.URL, core.Options{DB: s.store.DB})
}

func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		f, err := zw.CreateHeader(&zip.FileHeader{Name: n, Method: zip.Deflate})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.Write([]byte(files[n]))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// importZip posts a zip as multipart field "file".
func (c *client) importZip(data []byte) (int, map[string]any) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "files.zip")
	_, _ = fw.Write(data)
	_ = mw.Close()
	w, out := c.do("POST", "/api/v1/import", body.Bytes(), "Content-Type", mw.FormDataContentType())
	return w.Code, out
}

func zipNames(t *testing.T, data []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		var b bytes.Buffer
		_, _ = b.ReadFrom(rc)
		rc.Close()
		out[f.Name] = b.String()
	}
	return out
}

// Pocket Move's link tokens reach exactly the move routes and nothing else.
func TestMoveLinkScopes(t *testing.T) {
	s, h := testServer(t)
	withMoveCore(t, s)
	newPerson(t, s, h, "alice@example.com", "admin")
	exp := &client{t: t, h: h, token: "pcx_exp_alice"}
	imp := &client{t: t, h: h, token: "pcx_imp_alice"}

	w, _ := exp.do("GET", core.PortablePath, nil)
	var desc core.Portable
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &desc) != nil || len(desc.Exports) != 1 || desc.Exports[0].Kind != core.KindPocket || len(desc.Imports) != 2 {
		t.Fatalf("portable with move:export: %d %s", w.Code, w.Body.String())
	}
	if w, _ := imp.do("GET", core.PortablePath, nil); w.Code != 200 {
		t.Errorf("portable with move:import: %d", w.Code)
	}
	if w, _ := exp.do("GET", "/api/v1/export.zip", nil); w.Code != 200 {
		t.Errorf("export with move:export: %d", w.Code)
	}
	for _, c := range []struct {
		c            *client
		method, path string
	}{
		{exp, "POST", "/api/v1/import"},
		{exp, "GET", "/api/v1/settings"},
		{exp, "GET", "/api/v1/fs/list?space=home&path=/"},
		{exp, "GET", "/api/v1/fs/zip?space=home&path=/"},
		{exp, "PROPFIND", "/dav/"},
		{imp, "GET", "/api/v1/export.zip"},
		{imp, "GET", "/api/v1/settings"},
		{imp, "PUT", "/api/v1/fs/upload?space=home&path=/&name=x.txt"},
	} {
		if w, _ := c.c.do(c.method, c.path, "x"); w.Code != http.StatusForbidden {
			t.Errorf("%s %s with %s: %d, want 403", c.method, c.path, c.c.token, w.Code)
		}
	}
	if code, out := imp.importZip(makeZip(t, map[string]string{"a.txt": "a"})); code != 200 || jsonNum(out["imported"]) != "1" {
		t.Errorf("import with move:import: %d %v", code, out)
	}
}

// The export is My files only: hidden files yes, a mount or a folder someone
// shares with the person no, another person's files never.
func TestMoveExportOwnFilesOnly(t *testing.T) {
	s, h := testServer(t)
	alice := newPerson(t, s, h, "alice@example.com", "admin")
	bob := newPerson(t, s, h, "bob@example.com", "member")
	upload(t, alice, "home", "/Docs", "a.txt", "alice doc")
	upload(t, alice, "home", "/.config", "x.conf", "dotfile")
	upload(t, bob, "home", "/Holiday", "b.jpg", "bob photo")
	if w, _ := bob.do("PUT", "/api/v1/shares/path", map[string]any{"path": "/Holiday", "members": []map[string]any{{"user_id": 1, "perm": "edit"}}}); w.Code != 200 {
		t.Fatalf("share: %d %s", w.Code, w.Body.String())
	}
	if w, _ := alice.do("POST", "/api/v1/mounts", map[string]any{"name": "Media", "path": filepath.Join(s.cfg.MountRoots[0], "media")}); w.Code != 200 {
		t.Fatalf("mount: %d", w.Code)
	}

	w, _ := alice.do("GET", "/api/v1/export.zip", nil)
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("export: %d", w.Code)
	}
	got := zipNames(t, w.Body.Bytes())
	if got["Docs/a.txt"] != "alice doc" || got[".config/x.conf"] != "dotfile" {
		t.Errorf("alice's export: %v", got)
	}
	for name, body := range got {
		if strings.Contains(body, "bob") || strings.Contains(body, "film") || strings.Contains(name, "Holiday") {
			t.Errorf("alice's export holds %s", name)
		}
	}
	w, _ = bob.do("GET", "/api/v1/export.zip", nil)
	for name := range zipNames(t, w.Body.Bytes()) {
		if strings.Contains(name, "Docs") || strings.Contains(name, ".config") {
			t.Errorf("bob's export holds alice's %s", name)
		}
	}
}

// A zip comes in under "Moved in <date>", stays inside, and the same zip
// twice adds nothing; a file changed meanwhile is kept and the zip's copy
// takes a free name, once.
func TestMoveImport(t *testing.T) {
	s, h := testServer(t)
	alice := newPerson(t, s, h, "alice@example.com", "admin")
	bob := newPerson(t, s, h, "bob@example.com", "member")
	data := makeZip(t, map[string]string{
		"Photos/a.jpg":      "jpeg bytes",
		"notes.txt":         "hello",
		"Empty/":            "",
		"../escape.txt":     "nope",
		"/abs.txt":          "rooted",
		"__MACOSX/._a.jpg":  "fork",
		"Photos/.DS_Store":  "junk",
		"dir\\win.txt":      "backslash",
		"Photos/../up.txt":  "dots",
		".pf-upload-123abc": "temp",
	})
	folder := "/Moved in " + time.Now().Format("2006-01-02")
	home := s.homeDir(1)

	code, out := alice.importZip(data)
	if code != 200 || jsonNum(out["imported"]) != "4" || out["folder"] != folder {
		t.Fatalf("first import: %d %v", code, out)
	}
	for rel, want := range map[string]string{"Photos/a.jpg": "jpeg bytes", "notes.txt": "hello", "abs.txt": "rooted", "dir/win.txt": "backslash"} {
		if b, err := os.ReadFile(filepath.Join(home, folder, rel)); err != nil || string(b) != want {
			t.Errorf("%s: %q %v", rel, b, err)
		}
	}
	if fi, err := os.Stat(filepath.Join(home, folder, "Empty")); err != nil || !fi.IsDir() {
		t.Errorf("empty folder not kept: %v", err)
	}
	for _, p := range []string{filepath.Join(home, "escape.txt"), filepath.Join(s.cfg.FilesDir, "escape.txt"), filepath.Join(home, folder, "up.txt"), filepath.Join(home, folder, "Photos", ".DS_Store")} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s should not be there", p)
		}
	}

	if code, out := alice.importZip(data); code != 200 || jsonNum(out["imported"]) != "0" || jsonNum(out["skipped"]) != "4" {
		t.Fatalf("second import: %d %v", code, out)
	}
	// a changed file keeps its place; the zip's copy comes in beside it once
	_ = os.WriteFile(filepath.Join(home, folder, "notes.txt"), []byte("edited"), 0o644)
	if _, out := alice.importZip(data); jsonNum(out["imported"]) != "1" {
		t.Fatalf("after an edit: %v", out)
	}
	if b, _ := os.ReadFile(filepath.Join(home, folder, "notes.txt")); string(b) != "edited" {
		t.Errorf("the edit was overwritten: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(home, folder, "notes (2).txt")); string(b) != "hello" {
		t.Errorf("zip copy beside it: %q", b)
	}
	if _, out := alice.importZip(data); jsonNum(out["imported"]) != "0" {
		t.Errorf("third import added again: %v", out)
	}

	// bob's import lands in bob's home only
	if code, _ := bob.importZip(data); code != 200 {
		t.Fatalf("bob import: %d", code)
	}
	if _, err := os.Stat(filepath.Join(s.homeDir(2), folder, "notes.txt")); err != nil {
		t.Errorf("bob's import: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(home, folder, "notes.txt")); string(b) != "edited" {
		t.Errorf("bob's import touched alice's files")
	}

	// not a zip, not multipart
	if code, _ := alice.importZip([]byte("plain text")); code != 400 {
		t.Errorf("not a zip: %d", code)
	}
	if w, _ := alice.do("POST", "/api/v1/import", "raw"); w.Code != 400 {
		t.Errorf("not multipart: %d", w.Code)
	}
}

// Files' own export goes back in on another box (here: Bob).
func TestMoveRoundTrip(t *testing.T) {
	s, h := testServer(t)
	alice := newPerson(t, s, h, "alice@example.com", "admin")
	bob := newPerson(t, s, h, "bob@example.com", "member")
	upload(t, alice, "home", "/Docs/Deep", "a.txt", "deep")
	w, _ := alice.do("GET", "/api/v1/export.zip", nil)
	if code, out := bob.importZip(w.Body.Bytes()); code != 200 || jsonNum(out["imported"]) != "1" {
		t.Fatalf("round trip: %d %v", code, out)
	}
	folder := "Moved in " + time.Now().Format("2006-01-02")
	if b, _ := os.ReadFile(filepath.Join(s.homeDir(2), folder, "Docs", "Deep", "a.txt")); string(b) != "deep" {
		t.Errorf("round trip content %q", b)
	}
}
