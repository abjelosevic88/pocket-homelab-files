package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-files/internal/search"
	"github.com/abjelosevic88/pocket-homelab-files/internal/vfs"
)

// What Pocket Search and Pocket Hub ask every app (capabilities in links.go;
// docs/capabilities.md in pocket-homelab-core). Both run as the request's
// person and read the same index through the same places as GET /search
// (searchScopes): a person only ever gets what they can open. Neither reads
// the disk or changes anything.

var errNoIndex = errors.New("the search index is not available")

// hitMarks are what the index puts around the words it found in a document.
var hitMarks = strings.NewReplacer("\ue000", "", "\ue001", "")

// routePart escapes one part of an address the way the web app does
// (encodeURIComponent): a name with a space, "#", "?" or "%" stays one part.
func routePart(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }

// fileURL is where the web app shows a folder (/f/<space>/<path>) or opens a
// file: in its folder, with ?open=<name>, as the app's own search and the
// Starred page do (filesRoute in web/src/lib/api.ts).
func fileURL(space, p string, isDir bool) string {
	p = vfs.Clean(p)
	open := ""
	if !isDir {
		open = "?open=" + routePart(path.Base(p))
		p = path.Dir(p)
	}
	out := "/f/" + routePart(space)
	for _, part := range strings.Split(p, "/") {
		if part != "" {
			out += "/" + routePart(part)
		}
	}
	return out + open
}

// fileID names a file or folder across the person's places: "home:/Docs/a.pdf",
// "share:7:/plan.md". It stays until the file is moved or renamed.
func fileID(space, p string) string { return space + ":" + p }

// placeNames are the places a person can open, as the sidebar names them.
func (s *Server) placeNames(ctx context.Context, uid int64) map[string]string {
	out := map[string]string{"home": s.homeSpace(uid).Name}
	if mounts, err := s.store.MountsFor(ctx, uid); err == nil {
		for _, m := range mounts {
			out["mount:"+strconv.FormatInt(m.ID, 10)] = m.Name
		}
	}
	if clouds, err := s.store.CloudsFor(ctx, uid); err == nil {
		for _, c := range clouds {
			if ownSpace(&c) {
				out["cloud:"+strconv.FormatInt(c.ID, 10)] = c.Name
			}
		}
	}
	if shares, err := s.store.SharesWith(ctx, uid); err == nil {
		for i := range shares {
			sp := s.shareSpace(&shares[i])
			out[sp.ID] = sp.Name
		}
	}
	return out
}

// ---- search ----

// pocketSearch is the app's own search everywhere (GET /search): the same
// words and filters ("exact phrase", -without, ext:pdf, type:folder), the
// same places, the same order: names first, then the text inside documents.
func (s *Server) pocketSearch(r *http.Request, q string, limit int) ([]core.SearchItem, error) {
	if s.search == nil {
		return nil, errNoIndex
	}
	ctx, uid := r.Context(), me(r)
	hits, err := s.search.Search(ctx, search.Parse(q), s.searchScopes(ctx, uid), limit)
	if err != nil {
		return nil, err
	}
	var places map[string]string
	items := make([]core.SearchItem, 0, len(hits))
	for _, h := range hits {
		it := core.SearchItem{ID: fileID(h.Space, h.Path), Title: h.Name, Kind: "file", Snippet: hitMarks.Replace(h.Snippet),
			Date: h.Modified.UTC().Format(time.RFC3339), URL: fileURL(h.Space, h.Path, h.IsDir)}
		if h.IsDir {
			it.Kind = "folder"
		}
		// Found by its name: say where it is, as the app's search does ("My files/Docs").
		if it.Snippet == "" {
			if places == nil {
				places = s.placeNames(ctx, uid)
			}
			it.Snippet = places[h.Space] + strings.TrimSuffix(h.Folder, "/")
		}
		items = append(items, it)
	}
	return items, nil
}

// ---- widget ----

