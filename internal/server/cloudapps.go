package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-files/internal/clouds"
)

// The sign-in apps (OAuth clients) of Google Drive, Dropbox and OneDrive: one per box, set by an
// admin in Settings → Connected drives (or, as before, in the environment). Values saved in the
// app win over the environment; the secret is sealed like everything else that signs in.

type cloudAppCreds struct {
	ID, Secret, Tenant, Source string // Source: settings | env | ""
}

var cloudAppKinds = []string{"gdrive", "dropbox", "onedrive"}

func cloudAppKey(kind, field string) string { return "cloud_app." + kind + "." + field }

// cloudAppFor is the client a provider signs in with: Settings first, then the environment.
func (s *Server) cloudAppFor(ctx context.Context, kind string) cloudAppCreds {
	if id := s.store.BoxValue(ctx, cloudAppKey(kind, "id")); id != "" {
		secret, _ := s.unseal(s.store.BoxValue(ctx, cloudAppKey(kind, "secret")))
		return cloudAppCreds{ID: id, Secret: secret, Tenant: s.store.BoxValue(ctx, cloudAppKey(kind, "tenant")), Source: "settings"}
	}
	c := s.cfg
	switch kind {
	case "gdrive":
		if c.GoogleClientID != "" {
			return cloudAppCreds{ID: c.GoogleClientID, Secret: c.GoogleClientSecret, Source: "env"}
		}
	case "dropbox":
		if c.DropboxAppKey != "" {
			return cloudAppCreds{ID: c.DropboxAppKey, Secret: c.DropboxAppSecret, Source: "env"}
		}
	case "onedrive":
		if c.MicrosoftClientID != "" {
			return cloudAppCreds{ID: c.MicrosoftClientID, Secret: c.MicrosoftClientSecret, Tenant: c.MicrosoftTenant, Source: "env"}
		}
	}
	return cloudAppCreds{}
}

func (cr cloudAppCreds) ready() bool { return cr.ID != "" && cr.Secret != "" }

func appFromCreds(kind string, cr cloudAppCreds, write bool) *clouds.OAuthApp {
	if !cr.ready() {
		return nil
	}
	switch kind {
	case "gdrive":
		return clouds.GoogleApp(cr.ID, cr.Secret, write)
	case "dropbox":
		return clouds.DropboxApp(cr.ID, cr.Secret, write)
	case "onedrive":
		return clouds.MicrosoftApp(cr.ID, cr.Secret, cr.Tenant, write)
	}
	return nil
}

type cloudAppView struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Configured bool   `json:"configured"`
	Source     string `json:"source,omitempty"` // settings | env
	ClientID   string `json:"client_id,omitempty"`
	SecretSet  bool   `json:"secret_set"`
	Tenant     string `json:"tenant,omitempty"`
	Redirect   string `json:"redirect"`
}

func (s *Server) cloudAppView(ctx context.Context, kind string) cloudAppView {
	cr := s.cloudAppFor(ctx, kind)
	return cloudAppView{Kind: kind, Name: providerName(kind), Configured: cr.ready(), Source: cr.Source, ClientID: cr.ID,
		SecretSet: cr.Secret != "", Tenant: cr.Tenant, Redirect: s.cloudRedirect(kind)}
}

// GET /clouds/apps (admins): the three sign-in apps and the address each must allow.
func (s *Server) handleCloudApps(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	out := []cloudAppView{}
	for _, k := range cloudAppKinds {
		out = append(out, s.cloudAppView(r.Context(), k))
	}
	writeJSON(w, http.StatusOK, out)
}

