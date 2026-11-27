package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// fakeCapCore is a Pocket Core that vouches for link tokens
// pcx_<alice|bob>_<search|day|widget|capture|read|write> and remembers the
// manifest Files registers with.
type fakeCapCore struct {
	*httptest.Server
	mu       sync.Mutex
	manifest map[string]any
}

func withCapCore(t *testing.T, s *Server) *fakeCapCore {
	t.Helper()
	fc := &fakeCapCore{}
	scopes := map[string]string{"search": core.ScopeSearch, "day": core.ScopeDay, "widget": core.ScopeWidget, "capture": core.ScopeCapture,
		"read": "files:read", "write": "files:write"}
	fc.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/register":
			var m map[string]any
			_ = json.NewDecoder(r.Body).Decode(&m)
			fc.mu.Lock()
			fc.manifest = m
			fc.mu.Unlock()
			_, _ = io.WriteString(w, `{}`)
		case "/api/v1/auth/introspect":
			var in struct{ Token string }
			_ = json.NewDecoder(r.Body).Decode(&in)
			parts := strings.Split(in.Token, "_")
			if len(parts) != 3 || parts[0] != "pcx" || scopes[parts[2]] == "" {
				_, _ = io.WriteString(w, `{"active":false}`)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"active": true, "kind": "delegated", "scope": scopes[parts[2]], "audience": "files", "iss": "https://core.test",
				"exp":  time.Now().Add(time.Hour).Unix(),
				"user": map[string]any{"sub": "sub-" + parts[1], "email": parts[1] + "@example.com", "name": parts[1]},
				"app":  map[string]any{"id": 9, "slug": "search", "name": "Pocket Search"},
			})
		default:
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(fc.Close)
	s.core = core.New(fc.URL, core.Options{DB: s.store.DB})
	return fc
}

// rows are the items of a capability answer.
func rows(t *testing.T, m map[string]any) []map[string]any {
	t.Helper()
	l, ok := m["items"].([]any)
	if !ok {
		t.Fatalf("no items list in %v", m)
	}
	out := []map[string]any{}
	for _, v := range l {
		out = append(out, v.(map[string]any))
	}
	return out
}

// titles are the rows' titles in order, for messages and order checks.
func titles(list []map[string]any) string {
	var out []string
	for _, it := range list {
		out = append(out, it["title"].(string))
	}
	return strings.Join(out, " | ")
}