const (
	widgetFiles = 6  // rows of the widget
	widgetLook  = 30 // how many of the newest files are read to count today's
)

// pocketWidget answers the files changed last and how many of them changed
// today, with what My files holds. The files are what the app's search lists
// for "type:file" (newest first), without the server folders: other programs
// fill those. The totals are the ones the app last counted for its sidebar;
// nothing is counted here.
func (s *Server) pocketWidget(r *http.Request) (*core.WidgetData, error) {
	ctx, uid := r.Context(), me(r)
	out := &core.WidgetData{URL: fileURL("home", "/", true)}
	if s.search != nil {
		var scopes []search.Scope
		for _, sc := range s.searchScopes(ctx, uid) {
			if !strings.HasPrefix(sc.Space, "mount:") {
				scopes = append(scopes, sc)
			}
		}
		hits, err := s.search.Search(ctx, search.Query{Type: "file"}, scopes, widgetLook+1)
		if err != nil {
			return nil, err
		}
		now := time.Now().In(s.loc(ctx, uid)) // "today" is the person's day
		var places map[string]string
		today := 0
		for _, h := range hits {
			day := dayWord(h.Modified, now)
			if day == "today" {
				today++
			}
			if len(out.Items) == widgetFiles {
				continue
			}
			// The folder it is in; at the top of a place, the place.
			where := path.Base(h.Folder)
			if h.Folder == "/" {
				if places == nil {
					places = s.placeNames(ctx, uid)
				}
				where = places[h.Space]
			}
			if where != "" {
				day += " · " + where
			}
			out.Items = append(out.Items, core.WidgetItem{ID: fileID(h.Space, h.Path), Title: h.Name, Detail: day, URL: fileURL(h.Space, h.Path, false)})
		}
		switch {
		case today > widgetLook:
			out.Line = "More than " + strconv.Itoa(widgetLook) + " files changed today"
		case today == 1:
			out.Line = "1 file changed today"
		case today > 1:
			out.Line = strconv.Itoa(today) + " files changed today"
		}
	}
	if u, ok := s.knownUsage(s.homeSpace(uid)); ok && u.files > 0 {
		out.Metrics = []core.Metric{{Label: "Files", Value: thousands(u.files)}, {Label: "Space used", Value: sizeWords(u.size)}}
	}
	if len(out.Items) == 0 && len(out.Metrics) == 0 {
		return nil, nil
	}
	return out, nil
}

// dayWord says which day t was, seen from now: "today", "yesterday",
// "Monday" within a week, then "12 Nov" and "12 Nov 2025".
func dayWord(t, now time.Time) string {
	t = t.In(now.Location())
	midnight := func(x time.Time) time.Time {
		return time.Date(x.Year(), x.Month(), x.Day(), 0, 0, 0, 0, now.Location())
	}
	days := int(midnight(now).Sub(midnight(t)).Hours()/24 + 0.5)
	switch {
	case days <= 0:
		return "today"
	case days == 1:
		return "yesterday"
	case days < 7:
		return t.Format("Monday")
	case t.Year() == now.Year():
		return t.Format("2 Jan")
	}
	return t.Format("2 Jan 2006")
}

// thousands writes 1204 as "1,204".
func thousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// sizeWords writes bytes as the web app does (bytes in web/src/lib/format.ts): "3.20 GB".
func sizeWords(n int64) string {
	f := float64(n)
	switch {
	case n < 1024:
		return strconv.FormatInt(n, 10) + " B"
	case n < 1<<20:
		return strconv.FormatFloat(f/(1<<10), 'f', 0, 64) + " KB"
	case n < 1<<30:
		return strconv.FormatFloat(f/(1<<20), 'f', 1, 64) + " MB"
	case n < 1<<40:
		return strconv.FormatFloat(f/(1<<30), 'f', 2, 64) + " GB"
	}
	return strconv.FormatFloat(f/(1<<40), 'f', 2, 64) + " TB"
}
