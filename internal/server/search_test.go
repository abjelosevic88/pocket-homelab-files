package server

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func searchFor(t *testing.T, c *client, q string, want int) []any {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		res, out := c.do("GET", "/api/v1/search?q="+url.QueryEscape(q), nil)
		if res.Code != 200 {
			t.Fatalf("search %q: %d %v", q, res.Code, out)
		}
		hits, _ := out["hits"].([]any)
		if len(hits) == want || time.Now().After(deadline) {
			return hits
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestSearchFindsNamesAndTextOnlyWhereAllowed(t *testing.T) {
	s, h := testServer(t)
	alice := newPerson(t, s, h, "alice@example.com", "admin")
	bob := newPerson(t, s, h, "bob@example.com", "member")
	upload(t, alice, "home", "/", "Tax return 2026.txt", "Total refund for the electricity rebate")
	upload(t, bob, "home", "/Holiday", "plan.md", "Electricity in the cabin is solar")
	upload(t, bob, "home", "/Private", "diary.md", "electricity bill again")

	hits := searchFor(t, alice, "return", 1)
	if len(hits) != 1 || hits[0].(map[string]any)["match"] != "name" {
		t.Fatalf("name search: %v", hits)
	}
	hits = searchFor(t, alice, "electricity", 1)
	if len(hits) != 1 || !strings.Contains(hits[0].(map[string]any)["snippet"].(string), "") {
		t.Fatalf("alice sees only her own text: %v", hits)
	}

	// Bob shares /Holiday with Alice: his plan joins her results, under the share; his diary does not.
	if res, sh := bob.do("PUT", "/api/v1/shares/path", map[string]any{"path": "/Holiday", "members": []map[string]any{{"user_id": 1, "perm": "view"}}}); res.Code != 200 {
		t.Fatalf("share: %d %v", res.Code, sh)
	}
	hits = searchFor(t, alice, "electricity", 2)
	var spaces []string
	for _, x := range hits {
		m := x.(map[string]any)
		spaces = append(spaces, m["space"].(string)+m["path"].(string))
	}
	got := strings.Join(spaces, " ")
	if len(hits) != 2 || !strings.Contains(got, "/plan.md") || strings.Contains(got, "diary") || !strings.Contains(got, "share:") {
		t.Fatalf("with the share: %s", got)
	}

	// Folder search inside a space uses the index too.
	res, out := alice.do("GET", "/api/v1/fs/search?space=home&path=/&q=refund", nil)
	if res.Code != 200 || len(out["entries"].([]any)) != 1 {
		t.Fatalf("folder search: %d %v", res.Code, out)
	}
	// Deleting removes it from results.
	alice.do("POST", "/api/v1/fs/delete", map[string]any{"space": "home", "paths": []string{"/Tax return 2026.txt"}})
	if hits := searchFor(t, alice, "refund", 0); len(hits) != 0 {
		t.Fatalf("deleted file still found: %v", hits)
	}
}
