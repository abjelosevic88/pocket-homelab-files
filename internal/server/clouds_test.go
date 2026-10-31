package server

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func waitCloud(t *testing.T, c *client, id float64, want string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		for _, d := range c.list("GET", "/api/v1/clouds", nil) {
			if d["id"] == id && d["status"] == want && d["progress"] == nil {
				return d
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("drive %v never became %s: %v", id, want, c.list("GET", "/api/v1/clouds", nil))
	return nil
}

func remoteFixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("rclone"); err != nil {
		t.Skip("rclone not installed")
	}
	src := t.TempDir()
	_ = os.MkdirAll(filepath.Join(src, "Photos"), 0o755)
	_ = os.WriteFile(filepath.Join(src, "Photos", "a.jpg"), []byte("jpeg"), 0o644)
	_ = os.WriteFile(filepath.Join(src, "notes.txt"), []byte("notes"), 0o644)
	return src
}

func TestScheduledDriveIsPrivateAndReadOnly(t *testing.T) {
	s, h := testServer(t)
	a := newPerson(t, s, h, "a@example.com", "admin")
	b := newPerson(t, s, h, "b@example.com", "member")
	src := remoteFixture(t)

	res, d := a.do("POST", "/api/v1/clouds", map[string]any{"kind": "rclone", "name": "Archive", "interval_hours": 6,
		"fields": map[string]string{"config": fmt.Sprintf("[x]\ntype = alias\nremote = %s\n", src)}})
	if res.Code != http.StatusCreated {
		t.Fatalf("create: %d %v", res.Code, d)
	}
	id := d["id"].(float64)
	got := waitCloud(t, a, id, "idle")
	if got["files"].(float64) != 2 || got["space"] != fmt.Sprintf("cloud:%d", int(id)) {
		t.Fatalf("after sync: %v", got)
	}

	// Browsable as its own space, read only.
	if res, l := a.do("GET", fmt.Sprintf("/api/v1/fs/list?space=cloud:%d&path=/Photos", int(id)), nil); res.Code != 200 || l["writable"] != false {
		t.Fatalf("list: %d %v", res.Code, l)
	}
	if res, _ := a.do("POST", "/api/v1/fs/mkdir", map[string]any{"space": fmt.Sprintf("cloud:%d", int(id)), "path": "/new"}); res.Code != http.StatusForbidden {
		t.Fatalf("mkdir in a drive copy: %d", res.Code)
	}
	// Nobody else sees it.
	if res, _ := b.do("GET", fmt.Sprintf("/api/v1/fs/list?space=cloud:%d&path=/", int(id)), nil); res.Code != http.StatusNotFound {
		t.Fatalf("other person listed the drive: %d", res.Code)
	}
	if len(b.list("GET", "/api/v1/clouds", nil)) != 0 {
		t.Fatal("other person sees the drive")
	}
	if res, _ := b.do("POST", fmt.Sprintf("/api/v1/clouds/%d/sync", int(id)), nil); res.Code != http.StatusNotFound {
		t.Fatalf("other person synced the drive: %d", res.Code)
	}
	// The sign-in is sealed, not stored in clear.
	var cfg string
	_ = s.store.DB.QueryRow(`SELECT config FROM clouds WHERE id = ?`, int64(id)).Scan(&cfg)
	if len(cfg) < 10 || cfg[:3] != "v1:" {
		t.Fatalf("config not sealed: %q", cfg)
	}

	// Disconnect, keeping the copy: it moves into My files.
	res, out := a.do("DELETE", fmt.Sprintf("/api/v1/clouds/%d?keep=1", int(id)), nil)
	if res.Code != 200 || out["folder"] != "/Archive" {
		t.Fatalf("delete keep: %d %v", res.Code, out)
	}
	if res, _ := a.do("GET", "/api/v1/fs/stat?space=home&path=/Archive/notes.txt", nil); res.Code != 200 {
		t.Fatalf("kept copy missing: %d", res.Code)
	}
}

func TestOneTimeCopyLandsInMyFiles(t *testing.T) {
	s, h := testServer(t)
	a := newPerson(t, s, h, "a@example.com", "admin")
	src := remoteFixture(t)
	upload(t, a, "home", "/", "Box", "") // "Box" is taken by a file: the copy picks "Box (2)"
	res, d := a.do("POST", "/api/v1/clouds", map[string]any{"kind": "rclone", "name": "Box", "interval_hours": 0,
		"fields": map[string]string{"config": fmt.Sprintf("[x]\ntype = alias\nremote = %s\n", src)}})
	if res.Code != http.StatusCreated {
		t.Fatalf("create: %d %v", res.Code, d)
	}
	got := waitCloud(t, a, d["id"].(float64), "done")
	if got["folder"] != "/Box (2)" || got["space"] != "home" {
		t.Fatalf("one-time: %v", got)
	}
	if res, _ := a.do("GET", "/api/v1/fs/stat?space=home&path=/Box%20(2)/Photos/a.jpg", nil); res.Code != 200 {
		t.Fatalf("copied file missing: %d", res.Code)
	}
	for _, sp := range a.list("GET", "/api/v1/spaces", nil) {
		if sp["kind"] == "cloud" {
			t.Fatal("one-time copy shows as its own space")
		}
	}
	// Disconnecting a one-time drive leaves the files.
	a.do("DELETE", fmt.Sprintf("/api/v1/clouds/%d", int(d["id"].(float64))), nil)
	if res, _ := a.do("GET", "/api/v1/fs/stat?space=home&path=/Box%20(2)/notes.txt", nil); res.Code != 200 {
		t.Fatalf("one-time copy removed on disconnect: %d", res.Code)
	}
}

func TestBadRemoteIsRefused(t *testing.T) {
	s, h := testServer(t)
	a := newPerson(t, s, h, "a@example.com", "admin")
	remoteFixture(t)
	res, _ := a.do("POST", "/api/v1/clouds", map[string]any{"kind": "rclone", "fields": map[string]string{"config": "[x]\ntype = alias\nremote = /nope/missing\n"}})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("missing remote accepted: %d", res.Code)
	}
	if res, _ := a.do("GET", "/api/v1/clouds/oauth/gdrive/start", nil); res.Code != http.StatusBadRequest {
		t.Fatalf("OAuth start without a client: %d", res.Code)
	}
}

