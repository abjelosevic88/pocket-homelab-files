package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/abjelosevic88/pocket-homelab-files/internal/store"
)

// People to share with: everyone with an account here plus, on a box with
// Pocket Core, everyone in the household who has not opened Pocket Files
// yet. Sharing with one of those makes their account here (no password);
// they find the share the first time they sign in with Pocket Core.

type corePerson struct {
	Email string `json:"email"`
	Name  string `json:"name"`
}

var peopleCache struct {
	sync.Mutex
	list []corePerson
	at   time.Time
}

// corePeople asks Core for the household (cached for a minute).
func (s *Server) corePeople(ctx context.Context) []corePerson {
	if !s.core.Configured() || s.core.Token() == "" {
		return nil
	}
	peopleCache.Lock()
	defer peopleCache.Unlock()
	if time.Since(peopleCache.at) < time.Minute {
		return peopleCache.list
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, s.core.BaseURL()+"/api/v1/people", nil)
	if err != nil {
		return peopleCache.list
	}
	req.Header.Set("Authorization", "Bearer "+s.core.Token())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		s.log.Debug("pocket core: people", "err", err)
		return peopleCache.list
	}
	defer res.Body.Close()
	var list []corePerson
	if res.StatusCode != http.StatusOK || json.NewDecoder(res.Body).Decode(&list) != nil {
		s.log.Debug("pocket core: people", "status", res.StatusCode)
		return peopleCache.list
	}
	for i := range list {
		list[i].Email = strings.ToLower(strings.TrimSpace(list[i].Email))
	}
	peopleCache.list, peopleCache.at = list, time.Now()
	return list
}

// people merges the local accounts with Core's household. People without an
// account here have id 0.
func (s *Server) people(ctx context.Context) ([]store.Person, error) {
	out, err := s.store.People(ctx)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, p := range out {
		known[strings.ToLower(p.Email)] = true
	}
	for _, p := range s.corePeople(ctx) {
		if p.Email != "" && !known[p.Email] {
			known[p.Email] = true
			out = append(out, store.Person{Name: p.Name, Email: p.Email})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(firstNonEmpty(out[i].Name, out[i].Email)) < strings.ToLower(firstNonEmpty(out[j].Name, out[j].Email))
	})
	return out, nil
}

// personFor finds the account to share with by id, or by email — making a
// member account for someone Core knows who has not been here yet.
func (s *Server) personFor(ctx context.Context, id int64, email string) (*store.User, error) {
	if id > 0 {
		u, err := s.store.GetUser(ctx, id)
		if err != nil {
			return nil, badRequest("unknown person")
		}
		return u, nil
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return nil, badRequest("unknown person")
	}
	if u, err := s.store.GetUserByEmail(ctx, email); err == nil {
		return u, nil
	}
	for _, p := range s.corePeople(ctx) {
		if p.Email == email {
			u := &store.User{Email: email, Name: p.Name, Role: "member"}
			if err := s.store.CreateUser(ctx, u); err != nil {
				return nil, err
			}
			s.seedUser(ctx, u.ID)
			s.log.Info("account made for a share", "email", email)
			return u, nil
		}
	}
	return nil, badRequest(fmt.Sprintf("%s has no account here; an admin can add them in Settings → People", email))
}
