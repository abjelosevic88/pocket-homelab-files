package clouds

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
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
