package server

import (
	"sync"
	"time"
)

// Adding up a home folder walks every file in it, which takes seconds on a big one.
// The total is kept per person and served at once; a write or age refreshes it in the background.
const usageMaxAge = 5 * time.Minute

type homeUsage struct {
	files   int
	size    int64
	changed int64 // s.changed when it was counted
	at      time.Time
}

type usageCache struct {
	mu       sync.Mutex
	byUser   map[int64]homeUsage
	counting map[int64]bool
}

// homeUsage returns the person's file count and bytes, and whether a fresher count is on its way.
func (s *Server) homeUsage(uid int64) (files int, size int64, stale bool) {
	c := &s.usage
	c.mu.Lock()
	u, ok := c.byUser[uid]
	stale = ok && (u.changed != s.changed.Load() || time.Since(u.at) > usageMaxAge)
	if ok && stale && !c.counting[uid] {
		if c.counting == nil {
			c.counting = map[int64]bool{}
		}
		c.counting[uid] = true
		go func() {
			s.countHome(uid)
			c.mu.Lock()
			delete(c.counting, uid)
			c.mu.Unlock()
		}()
	}
	c.mu.Unlock()
	if !ok {
		u = s.countHome(uid)
	}
	return u.files, u.size, stale
}

func (s *Server) countHome(uid int64) homeUsage {
	changed := s.changed.Load()
	files, size := s.homeSpace(uid).Usage("/")
	u := homeUsage{files: files, size: size, changed: changed, at: time.Now()}
	s.usage.mu.Lock()
	if s.usage.byUser == nil {
		s.usage.byUser = map[int64]homeUsage{}
	}
	s.usage.byUser[uid] = u
	s.usage.mu.Unlock()
	return u
}