func TestPickedFolderKeepsLocalEdits(t *testing.T) {
	s, h := testServer(t)
	a := newPerson(t, s, h, "a@example.com", "admin")
	src := remoteFixture(t)
	res, d := a.do("POST", "/api/v1/clouds", map[string]any{"kind": "rclone", "name": "Work", "interval_hours": 6,
		"dest_space": "home", "dest_path": "/Projects/From cloud",
		"fields": map[string]string{"config": fmt.Sprintf("[x]\ntype = alias\nremote = %s\n", src)}})
	if res.Code != http.StatusCreated {
		t.Fatalf("create: %d %v", res.Code, d)
	}
	id := d["id"].(float64)
	got := waitCloud(t, a, id, "idle")
	if got["space"] != "home" || got["path"] != "/Projects/From cloud" || got["dest_name"] != "My files" {
		t.Fatalf("view: %v", got)
	}
	for _, sp := range a.list("GET", "/api/v1/spaces", nil) {
		if sp["kind"] == "cloud" {
			t.Fatal("a drive copying into a picked folder also got its own place")
		}
	}
	// An edit made here after the cloud's version survives the next sync.
	local := filepath.Join(s.homeDir(1), "Projects", "From cloud", "notes.txt")
	if err := os.WriteFile(local, []byte("edited here"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	_ = os.Chtimes(local, future, future)
	a.do("POST", fmt.Sprintf("/api/v1/clouds/%d/sync", int(id)), nil)
	time.Sleep(200 * time.Millisecond)
	waitCloud(t, a, id, "idle")
	if b, _ := os.ReadFile(local); string(b) != "edited here" {
		t.Fatalf("local edit replaced by %q", b)
	}
	// Disconnecting leaves the folder alone.
	a.do("DELETE", fmt.Sprintf("/api/v1/clouds/%d", int(id)), nil)
	if _, err := os.Stat(filepath.Join(s.homeDir(1), "Projects", "From cloud", "Photos", "a.jpg")); err != nil {
		t.Fatal("disconnect removed files from the picked folder")
	}
}

func TestPickedFolderMustBeWritable(t *testing.T) {
	s, h := testServer(t)
	a := newPerson(t, s, h, "a@example.com", "admin")
	remoteFixture(t)
	for _, space := range []string{"cloud:1", "share:1", "mount:999"} {
		res, _ := a.do("POST", "/api/v1/clouds", map[string]any{"kind": "rclone", "dest_space": space, "dest_path": "/",
			"fields": map[string]string{"config": "[x]\ntype = alias\nremote = /tmp\n"}})
		if res.Code < 400 {
			t.Fatalf("destination %s accepted: %d", space, res.Code)
		}
	}
	if res, _ := a.do("GET", "/api/v1/clouds/oauth/gdrive/start?dest_space=share:1", nil); res.Code < 400 {
		t.Fatal("OAuth start accepted a share as destination")
	}
}
