package clouds

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Native Google Docs, Sheets and Slides are not files on Google's side, so the two-way sync
// leaves them to this: they arrive here as .docx/.xlsx/.pptx, and an edit made here is uploaded
// INTO the same Doc (Google converts it; the Doc keeps its link, sharing and version history).

var GoogleAPI = "https://www.googleapis.com"

// GoogleDocMimes are the native kinds and the Office format each travels as.
var GoogleDocMimes = map[string]string{
	"application/vnd.google-apps.document":     "docx",
	"application/vnd.google-apps.spreadsheet":  "xlsx",
	"application/vnd.google-apps.presentation": "pptx",
}

var officeMime = map[string]string{
	"docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	"xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	"pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
}

// DriveAPI calls Google Drive as the person, refreshing the sign-in when needed.
type DriveAPI struct {
	App     *OAuthApp
	HTTP    *http.Client
	Token   *Token
	OnToken func(*Token) // a refreshed token, to keep
}

func (d *DriveAPI) access(ctx context.Context) (string, error) {
	if d.Token == nil {
		return "", &AuthError{Msg: "no Google sign-in stored — connect the drive again"}
	}
	if d.Token.AccessToken != "" && (d.Token.Expiry.IsZero() || time.Until(d.Token.Expiry) > time.Minute) {
		return d.Token.AccessToken, nil
	}
	if d.App == nil || d.Token.RefreshToken == "" {
		return "", &AuthError{Msg: "the Google sign-in expired — connect the drive again"}
	}
	t, err := d.App.Refresh(ctx, d.http(), d.Token.RefreshToken)
	if err != nil {
		return "", err
	}
	d.Token = t
	if d.OnToken != nil {
		d.OnToken(t)
	}
	return t.AccessToken, nil
}

func (d *DriveAPI) http() *http.Client {
	if d.HTTP != nil {
		return d.HTTP
	}
	return &http.Client{Timeout: 5 * time.Minute}
}

func (d *DriveAPI) do(ctx context.Context, method, target, ctype string, body io.Reader, out any) error {
	tok, err := d.access(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	res, err := d.http().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode == 401 || (res.StatusCode == 403 && bytes.Contains(b, []byte("insufficient"))) {
		return &AuthError{Msg: "Google refused the change (" + res.Status + "): connect the drive again with write access"}
	}
	if res.StatusCode >= 300 {
		return fmt.Errorf("Google Drive: %s %s", res.Status, strings.TrimSpace(string(b)))
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

// UploadInto replaces a native Doc's content with a local Office file; returns its new modified time.
func (d *DriveAPI) UploadInto(ctx context.Context, fileID, local, format string) (time.Time, error) {
	f, err := os.Open(local)
	if err != nil {
		return time.Time{}, err
	}
	defer f.Close()
	var out struct {
		ModifiedTime time.Time `json:"modifiedTime"`
	}
	target := GoogleAPI + "/upload/drive/v3/files/" + fileID + "?uploadType=media&fields=modifiedTime&supportsAllDrives=true"
	if err := d.do(ctx, http.MethodPatch, target, officeMime[format], f, &out); err != nil {
		return time.Time{}, err
	}
	return out.ModifiedTime, nil
}

// Trash moves a file to Google's trash (restorable there for 30 days).
func (d *DriveAPI) Trash(ctx context.Context, fileID string) error {
	return d.do(ctx, http.MethodPatch, GoogleAPI+"/drive/v3/files/"+fileID+"?supportsAllDrives=true", "application/json",
		strings.NewReader(`{"trashed":true}`), nil)
}
