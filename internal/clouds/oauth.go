package clouds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrReconnect marks failures only a new sign-in fixes.
var ErrReconnect = errors.New("connect the drive again")

// AuthError is a sign-in the cloud no longer accepts.
type AuthError struct{ Msg string }

func (e *AuthError) Error() string { return e.Msg }
func (e *AuthError) Unwrap() error { return ErrReconnect }

// Token is the OAuth token rclone keeps in a remote's `token` option.
type Token struct {
	AccessToken  string    `json:"access_token"`
	TokenType    string    `json:"token_type"`
	RefreshToken string    `json:"refresh_token"`
	Expiry       time.Time `json:"expiry"`
}

// JSON is the token as rclone writes it.
func (t *Token) JSON() string {
	if t.TokenType == "" {
		t.TokenType = "Bearer"
	}
	b, _ := json.Marshal(t)
	return string(b)
}

// OAuthApp is one provider's OAuth endpoints plus the box owner's client registration.
type OAuthApp struct {
	Kind         string
	AuthURL      string
	TokenURL     string
	Scopes       string
	ClientID     string
	ClientSecret string
	Extra        url.Values // provider-specific authorize parameters
}

// Endpoints of the OAuth clouds; tests point them at fakes.
var Endpoints = struct {
	GoogleAuth, GoogleToken, GoogleUser string
	MSAuth, MSToken, Graph              string
	DropboxAuth, DropboxToken, Dropbox  string
}{
	GoogleAuth: "https://accounts.google.com/o/oauth2/v2/auth", GoogleToken: "https://oauth2.googleapis.com/token",
	GoogleUser: "https://openidconnect.googleapis.com/v1/userinfo",
	MSAuth:     "https://login.microsoftonline.com/%s/oauth2/v2.0/authorize", MSToken: "https://login.microsoftonline.com/%s/oauth2/v2.0/token",
	Graph:       "https://graph.microsoft.com/v1.0",
	DropboxAuth: "https://www.dropbox.com/oauth2/authorize", DropboxToken: "https://api.dropboxapi.com/oauth2/token",
	Dropbox: "https://api.dropboxapi.com/2",
}

// Read-only scopes for one-way drives; write access only for two-way ones.

func GoogleApp(id, secret string, write bool) *OAuthApp {
	scope := "https://www.googleapis.com/auth/drive.readonly"
	if write {
		scope = "https://www.googleapis.com/auth/drive"
	}
	return &OAuthApp{Kind: "gdrive", AuthURL: Endpoints.GoogleAuth, TokenURL: Endpoints.GoogleToken, ClientID: id, ClientSecret: secret,
		Scopes: "openid email " + scope,
		Extra:  url.Values{"access_type": {"offline"}, "prompt": {"consent"}, "include_granted_scopes": {"true"}}}
}

func MicrosoftApp(id, secret, tenant string, write bool) *OAuthApp {
	if tenant == "" {
		tenant = "common"
	}
	scopes := "offline_access User.Read Files.Read Files.Read.All"
	if write {
		scopes = "offline_access User.Read Files.ReadWrite Files.ReadWrite.All"
	}
	return &OAuthApp{Kind: "onedrive", AuthURL: fmt.Sprintf(Endpoints.MSAuth, tenant), TokenURL: fmt.Sprintf(Endpoints.MSToken, tenant),
		ClientID: id, ClientSecret: secret, Scopes: scopes, Extra: url.Values{"prompt": {"select_account"}}}
}

func DropboxApp(key, secret string, write bool) *OAuthApp {
	scopes := "account_info.read files.metadata.read files.content.read"
	if write {
		scopes += " files.content.write"
	}
	return &OAuthApp{Kind: "dropbox", AuthURL: Endpoints.DropboxAuth, TokenURL: Endpoints.DropboxToken, ClientID: key, ClientSecret: secret,
		Scopes: scopes, Extra: url.Values{"token_access_type": {"offline"}}}
}

// AuthCodeURL is where the browser goes to sign in.
func (a *OAuthApp) AuthCodeURL(redirect, state string) string {
	q := url.Values{"client_id": {a.ClientID}, "redirect_uri": {redirect}, "response_type": {"code"}, "scope": {a.Scopes}, "state": {state}}
	for k, v := range a.Extra {
		q[k] = v
	}
	return a.AuthURL + "?" + q.Encode()
}

