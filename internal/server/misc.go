package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/abjelosevic88/pocket-homelab-files/internal/notify"
)

// ---------- rate limits for the public pages ----------

// limiter allows each address a steady rate of public requests and counts
// wrong passwords per link: ten wrong tries lock that link for that address
// for 15 minutes.
type limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	fails   map[string][]time.Time
}

type bucket struct {
	tokens float64
	at     time.Time
}

func newLimiter() *limiter {
	l := &limiter{buckets: map[string]*bucket{}, fails: map[string][]time.Time{}}
	go func() {
		for range time.Tick(10 * time.Minute) {
			l.mu.Lock()
			for k, b := range l.buckets {
				if time.Since(b.at) > 10*time.Minute {
					delete(l.buckets, k)
				}
			}
			for k, v := range l.fails {
				if len(v) == 0 || time.Since(v[len(v)-1]) > 15*time.Minute {
					delete(l.fails, k)
				}
			}
			l.mu.Unlock()
		}
	}()
	return l
}

const (
	publicRate  = 10.0 // requests per second, refilled
	publicBurst = 300.0
)

func (l *limiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[ip]
	now := time.Now()
	if b == nil {
		b = &bucket{tokens: publicBurst, at: now}
		l.buckets[ip] = b
	}
	b.tokens = min(publicBurst, b.tokens+now.Sub(b.at).Seconds()*publicRate)
	b.at = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (l *limiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.allow(clientIP(r)) {
			writeErr(w, http.StatusTooManyRequests, "slow down")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (l *limiter) recent(key string) []time.Time {
	cut := time.Now().Add(-15 * time.Minute)
	v := l.fails[key]
	i := 0
	for i < len(v) && v[i].Before(cut) {
		i++
	}
	l.fails[key] = v[i:]
	return l.fails[key]
}

func (l *limiter) allowAttempt(what, ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(what+"|"+ip)) < 10 && len(l.recent(what)) < 50
}

func (l *limiter) fail(what, ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.fails[what+"|"+ip] = append(l.recent(what+"|"+ip), now)
	l.fails[what] = append(l.recent(what), now)
}

// clientIP is the caller's address (RealIP has already applied proxy headers).
func clientIP(r *http.Request) string {
	if ip := r.Header.Get("Cf-Connecting-Ip"); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ---------- notifications ----------

// notifyQueue gathers events per key for a minute, so twenty files dropped
// through one upload link make one message.
type notifyQueue struct {
	s     *Server
	mu    sync.Mutex
	items map[string]*pendingNote
}

type pendingNote struct {
	uid         int64
	kind        string
	title, body string
	link        string
	count       int
	last        time.Time
}

func newNotifyQueue(s *Server) *notifyQueue {
	return &notifyQueue{s: s, items: map[string]*pendingNote{}}
}

func (q *notifyQueue) add(uid int64, kind, key, title, body, link string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	k := fmt.Sprintf("%d|%s", uid, key)
	n := q.items[k]
	if n == nil {
		n = &pendingNote{uid: uid, kind: kind, title: title, link: link}
		q.items[k] = n
	}
	n.body = body
	n.count++
	n.last = time.Now()
}

func (q *notifyQueue) run(ctx context.Context) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		var due []*pendingNote
		q.mu.Lock()
		for k, n := range q.items {
			if time.Since(n.last) > 45*time.Second {
				due = append(due, n)
				delete(q.items, k)
			}
		}
		q.mu.Unlock()
		for _, n := range due {
			body := n.body
			if strings.Contains(body, "%d") {
				what := "1 file"
				if n.count != 1 {
					what = fmt.Sprintf("%d files", n.count)
				}
				body = strings.Replace(body, "%d", what, 1)
			} else if n.count > 1 {
				body += fmt.Sprintf(" (%d times)", n.count)
			}
			if err := q.s.notifyUser(ctx, n.uid, n.kind, n.title, body, n.link); err != nil {
				q.s.log.Debug("notify", "err", err)
			}
		}
	}
}

// notifyUser sends through the person's own channel (Settings → Notifications).
func (s *Server) notifyUser(ctx context.Context, uid int64, kind, title, body, link string) error {
	st, err := s.store.GetSettings(ctx, uid)
	if err != nil {
		return err
	}
	if (kind == "sends" && !st.NotifySends) || (kind == "uploads" && !st.NotifyUploads) {
		return nil
	}
	url, k := st.NotifyURL, st.NotifyKind
	if url == "" && s.isOwner(ctx, uid) {
		url, k = s.cfg.NotifyURL, s.cfg.NotifyKind
	}
	if url == "" {
		return nil
	}
	return notify.Send(ctx, k, url, st.NotifyToken, title, body, link)
}

// isOwner reports whether uid is the account the box was set up with.
func (s *Server) isOwner(ctx context.Context, uid int64) bool {
	return uid > 0 && uid == s.store.OwnerID(ctx)
}
