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
// This is how Pocket Office, Pocket Documents and Pocket Collaborator keep
// files here instead of in Nextcloud: they speak WebDAV to /dav/ with the
// link token as the password (or a bearer token).

var linkOffers = []core.Offer{
	{Scope: "files:read", Name: "Open your files", Description: "List your folders and read your files, over WebDAV or the API (Pocket Office opens documents kept here)."},
	{Scope: "files:write", Name: "Save your files", Description: "Upload, change, move and delete files in your folders (Pocket Office saves documents back, Pocket Documents files your archive)."},
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
	{"GET", regexp.MustCompile(`^/api/v1/(spaces|stars|fs/(list|stat|raw|zip|search|thumb|usage))$`), "files:read"},
	{"PUT", regexp.MustCompile(`^/api/v1/fs/upload$`), "files:write"},
	{"POST", regexp.MustCompile(`^/api/v1/fs/(mkdir|move|copy|rename|delete|text)$`), "files:write"},
}

var errLinkScope = errors.New("outside the link's scope")

func linkAllows(scopes []string, r *http.Request) bool {
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
