package clouds

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestINIRoundTrip(t *testing.T) {
	var b bytes.Buffer
	if err := WriteINI(&b, "drive", Config{"type": "drive", "token": `{"access_token":"a=b"}`}); err != nil {
		t.Fatal(err)
	}
	c, err := ParseINI(b.String())
	if err != nil {
		t.Fatal(err)
	}
	if c["type"] != "drive" || c["token"] != `{"access_token":"a=b"}` {
		t.Fatalf("got %v", c)
	}
	if _, err := ParseINI("[a]\ntype = s3\n[b]\ntype = s3"); err == nil {
		t.Fatal("two remotes accepted")
	}
	if _, err := ParseINI("host = x"); err == nil {
		t.Fatal("remote without a type accepted")
	}
	if err := WriteINI(&b, "drive", Config{"x": "a\nb"}); err == nil {
		t.Fatal("multi-line value accepted")
	}
}

func TestAuthMessages(t *testing.T) {
	for _, s := range []string{`oauth2: "invalid_grant" "Token has been expired or revoked."`, "couldn't fetch token: unauthorized_client", "AADSTS70008"} {
		if !IsAuthMessage(s) {
			t.Errorf("%q not seen as a sign-in problem", s)
		}
	}
	if IsAuthMessage("connection reset by peer") {
		t.Error("network error seen as a sign-in problem")
	}
}

func rcloneBin(t *testing.T) string {
	p, err := exec.LookPath("rclone")
	if err != nil {
		t.Skip("rclone not installed")
	}
	return p
}

// A "local" remote stands in for a cloud: real rclone, real copy, real config round trip.
func TestCopyNeverDeletes(t *testing.T) {
	r := &Rclone{Bin: rcloneBin(t), Dir: t.TempDir()}
	src, dest := t.TempDir(), t.TempDir()
	_ = os.MkdirAll(filepath.Join(src, "Docs", "Empty"), 0o755)
	_ = os.WriteFile(filepath.Join(src, "Docs", "a.txt"), []byte("hello"), 0o644)
	_ = os.WriteFile(filepath.Join(dest, "kept.txt"), []byte("mine"), 0o644)
	var seen bool
	res, err := r.Copy(context.Background(), Job{Config: Config{"type": "alias", "remote": src}, Dest: dest}, func(Stats) { seen = true })
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "Docs", "a.txt")); string(b) != "hello" {
		t.Fatalf("copied %q", b)
	}
	if _, err := os.Stat(filepath.Join(dest, "Docs", "Empty")); err != nil {
		t.Error("empty folder not copied")
	}
	if _, err := os.Stat(filepath.Join(dest, "kept.txt")); err != nil {
		t.Error("copy deleted a file that only exists here")
	}
	if res.Stats.Transfers != 1 || !seen {
		t.Errorf("stats %+v seen=%v", res.Stats, seen)
	}
	if res.Config["remote"] != src {
		t.Errorf("config not read back: %v", res.Config)
	}
	entries, _ := os.ReadDir(r.Dir)
	if len(entries) != 0 {
		t.Errorf("config file left behind: %v", entries)
	}
}

func TestCopyReportsFailure(t *testing.T) {
	r := &Rclone{Bin: rcloneBin(t), Dir: t.TempDir()}
	_, err := r.Copy(context.Background(), Job{Config: Config{"type": "alias", "remote": "/does/not/exist"}, Dest: t.TempDir()}, nil)
	if err == nil {
		t.Fatal("missing source copied fine")
	}
}

func TestCheck(t *testing.T) {
	r := &Rclone{Bin: rcloneBin(t), Dir: t.TempDir()}
	if _, err := r.Check(context.Background(), Config{"type": "alias", "remote": t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Check(context.Background(), Config{"type": "nosuchbackend"}); err == nil {
		t.Fatal("unknown backend passed the check")
	}
}

func TestUploadIntoADoc(t *testing.T) {
	var gotPath, gotType, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotType = r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery, r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		if r.Header.Get("Authorization") != "Bearer live" {
			w.WriteHeader(401)
			return
		}
		io.WriteString(w, `{"modifiedTime":"2026-10-31T10:00:00.000Z"}`)
	}))
	defer srv.Close()
	GoogleAPI = srv.URL
	f := filepath.Join(t.TempDir(), "Plan.docx")
	_ = os.WriteFile(f, []byte("docx bytes"), 0o644)
	api := &DriveAPI{Token: &Token{AccessToken: "live", Expiry: time.Now().Add(time.Hour)}}
	mt, err := api.UploadInto(context.Background(), "doc123", f, "docx")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(gotPath, "PATCH /upload/drive/v3/files/doc123?uploadType=media") || !strings.Contains(gotType, "wordprocessingml") || gotBody != "docx bytes" || mt.IsZero() {
		t.Fatalf("%s %s %q %v", gotPath, gotType, gotBody, mt)
	}
	// Without a sign-in that can be renewed, the drive asks to be connected again.
	api = &DriveAPI{Token: &Token{AccessToken: "old", Expiry: time.Now().Add(-time.Hour)}}
	if _, err := api.UploadInto(context.Background(), "doc123", f, "docx"); !errors.Is(err, ErrReconnect) {
		t.Fatalf("expired sign-in: %v", err)
	}
}

func TestConflictNames(t *testing.T) {
	c, p := ConflictNames("Work/Plan.docx")
	if c != "Work/Plan.cloud.docx" || p != "Work/Plan.pocket.docx" {
		t.Fatal(c, p)
	}
	if got, ok := conflictPath("- WARNING  New or changed in both paths       - Work/Plan.docx"); !ok || got != "Work/Plan.docx" {
		t.Fatal(got)
	}
}

func TestCopyOnlyListedFiles(t *testing.T) {
	r := &Rclone{Bin: rcloneBin(t), Dir: t.TempDir()}
	src, dest := t.TempDir(), t.TempDir()
	_ = os.MkdirAll(filepath.Join(src, "Paper"), 0o755)
	_ = os.WriteFile(filepath.Join(src, "Paper", "Plan.html"), []byte("paper"), 0o644)
	_ = os.WriteFile(filepath.Join(src, "other.txt"), []byte("other"), 0o644)
	if _, err := r.Copy(context.Background(), Job{Config: Config{"type": "alias", "remote": src}, Dest: dest, Files: []string{"Paper/Plan.html"}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "Paper", "Plan.html")); err != nil {
		t.Fatal("listed file not copied")
	}
	if _, err := os.Stat(filepath.Join(dest, "other.txt")); err == nil {
		t.Fatal("unlisted file copied")
	}
}

func TestFriendlyICloud(t *testing.T) {
	raw := errors.New(`validate2FACode failed: HTTP error 400 (400 Bad Request) returned body: "{\n \"trustedPhoneNumber\" : {\n \"pushMode\" : \"sms\" }, \"successful\" : false\n}"`)
	if got := FriendlyICloud(raw); !strings.HasPrefix(got, "Apple did not accept that code") {
		t.Fatal(got)
	}
	if got := FriendlyICloud(errors.New(`something odd: {"x": 1}`)); got != "something odd" {
		t.Fatal(got)
	}
}
