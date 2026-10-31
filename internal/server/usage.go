package server

import (
	"net/http"
	"sync"
	"time"

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

// spaceSize leaves files and size out until the space has been counted; the disk is always known.
type spaceSize struct {
	Files *int   `json:"files,omitempty"`
	Size  *int64 `json:"size,omitempty"`
	Free  uint64 `json:"free,omitempty"`
	Total uint64 `json:"total,omitempty"`
}

// handleSpaceSizes reports how much each mount and connected drive in the sidebar holds, and the disk it is on.
// Folders not counted yet are left out and pending is set; the sidebar asks again shortly.
func (s *Server) handleSpaceSizes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := me(r)
	var spaces []*vfs.Space
	if mounts, err := s.store.MountsFor(ctx, uid); err == nil {
		for i := range mounts {
			if sp, err := s.mountSpace(&mounts[i]); err == nil {
				spaces = append(spaces, sp)
			}
		}
	}
	if clouds, err := s.store.CloudsFor(ctx, uid); err == nil {
		for i := range clouds {
			if ownSpace(&clouds[i]) {
				spaces = append(spaces, s.cloudSpace(&clouds[i]))
			}
		}
	}
	sizes, pending := map[string]spaceSize{}, false
	for _, sp := range spaces {
		u, known, stale := s.dirUsage(sp, usageWriteGap, false)
		var size spaceSize
		if known {
			size.Files, size.Size = &u.files, &u.size
		}
		size.Free, size.Total = diskFree(sp.Root)
		sizes[sp.ID] = size
		pending = pending || stale
	}
	writeJSON(w, http.StatusOK, map[string]any{"sizes": sizes, "pending": pending})
}
