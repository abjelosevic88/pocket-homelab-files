package server

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-files/internal/store"
)

// Pocket Core app links (docs/links.md in pocket-homelab-core): another
// Pocket app may act here for a person with a pcx_ token, only within these
// scopes. Every route not listed in linkRoutes stays closed to it.
//
// This is how Pocket Office, Pocket Documents and Pocket Review keep
// files here instead of in Nextcloud: they speak WebDAV to /dav/ with the
// link token as the password (or a bearer token).

var linkOffers = []core.Offer{
	{Scope: "files:read", Name: "Open your files", Description: "List, search and read your files, over WebDAV or the API (Pocket Office opens documents kept here, Pocket Search finds them beside the web)."},
	{Scope: "files:write", Name: "Save your files", Description: "Upload, change, move and delete files in your folders (Pocket Office saves documents back, Pocket Documents files your archive)."},
}

// linkUses: Pocket Office edits the documents kept here ("Open in Pocket Office").
var linkUses = []core.Use{
	{App: "office", Scopes: []string{"office:open"}, Reason: "Offer “Open in Pocket Office” on Word, Excel and PowerPoint files, once Pocket Office can reach your files"},
}

// capabilities are what Pocket Search and Pocket Hub may ask here for a
// person (docs/capabilities.md in pocket-homelab-core): one value for the
// manifest, the routes and linkAllows. The answers are in capabilities.go.
//
// No day: the app keeps no record of what happened. The index knows each
// file's own date, and an upload keeps the date the file had on the device
// it came from, so "added that day" has no honest answer. No capture: a file
// needs a name and a folder, which is a form.
var capabilities = core.Capabilities{
	Search: &core.Searchable{What: "Your files and folders, by name and by the text inside documents", Kinds: []string{"file", "folder"}, Bangs: []string{"files", "file"}},
	Widget: &core.Widget{Title: "Recent files", What: "The files changed last in My files, your drives and the folders shared with you, and how much My files holds"},
}

type linkRoute struct {
	methods string
	path    *regexp.Regexp
	scope   string
}

var davPath = `^/(dav|remote\.php/webdav|remote\.php/dav/files/[^/]+)(/.*)?$`

var linkRoutes = []linkRoute{
	{"GET", regexp.MustCompile(`^/api/v1/auth/me$`), ""},
	{"GET HEAD OPTIONS PROPFIND", regexp.MustCompile(davPath), "files:read"},
	{"PUT DELETE MKCOL MOVE COPY PROPPATCH LOCK UNLOCK", regexp.MustCompile(davPath), "files:write"},
	// search = the global names-and-contents search (Pocket Search shows your files beside the web)
	{"GET", regexp.MustCompile(`^/api/v1/(spaces|stars|search|fs/(list|stat|raw|zip|search|thumb|usage))$`), "files:read"},
	{"PUT", regexp.MustCompile(`^/api/v1/fs/upload$`), "files:write"},
	{"POST", regexp.MustCompile(`^/api/v1/fs/(mkdir|move|copy|rename|delete|text)$`), "files:write"},
}

var errLinkScope = errors.New("outside the link's scope")

func linkAllows(scopes []string, r *http.Request) bool {
	if portable.LinkAllows(scopes, r) || capabilities.LinkAllows(scopes, r) {
		return true
	}
	for _, lr := range linkRoutes {
		if !strings.Contains(" "+lr.methods+" ", " "+r.Method+" ") || !lr.path.MatchString(r.URL.Path) {
			continue
		}
		if lr.scope == "" {
			return true
		}
		for _, sc := range scopes {
			if sc == lr.scope {
				return true
			}
		}
	}
	return false
}

// linkUser validates a pcx_ token and maps it to the person it acts for.
func (s *Server) linkUser(ctx context.Context, tok string, r *http.Request) (*store.User, error) {
	in, err := s.core.Introspect(ctx, tok)
	if err != nil {
		return nil, err
	}
	if !in.Active || !in.Delegated() || in.User == nil || in.User.Email == "" {
		return nil, http.ErrNoCookie
	}
	if !linkAllows(strings.Fields(in.Scope), r) {
		return nil, errLinkScope
	}
	return s.coreIdentityUser(ctx, in)
}