func validAppKind(kind string) bool {
	for _, k := range cloudAppKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// PUT /clouds/apps/{kind} (admins): {client_id, client_secret ("" keeps the saved one), tenant}.
// The pair is checked with the provider first; a pair it refuses is not saved.
func (s *Server) handleSaveCloudApp(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	kind := chi.URLParam(r, "kind")
	if !validAppKind(kind) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var in struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		Tenant       string `json:"tenant"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	in.ClientID, in.ClientSecret, in.Tenant = strings.TrimSpace(in.ClientID), strings.TrimSpace(in.ClientSecret), strings.TrimSpace(in.Tenant)
	if in.ClientID == "" {
		writeErr(w, http.StatusBadRequest, "the client ID (app key) is needed")
		return
	}
	cur := s.cloudAppFor(ctx, kind)
	if in.ClientSecret == "" && cur.Source == "settings" && cur.ID == in.ClientID {
		in.ClientSecret = cur.Secret
	}
	if in.ClientSecret == "" {
		writeErr(w, http.StatusBadRequest, "the client secret (app secret) is needed")
		return
	}
	cr := cloudAppCreds{ID: in.ClientID, Secret: in.ClientSecret, Tenant: in.Tenant}
	if ok, msg := s.checkCloudApp(ctx, kind, cr); !ok {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	sealed, err := s.seal(in.ClientSecret)
	if err != nil {
		s.fail(w, err, "seal")
		return
	}
	for field, v := range map[string]string{"id": in.ClientID, "secret": sealed, "tenant": in.Tenant} {
		if err := s.store.SetBoxValue(ctx, cloudAppKey(kind, field), v); err != nil {
			s.fail(w, err, "save")
			return
		}
	}
	writeJSON(w, http.StatusOK, s.cloudAppView(ctx, kind))
}

// DELETE /clouds/apps/{kind} (admins): forget the saved app (the environment's, if any, applies again).
// Drives already connected keep their sign-in but cannot renew it until an app is set again.
func (s *Server) handleDeleteCloudApp(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	kind := chi.URLParam(r, "kind")
	if !validAppKind(kind) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	for _, f := range []string{"id", "secret", "tenant"} {
		_ = s.store.SetBoxValue(r.Context(), cloudAppKey(kind, f), "")
	}
	writeJSON(w, http.StatusOK, s.cloudAppView(r.Context(), kind))
}

// checkCloudApp asks the provider's token endpoint to trade a made-up code. A wrong client
// is refused as such ("invalid_client"); a right one only complains about the code.
func (s *Server) checkCloudApp(ctx context.Context, kind string, cr cloudAppCreds) (bool, string) {
	app := appFromCreds(kind, cr, false)
	if app == nil {
		return false, "the client ID and secret are both needed"
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {"pocket-files-check"}, "redirect_uri": {s.cloudRedirect(kind)},
		"client_id": {cr.ID}, "client_secret": {cr.Secret}}
	tokenURL := app.TokenURL
	if kind == "onedrive" {
		// Microsoft looks at a made-up code before the client, so ask for an app-only token
		// instead: the personal-accounts endpoint says plainly when the app or secret is wrong.
		tenant := cr.Tenant
		if tenant == "" || tenant == "common" || tenant == "organizations" {
			tenant = "consumers"
		}
		tokenURL = "https://login.microsoftonline.com/" + url.PathEscape(tenant) + "/oauth2/v2.0/token"
		form = url.Values{"grant_type": {"client_credentials"}, "scope": {"https://graph.microsoft.com/.default"},
			"client_id": {cr.ID}, "client_secret": {cr.Secret}}
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, providerName(kind) + " could not be reached: " + err.Error()
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	var out struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	_ = json.Unmarshal(body, &out)
	return judgeCheck(kind, out.Error, out.Description, string(body))
}

// judgeCheck reads the provider's refusal of the made-up code.
func judgeCheck(kind, code, desc, raw string) (bool, string) {
	all := strings.ToLower(code + " " + desc + " " + raw)
	switch {
	case strings.Contains(all, "invalid_client"), strings.Contains(all, "unauthorized_client"),
		strings.Contains(all, "aadsts7000215"), strings.Contains(all, "aadsts700016"), strings.Contains(all, "aadsts7000222"):
		msg := firstNonEmpty(desc, code)
		if i := strings.Index(msg, "Trace ID"); i > 0 {
			msg = strings.TrimSpace(msg[:i])
		}
		return false, providerName(kind) + " does not accept this client ID and secret: " + msg
	case strings.Contains(all, "redirect_uri") || strings.Contains(all, "aadsts50011"):
		return false, providerName(kind) + " does not allow the redirect address yet: add it to the app exactly as shown"
	case strings.Contains(all, "invalid_grant"), strings.Contains(all, "aadsts9002313"), strings.Contains(all, "aadsts70000"),
		strings.Contains(all, "code doesn't exist"), strings.Contains(all, "invalid code"):
		return true, ""
	case kind == "onedrive" && strings.Contains(all, "aadsts"):
		// Microsoft found the app and took the secret; it refuses the app-only token for other reasons.
		return true, ""
	}
	return false, providerName(kind) + " answered unexpectedly: " + firstNonEmpty(desc, code, strings.TrimSpace(raw))
}
