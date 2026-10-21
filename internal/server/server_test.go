package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abjelosevic88/pocket-homelab-files/internal/config"
	"github.com/abjelosevic88/pocket-homelab-files/internal/store"
)

func testServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	dir := t.TempDir()
	mounts := filepath.Join(dir, "server")
	_ = os.MkdirAll(filepath.Join(mounts, "media", "movies"), 0o755)
	_ = os.WriteFile(filepath.Join(mounts, "media", "movies", "film.mkv"), []byte("not really a film"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("outside"), 0o644)
	t.Setenv("DATA_DIR", dir)
	t.Setenv("AUTH_MODE", "local")
	t.Setenv("TZ", "Europe/Sarajevo")
	t.Setenv("MOUNT_ROOTS", mounts)
	t.Setenv("BASE_URL", "https://files.example")
	t.Setenv("PUBLIC_URL", "https://send.example")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(cfg.DBPath, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := New(cfg, st, log, nil)
	if err := s.Bootstrap(t.Context()); err != nil {
		t.Fatal(err)
	}
	return s, s.Router()
}

type client struct {
	t     *testing.T
	h     http.Handler
	token string
	basic bool
}

func (c *client) do(method, path string, body any, headers ...string) (*httptest.ResponseRecorder, map[string]any) {
	var rd io.Reader
	if body != nil {
		switch b := body.(type) {
		case string:
			rd = strings.NewReader(b)
		case []byte:
			rd = bytes.NewReader(b)
		default:
			j, _ := json.Marshal(b)
			rd = bytes.NewReader(j)
		}
	}
	req := httptest.NewRequest(method, path, rd)
	if _, ok := body.(map[string]any); ok {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		if c.basic {
			req.SetBasicAuth("x", c.token)
		} else {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	c.h.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

func (c *client) list(method, path string, body any) []map[string]any {
	w, _ := c.do(method, path, body)
	var out []map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out
}

// newPerson makes an account and returns a client with an API token.
func newPerson(t *testing.T, s *Server, h http.Handler, email, role string) *client {
	t.Helper()
	u := &store.User{Email: email, Name: strings.Split(email, "@")[0], Role: role}
	if err := s.store.CreateUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	tok, _, err := s.store.CreateAPIToken(context.Background(), u.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	return &client{t: t, h: h, token: tok}
}

func upload(t *testing.T, c *client, space, dir, name, body string) map[string]any {
	t.Helper()
	w, out := c.do("PUT", "/api/v1/fs/upload?"+url.Values{"space": {space}, "path": {dir}, "name": {name}}.Encode(), body)
	if w.Code != 200 {
		t.Fatalf("upload %s: %d %s", name, w.Code, w.Body.String())
	}
	return out
}

func TestHomeIsPrivate(t *testing.T) {
	s, h := testServer(t)
	alice := newPerson(t, s, h, "alice@example.com", "admin")
	bob := newPerson(t, s, h, "bob@example.com", "member")
	upload(t, alice, "home", "/Docs", "plan.txt", "alice's plan")

	w, out := alice.do("GET", "/api/v1/fs/list?space=home&path=/Docs", nil)
	if w.Code != 200 || len(out["entries"].([]any)) != 1 {
		t.Fatalf("alice list: %d %s", w.Code, w.Body.String())
	}
	// bob's home is his own: the same path is simply missing
	if w, _ := bob.do("GET", "/api/v1/fs/list?space=home&path=/Docs", nil); w.Code != 404 {
		t.Fatalf("bob sees alice's folder: %d", w.Code)
	}
	// alice is admin, yet bob's things stay closed to her too
	upload(t, bob, "home", "/", "diary.txt", "bob only")
	if w, _ := alice.do("GET", "/api/v1/fs/raw?space=home&path=/diary.txt", nil); w.Code != 404 {
		t.Fatalf("admin reads a member's file: %d", w.Code)
	}
	// someone else's share id answers 404
	if w, _ := bob.do("GET", "/api/v1/fs/list?space=share:1&path=/", nil); w.Code != 404 {
		t.Fatalf("unknown share: %d", w.Code)
	}
	// someone else's send answers 404
	w, created := alice.do("POST", "/api/v1/sends", map[string]any{"kind": "live", "live": map[string]any{"space": "home", "paths": []string{"/Docs/plan.txt"}}, "expires": "1d"})
	if w.Code != 201 {
		t.Fatalf("create live send: %d %s", w.Code, w.Body.String())
	}
	if w, _ := bob.do("GET", "/api/v1/sends/"+created["id"].(string), nil); w.Code != 404 {
		t.Fatalf("bob reads alice's send: %d", w.Code)
	}
	if w, _ := bob.do("DELETE", "/api/v1/sends/"+created["id"].(string), nil); w.Code != 404 {
		t.Fatalf("bob deletes alice's send: %d", w.Code)
	}
}

func TestPathsStayInside(t *testing.T) {
	s, h := testServer(t)
	alice := newPerson(t, s, h, "alice@example.com", "admin")
	for _, p := range []string{"/../../secret.txt", "/../1/../../secret.txt", "..%2F..%2Fsecret.txt"} {
		w, _ := alice.do("GET", "/api/v1/fs/raw?space=home&path="+url.QueryEscape(p), nil)
		if w.Code == 200 && strings.Contains(w.Body.String(), "outside") {
			t.Fatalf("%s escaped the home folder", p)
		}
	}
	// a symlink planted in the home that points outside is refused
	home := s.homeDir(1)
	_ = os.Symlink(filepath.Join(s.cfg.DataDir, "secret.txt"), filepath.Join(home, "link.txt"))
	if w, _ := alice.do("GET", "/api/v1/fs/raw?space=home&path=/link.txt", nil); w.Code == 200 {
		t.Fatalf("symlink out of the home was followed")
	}
	_ = os.Symlink(s.cfg.DataDir, filepath.Join(home, "dirlink"))
	if w, _ := alice.do("GET", "/api/v1/fs/raw?space=home&path=/dirlink/secret.txt", nil); w.Code == 200 {
		t.Fatalf("symlinked folder out of the home was followed")
	}
	w, _ := alice.do("PUT", "/api/v1/fs/upload?space=home&path=/dirlink&name=x.txt", "x")
	if w.Code == 200 {
		t.Fatalf("write through a symlink out of the home")
	}
}

func TestMountsAndShares(t *testing.T) {
	s, h := testServer(t)
	alice := newPerson(t, s, h, "alice@example.com", "admin")
	bob := newPerson(t, s, h, "bob@example.com", "member")
	carol := newPerson(t, s, h, "carol@example.com", "member")
	root := s.cfg.MountRoots[0]

	if w, _ := bob.do("POST", "/api/v1/mounts", map[string]any{"name": "Media", "path": filepath.Join(root, "media")}); w.Code != 403 {
		t.Fatalf("member made a mount: %d", w.Code)
	}
	if w, _ := alice.do("POST", "/api/v1/mounts", map[string]any{"name": "Etc", "path": "/etc"}); w.Code != 400 {
		t.Fatalf("mount outside MOUNT_ROOTS: %d", w.Code)
	}
	w, m := alice.do("POST", "/api/v1/mounts", map[string]any{"name": "Media", "path": filepath.Join(root, "media"), "access": []map[string]any{{"user_id": 2, "perm": "view"}}})
	if w.Code != 200 {
		t.Fatalf("mount: %d %s", w.Code, w.Body.String())
	}
	sp := "mount:" + jsonNum(m["id"])
	w, out := bob.do("GET", "/api/v1/fs/list?space="+sp+"&path=/movies", nil)
	if w.Code != 200 || len(out["entries"].([]any)) != 1 {
		t.Fatalf("bob lists mount: %d %s", w.Code, w.Body.String())
	}
	if w, _ := bob.do("PUT", "/api/v1/fs/upload?space="+sp+"&path=/&name=new.txt", "x"); w.Code != 403 {
		t.Fatalf("view-only mount took a file: %d", w.Code)
	}
	if w, _ := carol.do("GET", "/api/v1/fs/list?space="+sp+"&path=/", nil); w.Code != 404 {
		t.Fatalf("carol sees a mount she has no access to: %d", w.Code)
	}

	// bob shares a folder with carol (edit)
	upload(t, bob, "home", "/Holiday", "a.jpg", "jpeg")
	people := bob.list("GET", "/api/v1/people", nil)
	if len(people) != 3 {
		t.Fatalf("people: %v", people)
	}
	w, sh := bob.do("PUT", "/api/v1/shares/path", map[string]any{"path": "/Holiday", "members": []map[string]any{{"user_id": 3, "perm": "edit"}}})
	if w.Code != 200 {
		t.Fatalf("share: %d %s", w.Code, w.Body.String())
	}
	ssp := "share:" + jsonNum(sh["id"])
	upload(t, carol, ssp, "/", "b.jpg", "from carol")
	if _, err := os.Stat(filepath.Join(s.homeDir(2), "Holiday", "b.jpg")); err != nil {
		t.Fatalf("carol's upload did not land in bob's folder: %v", err)
	}
	if w, _ := alice.do("GET", "/api/v1/fs/list?space="+ssp+"&path=/", nil); w.Code != 404 {
		t.Fatalf("admin opened a share not meant for her: %d", w.Code)
	}
	// carol deletes: it goes to bob's trash
	w, del := carol.do("POST", "/api/v1/fs/delete", map[string]any{"space": ssp, "paths": []string{"/b.jpg"}})
	if w.Code != 200 || del["trashed"].(float64) != 1 {
		t.Fatalf("delete in share: %d %s", w.Code, w.Body.String())
	}
	if tr := bob.list("GET", "/api/v1/trash", nil); len(tr) != 1 || tr[0]["orig_path"] != "/Holiday/b.jpg" {
		t.Fatalf("bob's trash: %v", tr)
	}
	// renaming the folder keeps the share
	if w, _ := bob.do("POST", "/api/v1/fs/rename", map[string]any{"space": "home", "path": "/Holiday", "name": "Holiday 2026"}); w.Code != 200 {
		t.Fatalf("rename: %d", w.Code)
	}
	if w, out := carol.do("GET", "/api/v1/fs/list?space="+ssp+"&path=/", nil); w.Code != 200 || out["space"].(map[string]any)["name"] != "Holiday 2026" {
		t.Fatalf("share lost after rename: %d %s", w.Code, w.Body.String())
	}
}

func TestEncryptedSendFlow(t *testing.T) {
	s, h := testServer(t)
	alice := newPerson(t, s, h, "alice@example.com", "admin")
	auth := "client-derived-auth-token"
	sum := sha256.Sum256([]byte(auth))
	w, sd := alice.do("POST", "/api/v1/sends", map[string]any{"kind": "files", "encrypted": true, "meta": "Y2lwaGVy", "auth_hash": hex.EncodeToString(sum[:]),
		"files": []map[string]any{{"expected": 10}}, "expires": "1h", "max_downloads": 1})
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	id := sd["id"].(string)
	if !strings.HasPrefix(sd["url"].(string), "https://send.example/s/") {
		t.Fatalf("link not on PUBLIC_URL: %v", sd["url"])
	}
	anon := &client{t: t, h: h}
	if w, out := anon.do("GET", "/api/v1/public/sends/"+id, nil); w.Code != 200 || out["available"] != false {
		t.Fatalf("unfinished send is available: %s", w.Body.String())
	}
	if w, _ := alice.do("PUT", "/api/v1/sends/"+id+"/files/0?offset=0", "01234"); w.Code != 200 {
		t.Fatalf("chunk 1: %d", w.Code)
	}
	if w, _ := alice.do("PUT", "/api/v1/sends/"+id+"/files/0?offset=9", "x"); w.Code != 409 {
		t.Fatalf("wrong offset accepted: %d", w.Code)
	}
	if w, _ := alice.do("PUT", "/api/v1/sends/"+id+"/files/0?offset=5", "56789toolong"); w.Code != 413 {
		t.Fatalf("overlong chunk accepted: %d", w.Code)
	}
	alice.do("PUT", "/api/v1/sends/"+id+"/files/0?offset=5", "56789")
	if w, _ := alice.do("POST", "/api/v1/sends/"+id+"/finish", nil); w.Code != 200 {
		t.Fatalf("finish: %d %s", w.Code, w.Body.String())
	}
	if w, _ := anon.do("POST", "/api/v1/public/sends/"+id+"/open", map[string]any{"auth": "wrong"}); w.Code != 401 {
		t.Fatalf("wrong auth opened: %d", w.Code)
	}
	w, open := anon.do("POST", "/api/v1/public/sends/"+id+"/open", map[string]any{"auth": auth})
	if w.Code != 200 {
		t.Fatalf("open: %d %s", w.Code, w.Body.String())
	}
	ticket := open["ticket"].(string)
	w, _ = anon.do("GET", "/api/v1/public/sends/"+id+"/files/0?t="+ticket, nil)
	if w.Code != 200 || w.Body.String() != "0123456789" {
		t.Fatalf("download: %d %q", w.Code, w.Body.String())
	}
	if w, _ := anon.do("GET", "/api/v1/public/sends/"+id+"/files/0?t=bogus", nil); w.Code != 403 {
		t.Fatalf("bogus ticket: %d", w.Code)
	}
	// one download allowed: the second open is refused
	if w, _ := anon.do("POST", "/api/v1/public/sends/"+id+"/open", map[string]any{"auth": auth}); w.Code != 410 {
		t.Fatalf("limit not enforced: %d", w.Code)
	}
	// the ticket in hand still finishes the download
	if w, _ := anon.do("GET", "/api/v1/public/sends/"+id+"/files/0?t="+ticket, nil); w.Code != 200 {
		t.Fatalf("ticket died with the limit: %d", w.Code)
	}
}

func TestLivePasswordSendAndPublicRouter(t *testing.T) {
	s, h := testServer(t)
	alice := newPerson(t, s, h, "alice@example.com", "admin")
	upload(t, alice, "home", "/Photos", "one.jpg", "1")
	upload(t, alice, "home", "/Photos", "two.jpg", "22")
	upload(t, alice, "home", "/", "private.txt", "no")
	w, sd := alice.do("POST", "/api/v1/sends", map[string]any{"kind": "live", "live": map[string]any{"space": "home", "paths": []string{"/Photos"}}, "password": "hunter22", "expires": "7d"})
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	id := sd["id"].(string)
	pub := &client{t: t, h: s.PublicRouter()}
	if w, out := pub.do("GET", "/api/v1/public/sends/"+id, nil); w.Code != 200 || out["entries"] != nil {
		t.Fatalf("password send leaked its listing: %s", w.Body.String())
	}
	if w, _ := pub.do("POST", "/api/v1/public/sends/"+id+"/open", map[string]any{"password": "nope"}); w.Code != 401 {
		t.Fatalf("wrong password: %d", w.Code)
	}
	w, open := pub.do("POST", "/api/v1/public/sends/"+id+"/open", map[string]any{"password": "hunter22"})
	if w.Code != 200 {
		t.Fatalf("open: %d %s", w.Code, w.Body.String())
	}
	tk := url.QueryEscape(open["ticket"].(string))
	if w, _ := pub.do("GET", "/api/v1/public/sends/"+id+"/live/file?t="+tk+"&path=/Photos/two.jpg", nil); w.Code != 200 || w.Body.String() != "22" {
		t.Fatalf("live file: %d", w.Code)
	}
	if w, _ := pub.do("GET", "/api/v1/public/sends/"+id+"/live/file?t="+tk+"&path=/private.txt", nil); w.Code != 404 {
		t.Fatalf("live link reached outside its folder: %d", w.Code)
	}
	if w, _ := pub.do("GET", "/api/v1/public/sends/"+id+"/live/file?t="+tk+"&path=/Photos/../private.txt", nil); w.Code != 404 {
		t.Fatalf("live link climbed out: %d", w.Code)
	}
	if w, _ := pub.do("GET", "/api/v1/public/sends/"+id+"/live/zip?t="+tk, nil); w.Code != 200 || !strings.HasPrefix(w.Body.String(), "PK") {
		t.Fatalf("zip: %d", w.Code)
	}
	// the public router has nothing else
	pub.token = alice.token
	for _, p := range []string{"/api/v1/fs/list?space=home", "/api/v1/sends", "/dav/", "/api/v1/auth/status", "/settings"} {
		if w, _ := pub.do("GET", p, nil); w.Code != 404 {
			t.Fatalf("public router serves %s: %d", p, w.Code)
		}
	}
}

func TestRequestUpload(t *testing.T) {
	s, h := testServer(t)
	alice := newPerson(t, s, h, "alice@example.com", "admin")
	alice.do("POST", "/api/v1/fs/mkdir", map[string]any{"space": "home", "path": "/Inbox"})
	w, rq := alice.do("POST", "/api/v1/requests", map[string]any{"title": "Wedding photos", "space": "home", "path": "/Inbox", "expires": "1d", "max_files": 2})
	if w.Code != 200 {
		t.Fatalf("request: %d %s", w.Code, w.Body.String())
	}
	id := rq["id"].(string)
	anon := &client{t: t, h: s.PublicRouter()}
	_, info := anon.do("GET", "/api/v1/public/requests/"+id, nil)
	tok := info["token"].(string)
	for i, name := range []string{"x.jpg", "x.jpg", "third.jpg"} {
		w, up := anon.do("POST", "/api/v1/public/requests/"+id+"/uploads", map[string]any{"token": tok, "name": "../../" + name, "size": 3})
		if i == 2 {
			if w.Code != 410 {
				t.Fatalf("request took more than max_files: %d", w.Code)
			}
			break
		}
		if w.Code != 200 {
			t.Fatalf("start: %d %s", w.Code, w.Body.String())
		}
		uid := up["id"].(string)
		anon.do("PUT", "/api/v1/public/requests/"+id+"/uploads/"+uid+"?offset=0&token="+url.QueryEscape(tok), "abc")
		if w, _ := anon.do("POST", "/api/v1/public/requests/"+id+"/uploads/"+uid+"/finish", map[string]any{"token": tok}); w.Code != 200 {
			t.Fatalf("finish: %d %s", w.Code, w.Body.String())
		}
	}
	w, out := alice.do("GET", "/api/v1/fs/list?space=home&path=/Inbox", nil)
	names := []string{}
	for _, e := range out["entries"].([]any) {
		names = append(names, e.(map[string]any)["name"].(string))
	}
	if w.Code != 200 || strings.Join(names, ",") != "x (2).jpg,x.jpg" {
		t.Fatalf("received files: %v", names)
	}
}

func TestWebDAVAndLinkScopes(t *testing.T) {
	s, h := testServer(t)
	alice := newPerson(t, s, h, "alice@example.com", "admin")
	alice.basic = true
	if w, _ := alice.do("PUT", "/dav/Notes/todo.txt", "milk"); w.Code != 409 && w.Code != 201 {
		t.Fatalf("put into missing folder: %d", w.Code)
	}
	if w, _ := alice.do("MKCOL", "/dav/Notes", nil); w.Code != 201 {
		t.Fatalf("mkcol: %d", w.Code)
	}
	if w, _ := alice.do("PUT", "/dav/Notes/todo.txt", "milk"); w.Code != 201 {
		t.Fatalf("put: %d", w.Code)
	}
	w, _ := alice.do("PROPFIND", "/remote.php/dav/files/alice/Notes/", nil, "Depth", "1")
	if w.Code != 207 || !strings.Contains(w.Body.String(), "todo.txt") {
		t.Fatalf("propfind: %d %s", w.Code, w.Body.String())
	}
	if w, _ := alice.do("GET", "/dav/Notes/todo.txt", nil); w.Body.String() != "milk" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("get: %q csp %q", w.Body.String(), w.Header().Get("Content-Security-Policy"))
	}
	if w, _ := alice.do("MKCOL", "/dav/Mounts", nil); w.Code == 201 {
		t.Fatalf("reserved name created")
	}
	if w, _ := alice.do("DELETE", "/dav/Notes/todo.txt", nil); w.Code != 204 {
		t.Fatalf("delete: %d", w.Code)
	}
	if tr := alice.list("GET", "/api/v1/trash", nil); len(tr) != 1 || tr[0]["orig_path"] != "/Notes/todo.txt" {
		t.Fatalf("WebDAV delete did not go to the trash: %v", tr)
	}
	// no session-cookie WebDAV
	anon := &client{t: t, h: h}
	if w, _ := anon.do("PROPFIND", "/dav/", nil, "Depth", "0"); w.Code != 401 {
		t.Fatalf("anonymous dav: %d", w.Code)
	}
	// link scopes
	r := httptest.NewRequest("PUT", "/dav/x.docx", nil)
	if linkAllows([]string{"files:read"}, r) {
		t.Fatal("files:read allowed a PUT")
	}
	if !linkAllows([]string{"files:write"}, r) {
		t.Fatal("files:write refused a PUT")
	}
	if !linkAllows([]string{"files:read"}, httptest.NewRequest("PROPFIND", "/remote.php/dav/files/a/Docs", nil)) {
		t.Fatal("files:read refused PROPFIND")
	}
	if linkAllows([]string{"files:read", "files:write"}, httptest.NewRequest("POST", "/api/v1/sends", nil)) {
		t.Fatal("a link may create sends")
	}
	if linkAllows([]string{"files:read", "files:write"}, httptest.NewRequest("GET", "/api/v1/shares", nil)) {
		t.Fatal("a link may read shares")
	}
}

func jsonNum(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