// pocketUntil asks a capability route until ok likes the answer: the index
// learns of a change a moment after it is made. It returns the last answer.
func pocketUntil(t *testing.T, c *client, path string, ok func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		res, out := c.do("GET", path, nil)
		if res.Code != 200 {
			t.Fatalf("GET %s: %d %s", path, res.Code, res.Body.String())
		}
		if ok(out) || time.Now().After(deadline) {
			return out
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// pocketFor waits for an answer with want items.
func pocketFor(t *testing.T, c *client, path string, want int) map[string]any {
	t.Helper()
	return pocketUntil(t, c, path, func(m map[string]any) bool { return len(rows(t, m)) == want })
}

func pocketSearchFor(t *testing.T, c *client, q string, want int) []map[string]any {
	t.Helper()
	got := rows(t, pocketFor(t, c, core.SearchPath+"?q="+url.QueryEscape(q), want))
	if len(got) != want {
		t.Fatalf("search %q: %d items, want %d: %s", q, len(got), want, titles(got))
	}
	return got
}

// uploadDated uploads a file that was last changed at when, as a browser
// does with a file's own date.
func uploadDated(t *testing.T, c *client, space, dir, name, body string, when time.Time) {
	t.Helper()
	q := url.Values{"space": {space}, "path": {dir}, "name": {name}, "mtime": {strconv.FormatInt(when.UnixMilli(), 10)}}
	if w, _ := c.do("PUT", "/api/v1/fs/upload?"+q.Encode(), body); w.Code != 200 {
		t.Fatalf("upload %s: %d %s", name, w.Code, w.Body.String())
	}
}

// twoPeople is Alice (admin) and Bob with what the access tests need: Alice's
// own file, Bob's /Holiday shared with Alice, Bob's /Private shared with
// nobody, and the server folder Media that Alice set up and let Bob into.
func twoPeople(t *testing.T) (s *Server, h http.Handler, alice, bob *client, share, mount string) {
	t.Helper()
	s, h = testServer(t)
	alice = newPerson(t, s, h, "alice@example.com", "admin")
	bob = newPerson(t, s, h, "bob@example.com", "member")
	upload(t, alice, "home", "/Papers", "Tax return 2026.txt", "Total refund for the electricity rebate")
	upload(t, bob, "home", "/Holiday", "plan.md", "Electricity in the cabin is solar")
	upload(t, bob, "home", "/Private", "diary.md", "electricity bill again")
	w, m := alice.do("POST", "/api/v1/mounts", map[string]any{"name": "Media", "path": filepath.Join(s.cfg.MountRoots[0], "media"), "access": []map[string]any{{"user_id": 2, "perm": "view"}}})
	if w.Code != 200 {
		t.Fatalf("mount: %d %s", w.Code, w.Body.String())
	}
	w, sh := bob.do("PUT", "/api/v1/shares/path", map[string]any{"path": "/Holiday", "members": []map[string]any{{"user_id": 1, "perm": "view"}}})
	if w.Code != 200 {
		t.Fatalf("share: %d %s", w.Code, w.Body.String())
	}
	share, mount = "share:"+jsonNum(sh["id"]), "mount:"+jsonNum(m["id"])
	// The index walks a new server folder on its next round; the tests ask for it now.
	s.search.Touch(mount, "/")
	return s, h, alice, bob, share, mount
}

func TestPocketSearch(t *testing.T) {
	s, h, alice, bob, share, mount := twoPeople(t)

	// by name: where it is stands in the snippet
	got := pocketSearchFor(t, alice, "return", 1)
	if it := got[0]; it["id"] != "home:/Papers/Tax return 2026.txt" || it["title"] != "Tax return 2026.txt" || it["kind"] != "file" || it["snippet"] != "My files/Papers" ||
		it["url"] != "/f/home/Papers?open=Tax%20return%202026.txt" {
		t.Fatalf("by name: %v", it)
	}
	if when, err := time.Parse(time.RFC3339, got[0]["date"].(string)); err != nil || time.Since(when) > time.Minute || time.Since(when) < -time.Minute {
		t.Fatalf("date: %v", got[0]["date"])
	}
	// by the text inside: the words found, without the marks the app's own search carries
	got = pocketSearchFor(t, alice, "electricity", 2)
	byID := map[string]map[string]any{}
	for _, it := range got {
		byID[it["id"].(string)] = it
		if sn := it["snippet"].(string); !strings.Contains(strings.ToLower(sn), "electricity") || strings.ContainsAny(sn, "\ue000\ue001") {
			t.Fatalf("snippet: %q", sn)
		}
	}
	if byID["home:/Papers/Tax return 2026.txt"]["snippet"] != "Total refund for the electricity rebate" {
		t.Fatalf("her own: %v", got)
	}
	// Bob's shared folder is found under the share, his private folder never
	plan := byID[share+":/plan.md"]
	if plan == nil || plan["title"] != "plan.md" || plan["url"] != "/f/"+strings.ReplaceAll(share, ":", "%3A")+"?open=plan.md" {
		t.Fatalf("the shared file: %v", got)
	}
	// Bob finds his own two, and the server folder he was let into (now the index has all there is)
	if got := pocketSearchFor(t, bob, "electricity", 2); !strings.HasPrefix(got[0]["id"].(string), "home:/") || !strings.HasPrefix(got[1]["id"].(string), "home:/") {
		t.Fatalf("bob's own: %v", got)
	}
	if got := pocketSearchFor(t, bob, "film", 1); got[0]["id"] != mount+":/movies/film.mkv" || got[0]["snippet"] != "Media/movies" ||
		got[0]["url"] != "/f/"+strings.ReplaceAll(mount, ":", "%3A")+"/movies?open=film.mkv" {
		t.Fatalf("in the server folder: %v", got)
	}
	// Alice set the server folder up, so it is hers to open too; Bob's private folder is not
	if got := pocketSearchFor(t, alice, "film", 1); got[0]["id"] != mount+":/movies/film.mkv" {
		t.Fatalf("alice in the server folder: %v", got)
	}
	for _, q := range []string{"diary", "bill", "Private"} {
		if got := rows(t, pocketFor(t, alice, core.SearchPath+"?q="+q, 0)); len(got) != 0 {
			t.Fatalf("alice finds %q in a place she cannot open: %v", q, got)
		}
	}
	// Carol was let into nothing: not the server folder, not the share, nobody's home
	carol := newPerson(t, s, h, "carol@example.com", "member")
	for _, q := range []string{"film", "movies", "electricity", "plan", "return", "type:file", "type:folder"} {
		if got := rows(t, pocketFor(t, carol, core.SearchPath+"?q="+q, 0)); len(got) != 0 {
			t.Fatalf("carol finds %q in a place she cannot open: %v", q, got)
		}
	}
	if got := rows(t, pocketFor(t, bob, core.SearchPath+"?q=refund", 0)); len(got) != 0 {
		t.Fatalf("bob finds alice's file: %v", got)
	}
	// folders, and the search box's own filters
	if got := pocketSearchFor(t, alice, "papers type:folder", 1); got[0]["kind"] != "folder" || got[0]["id"] != "home:/Papers" || got[0]["url"] != "/f/home/Papers" || got[0]["snippet"] != "My files" {
		t.Fatalf("folder: %v", got)
	}
	if got := pocketSearchFor(t, alice, "electricity ext:md", 1); got[0]["id"] != share+":/plan.md" {
		t.Fatalf("ext: %v", got)
	}
	if got := rows(t, pocketFor(t, alice, core.SearchPath+"?q=electricity&limit=1", 1)); len(got) != 1 {
		t.Fatalf("limit 1: %v", got)
	}
	if _, out := alice.do("GET", core.SearchPath+"?q=", nil); len(rows(t, out)) != 0 {
		t.Fatalf("no words: %v", out)
	}
	// the share taken back: gone from her results at once
	if w, _ := bob.do("PUT", "/api/v1/shares/path", map[string]any{"path": "/Holiday", "members": []map[string]any{}}); w.Code != 200 {
		t.Fatalf("unshare: %d %s", w.Code, w.Body.String())
	}
	if _, out := alice.do("GET", core.SearchPath+"?q=electricity", nil); len(rows(t, out)) != 1 || rows(t, out)[0]["id"] != "home:/Papers/Tax return 2026.txt" {
		t.Fatalf("after the share ended: %v", out)
	}
	// the app's own search still answers as before, marks and all
	hits := searchFor(t, alice, "electricity", 1)
	if hit := hits[0].(map[string]any); hit["space"] != "home" || hit["path"] != "/Papers/Tax return 2026.txt" || !strings.Contains(hit["snippet"].(string), "\ue000") {
		t.Fatalf("/search changed: %v", hits)
	}
	anon := &client{t: t, h: h}
	if w, _ := anon.do("GET", core.SearchPath+"?q=return", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("without signing in: %d", w.Code)
	}
}

// An address must open the right file whatever its name holds: spaces, "#",
// "?", "%", "&", "+" and letters outside ASCII, in the name and in its folders.
func TestPocketSearchAddresses(t *testing.T) {
	s, h := testServer(t)
	alice := newPerson(t, s, h, "alice@example.com", "admin")
	const dir, name = "/Moji papiri & računi/100% #1", "Šta je ovo? Račun #7 + prilog.txt"
	upload(t, alice, "home", dir, name, "struja i voda")

	// what the web app's router makes of an address: /f/<space>/<folders…> and ?open=<name>
	opens := func(addr string) (space, folder, open string) {
		u, err := url.Parse("https://files.example" + addr)
		if err != nil || u.Fragment != "" {
			t.Fatalf("address %q: %v (fragment %q)", addr, err, u.Fragment)
		}
		parts := strings.Split(strings.TrimPrefix(u.EscapedPath(), "/f/"), "/")
		for i, p := range parts {
			if parts[i], err = url.PathUnescape(p); err != nil {
				t.Fatalf("address %q: %v", addr, err)
			}
		}
		return parts[0], "/" + strings.Join(parts[1:], "/"), u.Query().Get("open")
	}
	got := pocketSearchFor(t, alice, "prilog", 1)
	if got[0]["id"] != "home:"+dir+"/"+name || got[0]["title"] != name || got[0]["snippet"] != "My files"+dir {
		t.Fatalf("the file: %v", got[0])
	}
	if want := "/f/home/Moji%20papiri%20%26%20ra%C4%8Duni/100%25%20%231?open=%C5%A0ta%20je%20ovo%3F%20Ra%C4%8Dun%20%237%20%2B%20prilog.txt"; got[0]["url"] != want {
		t.Fatalf("address:\n got %v\nwant %s", got[0]["url"], want)
	}
	if space, folder, open := opens(got[0]["url"].(string)); space != "home" || folder != dir || open != name {
		t.Fatalf("the address opens %q %q %q", space, folder, open)
	}
	// and the app serves that very file from where the address points
	if w, _ := alice.do("GET", "/api/v1/fs/raw?"+url.Values{"space": {"home"}, "path": {dir + "/" + name}}.Encode(), nil); w.Code != 200 || w.Body.String() != "struja i voda" {
		t.Fatalf("the file behind it: %d %s", w.Code, w.Body.String())
	}
	// a folder opens as itself
	got = pocketSearchFor(t, alice, `"100% #1" type:folder`, 1)
	if space, folder, open := opens(got[0]["url"].(string)); got[0]["kind"] != "folder" || space != "home" || folder != dir || open != "" {
		t.Fatalf("the folder: %v", got[0])
	}
}

func TestPocketWidget(t *testing.T) {
	s, _, alice, bob, share, _ := twoPeople(t)
	carol := newPerson(t, s, alice.h, "carol@example.com", "member")
	// nothing kept here: nothing to show
	if _, w := carol.do("GET", core.WidgetPath, nil); w["line"] != nil || w["metrics"] != nil || w["url"] != nil || len(rows(t, w)) != 0 {
		t.Fatalf("an empty home: %v", w)
	}
	now := time.Now()
	uploadDated(t, alice, "home", "/", "Old letter.txt", "x", now.AddDate(0, 0, -1))
	uploadDated(t, alice, "home", "/Papers/2019", "Lease.pdf", "x", time.Date(2019, 3, 4, 12, 0, 0, 0, time.UTC))

	// Once the index has all of Bob's and all of hers: today's first (her own file and
	// Bob's shared plan), then yesterday's, then the old one. Bob's private folder is not
	// hers, and the server folder both may open is in nobody's widget.
	bw := pocketFor(t, bob, core.WidgetPath, 2)
	if got := titles(rows(t, bw)); len(rows(t, bw)) != 2 || !strings.Contains(got, "plan.md") || !strings.Contains(got, "diary.md") || bw["line"] != "2 files changed today" {
		t.Fatalf("bob's widget: %s (%v)", got, bw["line"])
	}
	pocketUntil(t, alice, core.WidgetPath, func(m map[string]any) bool {
		got := titles(rows(t, m))
		return strings.Contains(got, "Tax return") && strings.Contains(got, "Old letter") && strings.Contains(got, "Lease")
	})
	_, w := alice.do("GET", core.WidgetPath, nil)
	list := rows(t, w)
	if got := titles(list); len(list) != 4 || !strings.Contains(got, "plan.md") || !strings.Contains(got, "Tax return 2026.txt") || list[2]["title"] != "Old letter.txt" || list[3]["title"] != "Lease.pdf" {
		t.Fatalf("widget rows: %s", got)
	}
	if w["url"] != "/f/home" || w["line"] != "2 files changed today" {
		t.Fatalf("widget: %v", w)
	}
	byTitle := map[string]map[string]any{}
	for _, it := range list {
		byTitle[it["title"].(string)] = it
	}
	if it := byTitle["Tax return 2026.txt"]; it == nil || it["id"] != "home:/Papers/Tax return 2026.txt" || it["detail"] != "today · Papers" || it["url"] != "/f/home/Papers?open=Tax%20return%202026.txt" {
		t.Fatalf("her own file: %v", it)
	}
	if it := byTitle["plan.md"]; it == nil || it["id"] != share+":/plan.md" || it["detail"] != "today · Holiday" || it["url"] != "/f/"+strings.ReplaceAll(share, ":", "%3A")+"?open=plan.md" {
		t.Fatalf("the shared file: %v", it)
	}
	if it := byTitle["Old letter.txt"]; it["detail"] != "yesterday · My files" || it["url"] != "/f/home?open=Old%20letter.txt" {
		t.Fatalf("yesterday's: %v", it)
	}
	if it := byTitle["Lease.pdf"]; it["detail"] != "4 Mar 2019 · 2019" {
		t.Fatalf("the old one: %v", it)
	}
	// what My files holds shows once the app has counted it (the sidebar asks), never counted here
	if w["metrics"] != nil {
		t.Fatalf("totals before they were counted: %v", w["metrics"])
	}
	if res, _ := alice.do("GET", "/api/v1/settings/system", nil); res.Code != 200 {
		t.Fatalf("settings/system: %d", res.Code)
	}
	_, w = alice.do("GET", core.WidgetPath, nil)
	ms, _ := w["metrics"].([]any)
	if len(ms) != 2 || ms[0].(map[string]any)["label"] != "Files" || ms[0].(map[string]any)["value"] != "3" ||
		ms[1].(map[string]any)["label"] != "Space used" || ms[1].(map[string]any)["value"] != "41 B" {
		t.Fatalf("metrics: %v", w["metrics"])
	}
	if _, bw := bob.do("GET", core.WidgetPath, nil); bw["metrics"] != nil {
		t.Fatalf("bob gets alice's totals: %v", bw["metrics"])
	}

	// only the six newest are listed; the line counts the day
	for i := range 6 {
		upload(t, alice, "home", "/Inbox", "scan "+strconv.Itoa(i)+".txt", "x")
	}
	w = pocketUntil(t, alice, core.WidgetPath, func(m map[string]any) bool { return m["line"] == "8 files changed today" })
	if list := rows(t, w); len(list) != 6 || w["line"] != "8 files changed today" || !strings.HasPrefix(list[0]["title"].(string), "scan ") || list[0]["detail"] != "today · Inbox" {
		t.Fatalf("after six more: %v", w)
	}
	// the share taken back: Bob's file leaves her widget
	if res, _ := bob.do("PUT", "/api/v1/shares/path", map[string]any{"path": "/Holiday", "members": []map[string]any{}}); res.Code != 200 {
		t.Fatalf("unshare: %d", res.Code)
	}
	if _, w = alice.do("GET", core.WidgetPath, nil); w["line"] != "7 files changed today" || strings.Contains(titles(rows(t, w)), "plan.md") {
		t.Fatalf("after the share ended: %v", w)
	}
}

func TestWidgetWords(t *testing.T) {
	for n, want := range map[int]string{0: "0", 7: "7", 999: "999", 1204: "1,204", 1234567: "1,234,567"} {
		if got := thousands(n); got != want {
			t.Errorf("thousands(%d) = %q, want %q", n, got, want)
		}
	}
	for n, want := range map[int64]string{0: "0 B", 41: "41 B", 2048: "2 KB", 5 << 20: "5.0 MB", 3435973837: "3.20 GB", 6399501267763: "5.82 TB"} {
		if got := sizeWords(n); got != want {
			t.Errorf("sizeWords(%d) = %q, want %q", n, got, want)
		}
	}
	now := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		at   time.Time
		want string
	}{
		{now.Add(-time.Hour), "today"}, {now.Add(-10 * time.Hour), "yesterday"}, {now.AddDate(0, 0, -3), "Tuesday"},
		{now.AddDate(0, 0, -15), "12 Nov"}, {now.AddDate(-1, 0, 0), "27 Nov 2025"},
	} {
		if got := dayWord(c.at, now); got != c.want {
			t.Errorf("dayWord(%s) = %q, want %q", c.at.Format(time.RFC3339), got, c.want)
		}
	}
}

// A link token with one pocket:* scope reaches its one route and nothing
// else, and the old files:* scopes do not open the new routes.
func TestPocketLinkTokens(t *testing.T) {
	s, h, alice, bob, share, _ := twoPeople(t)
	withCapCore(t, s)
	pocketSearchFor(t, alice, "electricity", 2) // the index has caught up, with her things
	pocketSearchFor(t, bob, "electricity", 2)   // and with all of Bob's
	tok := func(name string) *client { return &client{t: t, h: h, token: name} }
	for _, c := range []struct {
		tok, method, path string
		want              int
	}{
		{"pcx_alice_search", "GET", core.SearchPath + "?q=electricity", 200},
		{"pcx_alice_widget", "GET", core.WidgetPath, 200},
		{"pcx_alice_search", "GET", "/api/v1/auth/me", 200},
		{"pcx_alice_widget", "GET", "/api/v1/auth/me", 200},
		// the other capability's route
		{"pcx_alice_search", "GET", core.WidgetPath, 403},
		{"pcx_alice_widget", "GET", core.SearchPath + "?q=electricity", 403},
		// what Files does not declare is not there, and a token for it opens nothing else
		{"pcx_alice_day", "GET", core.DayPath + "2026-11-27", 404},
		{"pcx_alice_day", "GET", core.SearchPath + "?q=electricity", 403},
		{"pcx_alice_capture", "POST", core.CapturePath, 404},
		{"pcx_alice_capture", "GET", core.WidgetPath, 403},
		// the rest of the app
		{"pcx_alice_search", "GET", "/api/v1/search?q=electricity", 403},
		{"pcx_alice_search", "GET", "/api/v1/fs/search?space=home&path=/&q=refund", 403},
		{"pcx_alice_search", "GET", "/api/v1/fs/list?space=home&path=/", 403},
		{"pcx_alice_search", "GET", "/api/v1/fs/raw?space=home&path=/Papers/Tax%20return%202026.txt", 403},
		{"pcx_alice_search", "GET", "/api/v1/spaces", 403},
		{"pcx_alice_search", "PROPFIND", "/dav/", 403},
		{"pcx_alice_search", "POST", core.SearchPath + "?q=electricity", 405},
		{"pcx_alice_search", "PUT", "/api/v1/fs/upload?space=home&path=/&name=x.txt", 403},
		{"pcx_alice_widget", "GET", "/api/v1/spaces/sizes", 403},
		{"pcx_alice_widget", "GET", "/api/v1/settings/system", 403},
		{"pcx_alice_widget", "GET", "/api/v1/fs/usage?space=home&path=/", 403},
		{"pcx_alice_widget", "GET", "/api/v1/stars", 403},
		{"pcx_alice_widget", "GET", "/api/v1/export.zip", 403},
		// the old scopes keep what they had and get nothing new
		{"pcx_alice_read", "GET", "/api/v1/search?q=electricity", 200},
		{"pcx_alice_read", "GET", core.SearchPath + "?q=electricity", 403},
		{"pcx_alice_read", "GET", core.WidgetPath, 403},
		{"pcx_alice_write", "GET", core.SearchPath + "?q=electricity", 403},
		{"pcx_alice_write", "GET", core.WidgetPath, 403},
		// not a token Core vouches for
		{"pcx_alice_nothing", "GET", core.SearchPath + "?q=electricity", 401},
	} {
		if w, _ := tok(c.tok).do(c.method, c.path, "x"); w.Code != c.want {
			t.Errorf("%s %s with %s: %d, want %d", c.method, c.path, c.tok, w.Code, c.want)
		}
	}

	// through a link the answer is the person's: Alice's own and the folder shared with her, Bob's all of his
	ids := func(list []map[string]any) string {
		var out []string
		for _, it := range list {
			out = append(out, it["id"].(string))
		}
		return strings.Join(out, " | ")
	}
	_, out := tok("pcx_alice_search").do("GET", core.SearchPath+"?q=electricity", nil)
	if got := ids(rows(t, out)); len(rows(t, out)) != 2 || !strings.Contains(got, "home:/Papers/Tax return 2026.txt") || !strings.Contains(got, share+":/plan.md") || strings.Contains(got, "diary") {
		t.Fatalf("alice through a link: %s", got)
	}
	_, out = tok("pcx_bob_search").do("GET", core.SearchPath+"?q=electricity", nil)
	if got := ids(rows(t, out)); got != "home:/Holiday/plan.md | home:/Private/diary.md" && got != "home:/Private/diary.md | home:/Holiday/plan.md" {
		t.Fatalf("bob through a link: %s", got)
	}
	_, out = tok("pcx_alice_widget").do("GET", core.WidgetPath, nil)
	if got := titles(rows(t, out)); len(rows(t, out)) != 2 || !strings.Contains(got, "Tax return 2026.txt") || !strings.Contains(got, "plan.md") || strings.Contains(got, "diary") || strings.Contains(got, "film") {
		t.Fatalf("alice's widget through a link: %s", got)
	}
	_, out = tok("pcx_bob_widget").do("GET", core.WidgetPath, nil)
	if got := titles(rows(t, out)); len(rows(t, out)) != 2 || !strings.Contains(got, "diary.md") || strings.Contains(got, "Tax return") || strings.Contains(got, "film") {
		t.Fatalf("bob's widget through a link: %s", got)
	}
}

// Files tells Core its shelf, what it can be asked, and the offers that go with it.
func TestManifestCapabilities(t *testing.T) {
	s, _ := testServer(t)
	fc := withCapCore(t, s)
	s.cfg.CoreRegistrationKey = "key"
	if err := s.registerWithCore(t.Context()); err != nil {
		t.Fatal(err)
	}
	fc.mu.Lock()
	m := fc.manifest
	fc.mu.Unlock()
	if m["slug"] != "files" || m["category"] != "Files" {
		t.Fatalf("manifest: slug %v, category %v", m["slug"], m["category"])
	}
	// the capabilities sit at the top of the manifest, next to the slug
	search, _ := m["search"].(map[string]any)
	widget, _ := m["widget"].(map[string]any)
	if search == nil || jsonNum(search["bangs"]) != `["files","file"]` || jsonNum(search["kinds"]) != `["file","folder"]` || search["what"] == "" {
		t.Fatalf("search: %v", m["search"])
	}
	if widget == nil || widget["title"] != "Recent files" || widget["what"] == "" {
		t.Fatalf("widget: %v", m["widget"])
	}
	if m["day"] != nil || m["capture"] != nil {
		t.Fatalf("declares more than it answers: day %v, capture %v", m["day"], m["capture"])
	}
	offers := map[string]bool{}
	for _, o := range m["offers"].([]any) {
		offers[o.(map[string]any)["scope"].(string)] = true
	}
	for scope, want := range map[string]bool{"files:read": true, "files:write": true, core.ScopeSearch: true, core.ScopeWidget: true, core.ScopeDay: false, core.ScopeCapture: false} {
		if offers[scope] != want {
			t.Errorf("offer %s: %v, want %v (%v)", scope, offers[scope], want, offers)
		}
	}
}
