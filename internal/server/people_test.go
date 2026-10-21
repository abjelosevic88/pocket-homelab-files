package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abjelosevic88/pocket-homelab-files/internal/config"
	"github.com/abjelosevic88/pocket-homelab-files/internal/store"
)

// Household people Core knows but who never opened Pocket Files can be
// shared with: the share makes their account, and Core sign-in finds it.
func TestShareWithCoreHousehold(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/people":
			if r.Header.Get("Authorization") != "Bearer pca_test" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode([]map[string]string{{"email": "owner@example.com", "name": "Owner"}, {"email": "Sani@Example.com", "name": "Sani"}})
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer fake.Close()
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "core.token"), []byte("pca_test"), 0o600)
	t.Setenv("DATA_DIR", dir)
	t.Setenv("CORE_URL", fake.URL)
	t.Setenv("TZ", "UTC")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(cfg.DBPath, log)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := New(cfg, st, log, nil)
	if err := s.core.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	h := s.Router()
	owner := newPerson(t, s, h, "owner@example.com", "admin")

	people := owner.list("GET", "/api/v1/people", nil)
	if len(people) != 2 || people[1]["email"] != "sani@example.com" || people[1]["id"].(float64) != 0 {
		t.Fatalf("people: %v", people)
	}
	owner.do("POST", "/api/v1/fs/mkdir", map[string]any{"space": "home", "path": "/Test"})
	w, sh := owner.do("PUT", "/api/v1/shares/path", map[string]any{"path": "/Test", "members": []map[string]any{{"email": "sani@example.com", "perm": "edit"}}})
	if w.Code != 200 || len(sh["members"].([]any)) != 1 {
		t.Fatalf("share by email: %d %s", w.Code, w.Body.String())
	}
	sani, err := st.GetUserByEmail(t.Context(), "sani@example.com")
	if err != nil || sani.Role != "member" || sani.PasswordHash != "" {
		t.Fatalf("account for Sani: %+v %v", sani, err)
	}
	if w, _ := owner.do("PUT", "/api/v1/shares/path", map[string]any{"path": "/Test", "members": []map[string]any{{"email": "stranger@example.com", "perm": "view"}}}); w.Code != 400 || !strings.Contains(w.Body.String(), "no account") {
		t.Fatalf("stranger: %d %s", w.Code, w.Body.String())
	}
}
