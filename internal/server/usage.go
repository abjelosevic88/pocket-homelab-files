package server

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/abjelosevic88/pocket-homelab-files/internal/store"
	"github.com/abjelosevic88/pocket-homelab-files/internal/vfs"
)

// Adding up a folder walks every file in it, which takes seconds on a home and minutes on a media disk.
// Totals are kept per folder and served at once; a write or age refreshes them in the background.
const (
	usageMaxAge = 10 * time.Minute
	// A write anywhere marks every total old; big folders wait this long before counting again.
	usageWriteGap = time.Minute
)

type folderUsage struct {
	files   int
	size    int64
	changed int64 // s.changed when it was counted
	at      time.Time
}

type usageCache struct {
	mu       sync.Mutex
	byRoot   map[string]folderUsage
	counting map[string]bool
	slots    chan struct{} // background walks at once
}

// dirUsage returns a folder's totals, whether they are known, and whether a fresher count is on its way.
// With wait, an unknown folder is counted now; otherwise it is counted in the background.
func (s *Server) dirUsage(sp *vfs.Space, writeGap time.Duration, wait bool) (u folderUsage, known, stale bool) {
	c := &s.usage
	c.mu.Lock()
	u, known = c.byRoot[sp.Root]
	age := time.Since(u.at)
	stale = !known || age > usageMaxAge || (u.changed != s.changed.Load() && age > writeGap)
	if stale && (known || !wait) && !c.counting[sp.Root] {
		if c.counting == nil {
			c.counting, c.slots = map[string]bool{}, make(chan struct{}, 2)
		}
		c.counting[sp.Root] = true
		go func() {
			c.slots <- struct{}{}
			s.countDir(sp)
			<-c.slots
			c.mu.Lock()
			delete(c.counting, sp.Root)
			c.mu.Unlock()
		}()
	}
	c.mu.Unlock()
	if !known && wait {
		u, known, stale = s.countDir(sp), true, false
	}
	return u, known, stale
}

func (s *Server) countDir(sp *vfs.Space) folderUsage {
	changed := s.changed.Load()
	files, size := sp.Usage("/")
	u := folderUsage{files: files, size: size, changed: changed, at: time.Now()}
	s.usage.mu.Lock()
	if s.usage.byRoot == nil {
		s.usage.byRoot = map[string]folderUsage{}
	}
	s.usage.byRoot[sp.Root] = u
	s.usage.mu.Unlock()
	return u
}

// homeUsage returns the person's file count and bytes, and whether a fresher count is on its way.
func (s *Server) homeUsage(uid int64) (files int, size int64, stale bool) {
	u, _, stale := s.dirUsage(s.homeSpace(uid), 0, true)
	return u.files, u.size, stale
}

type spaceSize struct {
	Files int   `json:"files"`
	Size  int64 `json:"size"`
	// What the place can hold: the server disk under a mount, the account behind a drive.
	Total int64 `json:"total,omitempty"`
	Free  int64 `json:"free,omitempty"`
}

// handleSpaceSizes reports how much each mount and connected drive in the sidebar holds, and how
// much its disk or account can hold. Folders not counted yet are left out and pending is set; the
// sidebar asks again shortly.
func (s *Server) handleSpaceSizes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := me(r)
	sizes, pending := map[string]spaceSize{}, false
	add := func(sp *vfs.Space, total, free int64, asking bool) {
		u, known, stale := s.dirUsage(sp, usageWriteGap, false)
		if known {
			sizes[sp.ID] = spaceSize{Files: u.files, Size: u.size, Total: total, Free: free}
		}
		pending = pending || stale || asking
	}
	if mounts, err := s.store.MountsFor(ctx, uid); err == nil {
		for i := range mounts {
			if sp, err := s.mountSpace(&mounts[i]); err == nil {
				free, total := diskFree(sp.Root)
				add(sp, int64(total), int64(free), false)
			}
		}
	}
	if clouds, err := s.store.CloudsFor(ctx, uid); err == nil {
		for i := range clouds {
			if ownSpace(&clouds[i]) {
				q, asking := s.cloudQuota(&clouds[i])
				add(s.cloudSpace(&clouds[i]), q.total, q.free, asking)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sizes": sizes, "pending": pending})
}

// A cloud's size limit changes slowly and asking costs a call to the cloud, so it is kept an hour.
const quotaMaxAge = time.Hour

type accountQuota struct {
	total, free int64 // zero when the cloud does not say
	at          time.Time
}

type quotaCache struct {
	mu     sync.Mutex
	byID   map[int64]accountQuota
	asking map[int64]bool
}

// cloudQuota returns what is known of a drive's account size, and whether the cloud is being asked now.
func (s *Server) cloudQuota(c *store.Cloud) (accountQuota, bool) {
	qc := &s.quota
	qc.mu.Lock()
	defer qc.mu.Unlock()
	q, known := qc.byID[c.ID]
	if (!known || time.Since(q.at) > quotaMaxAge) && !qc.asking[c.ID] {
		if qc.asking == nil {
			qc.byID, qc.asking = map[int64]accountQuota{}, map[int64]bool{}
		}
		qc.asking[c.ID] = true
		cloud := *c
		go func() {
			got := accountQuota{at: time.Now()}
			if cfg, err := s.runtimeConfig(&cloud); err == nil {
				if a, err := s.rclone().About(context.Background(), cfg); err == nil && a.Total != nil && *a.Total > 0 {
					got.total = *a.Total
					switch {
					case a.Free != nil:
						got.free = *a.Free
					case a.Used != nil:
						got.free = max(0, *a.Total-*a.Used)
					}
				} else if err != nil {
					s.log.Debug("cloud quota", "cloud", cloud.ID, "err", err)
				}
			}
			qc.mu.Lock()
			qc.byID[cloud.ID] = got
			delete(qc.asking, cloud.ID)
			qc.mu.Unlock()
		}()
	}
	return q, qc.asking[c.ID]
}