func (a *OAuthApp) Exchange(ctx context.Context, hc *http.Client, code, redirect string) (*Token, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect},
		"client_id": {a.ClientID}, "client_secret": {a.ClientSecret}}
	req, err := http.NewRequestWithContext(ctx, "POST", a.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Error        string `json:"error"`
		Description  string `json:"error_description"`
	}
	_ = json.Unmarshal(body, &out)
	if res.StatusCode != 200 || out.AccessToken == "" {
		return nil, fmt.Errorf("sign-in failed: %s", firstNonEmpty(out.Description, out.Error, res.Status))
	}
	if out.RefreshToken == "" {
		return nil, errors.New("the cloud gave no refresh token, so the copy could not keep running; remove this app's access in your account settings and connect again")
	}
	t := &Token{AccessToken: out.AccessToken, RefreshToken: out.RefreshToken, TokenType: "Bearer"}
	if out.ExpiresIn > 0 {
		t.Expiry = time.Now().Add(time.Duration(out.ExpiresIn) * time.Second)
	}
	return t, nil
}

// Account asks the cloud who signed in (shown next to the drive), plus anything rclone needs
// (OneDrive's drive id and type).
func (a *OAuthApp) Account(ctx context.Context, hc *http.Client, t *Token) (login string, extra map[string]string, err error) {
	extra = map[string]string{}
	switch a.Kind {
	case "gdrive":
		var u struct {
			Email string `json:"email"`
		}
		err = getJSON(ctx, hc, "GET", Endpoints.GoogleUser, t.AccessToken, &u)
		return u.Email, extra, err
	case "onedrive":
		var me struct {
			Mail string `json:"mail"`
			UPN  string `json:"userPrincipalName"`
		}
		if err = getJSON(ctx, hc, "GET", Endpoints.Graph+"/me", t.AccessToken, &me); err != nil {
			return "", nil, err
		}
		var d struct {
			ID   string `json:"id"`
			Type string `json:"driveType"`
		}
		if err = getJSON(ctx, hc, "GET", Endpoints.Graph+"/me/drive", t.AccessToken, &d); err != nil {
			return "", nil, err
		}
		extra["drive_id"], extra["drive_type"] = d.ID, d.Type
		return firstNonEmpty(me.Mail, me.UPN), extra, nil
	case "dropbox":
		var acc struct {
			Email string `json:"email"`
		}
		err = getJSON(ctx, hc, "POST", Endpoints.Dropbox+"/users/get_current_account", t.AccessToken, &acc)
		return acc.Email, extra, err
	}
	return "", extra, nil
}

func getJSON(ctx context.Context, hc *http.Client, method, target, token string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	res, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return fmt.Errorf("%s %s: %s %s", method, target, res.Status, strings.TrimSpace(string(b)))
	}
	return json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(out)
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// Refresh trades a refresh token for a new access token (keeping the refresh token when the
// provider does not send a new one).
func (a *OAuthApp) Refresh(ctx context.Context, hc *http.Client, refresh string) (*Token, error) {
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {a.ClientID}, "client_secret": {a.ClientSecret}}
	req, err := http.NewRequestWithContext(ctx, "POST", a.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Error        string `json:"error"`
		Description  string `json:"error_description"`
	}
	_ = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&out)
	if res.StatusCode != 200 || out.AccessToken == "" {
		if out.Error == "invalid_grant" {
			return nil, &AuthError{Msg: "the cloud no longer accepts this sign-in — connect the drive again"}
		}
		return nil, fmt.Errorf("token refresh failed: %s", firstNonEmpty(out.Description, out.Error, res.Status))
	}
	t := &Token{AccessToken: out.AccessToken, RefreshToken: firstNonEmpty(out.RefreshToken, refresh), TokenType: "Bearer"}
	if out.ExpiresIn > 0 {
		t.Expiry = time.Now().Add(time.Duration(out.ExpiresIn) * time.Second)
	}
	return t, nil
}

// ParseToken reads the token rclone keeps in a remote's options.
func ParseToken(s string) (*Token, error) {
	var t Token
	if err := json.Unmarshal([]byte(s), &t); err != nil {
		return nil, err
	}
	return &t, nil
}
