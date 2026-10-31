package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-files/internal/vfs"
)

// "Open in Pocket Office": offered only when Pocket Office is installed and connected both
// ways for the person, all through Pocket Core: this app's office:open link (Office is
// registered and the switch is on), and Office's own files:read/files:write link back here
// (Office answers that when asked with the office:open token). Office then edits the file
// over WebDAV, as the person, and saves straight back.

type officeStatus struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	OpenURL   string `json:"-"`
}

type officeEntry struct {
	st    officeStatus
	until time.Time
}

type officeCache struct {
	mu sync.Mutex
	m  map[int64]officeEntry
}

// officeFor asks Core and Office, remembering the answer briefly (a minute when it works,
// fifteen seconds when not, so switching a link on shows up quickly).
func (s *Server) officeFor(ctx context.Context, uid int64, email string) officeStatus {
	s.office.mu.Lock()
	if e, ok := s.office.m[uid]; ok && time.Now().Before(e.until) {
		s.office.mu.Unlock()
		return e.st
	}
	s.office.mu.Unlock()
	st := s.askOffice(ctx, email)
	keep := 15 * time.Second
	if st.Available {
		keep = time.Minute
	}
	s.office.mu.Lock()
	s.office.m[uid] = officeEntry{st: st, until: time.Now().Add(keep)}
	s.office.mu.Unlock()
	return st
}

var officeReasons = map[string]string{
	"target_missing": "Pocket Office is not installed",
	"not_declared":   "this version of Pocket Files does not know Pocket Office yet",
	"not_offered":    "this version of Pocket Office cannot be opened from other apps yet",
	"off_admin":      "the link from Pocket Files to Pocket Office is switched off in Pocket Core → Links",
	"off_person":     "you switched off Pocket Files → Pocket Office in Pocket Core",
}

func (s *Server) askOffice(ctx context.Context, email string) officeStatus {
	if !s.core.Configured() {
		return officeStatus{Reason: "Pocket Files is not connected to Pocket Core"}
	}
	if email == "" {
		return officeStatus{Reason: "your account has no email"}
	}
	lt, err := s.core.ActAs(ctx, email, "office", "office:open")
	if err != nil {
		var le *core.LinkError
		if errors.As(err, &le) {
			return officeStatus{Reason: firstNonEmpty(officeReasons[le.Reason], le.Message, le.Reason)}
		}
		return officeStatus{Reason: "Pocket Core did not answer"}
	}
	base := firstNonEmpty(lt.URL, lt.PublicURL)
	if base == "" {
		return officeStatus{Reason: "Pocket Core knows no address for Pocket Office"}
	}
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, strings.TrimRight(base, "/")+"/api/v1/integrations/files", nil)
	req.Header.Set("Authorization", "Bearer "+lt.AccessToken)
	req.Header.Set("Accept", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return officeStatus{Reason: "Pocket Office does not answer"}
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return officeStatus{Reason: "this version of Pocket Office cannot be opened from other apps yet"}
	}
	var out struct {
		Ready   bool   `json:"ready"`
		Reason  string `json:"reason"`
		OpenURL string `json:"open_url"`
	}
	if res.StatusCode != http.StatusOK || json.NewDecoder(res.Body).Decode(&out) != nil {
		return officeStatus{Reason: fmt.Sprintf("Pocket Office answered %s", res.Status)}
	}
	if !out.Ready || out.OpenURL == "" {
		return officeStatus{Reason: firstNonEmpty(out.Reason, "Pocket Office cannot reach your files")}
	}
	return officeStatus{Available: true, OpenURL: out.OpenURL}
}

// handleOfficeStatus: GET /office → {available, reason?} for the signed-in person.
func (s *Server) handleOfficeStatus(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	writeJSON(w, http.StatusOK, s.officeFor(r.Context(), u.ID, u.Email))
}

// handleOfficeOpen: GET /office/open?space=&path= → {url} to load in a frame.
func (s *Server) handleOfficeOpen(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	st := s.officeFor(ctx, u.ID, u.Email)
	if !st.Available {
		writeErr(w, http.StatusConflict, st.Reason)
		return
	}
	sp, err := s.spaceFromQuery(r)
	if err != nil {
		s.fail(w, err, "open")
		return
	}
	rel := vfs.Clean(r.URL.Query().Get("path"))
	if e, err := sp.Stat(rel); err != nil {
		s.fail(w, err, "open")
		return
	} else if e.IsDir {
		writeErr(w, http.StatusBadRequest, "that is a folder")
		return
	}
	dav, err := s.davPathOf(ctx, me(r), sp, rel)
	if err != nil {
		s.fail(w, err, "open")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": st.OpenURL + "?" + url.Values{"path": {dav}, "embed": {"1"}}.Encode()})
}

// davPathOf is where a file sits in this person's WebDAV drive, which is how Pocket Office
// reaches it: My files at the root, then "Mounts/<name>" and "Shared with me/<name>".
func (s *Server) davPathOf(ctx context.Context, uid int64, sp *vfs.Space, rel string) (string, error) {
	rel = strings.TrimPrefix(vfs.Clean(rel), "/")
	if sp.ID == "home" {
		return rel, nil
	}
	shares, mounts := s.davNames(ctx, uid)
	for group, set := range map[string]map[string]*vfs.Space{davMounts: mounts, davShared: shares} {
		for name, x := range set {
			if x.ID != sp.ID {
				continue
			}
			if x.File != "" {
				return group + "/" + name, nil
			}
			if rel == "" {
				return group + "/" + name, nil
			}
			return group + "/" + name + "/" + rel, nil
		}
	}
	return "", badRequest("Pocket Office cannot open files from " + sp.Name)
}
