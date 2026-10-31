package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

func TestNoOfficeWithoutCore(t *testing.T) {
	s, h := testServer(t)
	a := newPerson(t, s, h, "a@example.com", "admin")
	_, st := a.do("GET", "/api/v1/office", nil)
	if st["available"] != false || !strings.Contains(st["reason"].(string), "Pocket Core") {
		t.Fatalf("status: %v", st)
	}
	upload(t, a, "home", "/", "Report.docx", "docx")
	if res, _ := a.do("GET", "/api/v1/office/open?space=home&path=/Report.docx", nil); res.Code != http.StatusConflict {
		t.Fatalf("open without Office: %d", res.Code)
	}
}

// fakeOffice is Pocket Core minting office:open tokens plus a Pocket Office answering
// whether its own link to Pocket Files is on.
func fakeOffice(t *testing.T, coreReason string, officeReady bool) (string, *atomic.Int32) {
	var asked atomic.Int32
	office := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		if r.URL.Path != "/api/v1/integrations/files" || r.Header.Get("Authorization") != "Bearer pcx_office" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		out := map[string]any{"ready": officeReady, "open_url": "https://office.test/open/files"}
		if !officeReady {
			out["reason"] = "Pocket Office's link to Pocket Files is off"
		}
		json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(office.Close)
	c := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			App    string   `json:"app"`
			Scopes []string `json:"scopes"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		if r.URL.Path != "/api/v1/links/token" || in.App != "office" || strings.Join(in.Scopes, " ") != "office:open" {
			http.Error(w, "unexpected", http.StatusBadRequest)
			return
		}
		if coreReason != "" {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{"error": "link is off", "reason": coreReason})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"access_token": "pcx_office", "expires_in": 300, "app": "office", "url": office.URL})
	}))
	t.Cleanup(c.Close)
	return c.URL, &asked
}

func TestOfficeOnlyWhenInstalledAndConnected(t *testing.T) {
	for _, tc := range []struct {
		name        string
		coreReason  string
		officeReady bool
		want        bool
		reason      string
	}{
		{"not installed", "target_missing", false, false, "not installed"},
		{"link off in Core", "off_admin", false, false, "switched off"},
		{"Office cannot reach Files", "", false, false, "link to Pocket Files is off"},
		{"connected", "", true, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, h := testServer(t)
			a := newPerson(t, s, h, "a@example.com", "admin")
			coreURL, _ := fakeOffice(t, tc.coreReason, tc.officeReady)
			s.core = core.New(coreURL, core.Options{Token: "pca_files"})
			_, st := a.do("GET", "/api/v1/office", nil)
			if st["available"] != tc.want || (tc.reason != "" && !strings.Contains(st["reason"].(string), tc.reason)) {
				t.Fatalf("status: %v", st)
			}
		})
	}
}

func TestOfficeOpensByWebDAVPath(t *testing.T) {
	s, h := testServer(t)
	a := newPerson(t, s, h, "a@example.com", "admin")
	b := newPerson(t, s, h, "b@example.com", "member")
	coreURL, asked := fakeOffice(t, "", true)
	s.core = core.New(coreURL, core.Options{Token: "pca_files"})

	upload(t, a, "home", "/Work", "Q3 report.docx", "docx")
	open := func(c *client, space, p string) (int, string) {
		res, out := c.do("GET", "/api/v1/office/open?space="+url.QueryEscape(space)+"&path="+url.QueryEscape(p), nil)
		u, _ := out["url"].(string)
		if u == "" {
			return res.Code, ""
		}
		parsed, _ := url.Parse(u)
		return res.Code, parsed.Query().Get("path") + "|" + parsed.Query().Get("embed")
	}
	if code, got := open(a, "home", "/Work/Q3 report.docx"); code != 200 || got != "Work/Q3 report.docx|1" {
		t.Fatalf("home: %d %q", code, got)
	}
	// A mount, as Office sees it through WebDAV.
	root := s.cfg.MountRoots[0]
	_, m := a.do("POST", "/api/v1/mounts", map[string]any{"name": "Media", "path": filepath.Join(root, "media"), "everyone_perm": "edit"})
	upload(t, a, "mount:"+jsonNum(m["id"]), "/", "Budget.xlsx", "xlsx")
	if code, got := open(a, "mount:"+jsonNum(m["id"]), "/Budget.xlsx"); code != 200 || got != "Mounts/Media/Budget.xlsx|1" {
		t.Fatalf("mount: %d %q", code, got)
	}
	// A folder shared with someone else, under "Shared with me".
	a.do("PUT", "/api/v1/shares/path", map[string]any{"path": "/Work", "members": []map[string]any{{"user_id": 2, "perm": "edit"}}})
	var share string
	for _, sp := range b.list("GET", "/api/v1/spaces", nil) {
		if sp["kind"] == "share" {
			share = sp["id"].(string)
		}
	}
	if code, got := open(b, share, "/Q3 report.docx"); code != 200 || got != "Shared with me/Work/Q3 report.docx|1" {
		t.Fatalf("share: %d %q", code, got)
	}
	// Folders are not documents; and the answer is remembered rather than asked every time.
	if code, _ := open(a, "home", "/Work"); code != http.StatusBadRequest {
		t.Fatalf("folder: %d", code)
	}
	if n := asked.Load(); n > 2 {
		t.Fatalf("Office asked %d times for two people", n)
	}
}
