package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-files/internal/clouds"
	"github.com/abjelosevic88/pocket-homelab-files/internal/store"
	"github.com/abjelosevic88/pocket-homelab-files/internal/vfs"
)

// Connected drives (docs/clouds.md). Each person connects their own; nobody else sees them.
// A drive is copied one way with rclone: either on a schedule into its own read-only space
// ("cloud:<id>"), or once into a folder of My files. Nothing is ever deleted or written on
// the cloud side, and a file deleted in the cloud stays here.

type cloudProvider struct {
	Kind       string       `json:"kind"`
	Name       string       `json:"name"`
	Auth       string       `json:"auth"` // oauth | form
	Configured bool         `json:"configured"`
	Fields     []cloudField `json:"fields,omitempty"`
	Help       string       `json:"help"`
}

type cloudField struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Secret      bool   `json:"secret,omitempty"`
	Multiline   bool   `json:"multiline,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
}

func (s *Server) cloudProviders(ctx context.Context) []cloudProvider {
	ready := func(kind string) bool { return s.cloudAppFor(ctx, kind).ready() }
	return []cloudProvider{
		{Kind: "gdrive", Name: "Google Drive", Auth: "oauth", Configured: ready("gdrive"),
			Help: "Read-only access. Google Docs, Sheets and Slides arrive as Word, Excel and PowerPoint files."},
		{Kind: "dropbox", Name: "Dropbox", Auth: "oauth", Configured: ready("dropbox"),
			Help: "Read-only access to your whole Dropbox."},
		{Kind: "onedrive", Name: "OneDrive", Auth: "oauth", Configured: ready("onedrive"),
			Help: "Read-only access to your OneDrive."},
		{Kind: "icloud", Name: "iCloud Drive", Auth: "form", Configured: true,
			Fields: []cloudField{{Key: "apple_id", Label: "Apple ID", Placeholder: "you@icloud.com"}, {Key: "password", Label: "Password", Secret: true}},
			Help:   "Apple sends a code to your devices next. Advanced Data Protection must be off and “Access iCloud Data on the Web” on. Apple asks for a new code about once a month."},
		{Kind: "rclone", Name: "Other (rclone remote)", Auth: "form", Configured: true,
			Fields: []cloudField{{Key: "config", Label: "rclone remote", Multiline: true, Placeholder: "[mybox]\ntype = sftp\nhost = …\nuser = …\npass = (rclone obscure …)"}},
			Help:   "Any of rclone's 70+ backends (S3, SFTP, WebDAV, pCloud, Mega, Box…). Paste one remote from `rclone config show`."},
	}
}

func (s *Server) oauthApp(ctx context.Context, kind string, write bool) *clouds.OAuthApp {
	return appFromCreds(kind, s.cloudAppFor(ctx, kind), write)
}

func providerName(kind string) string {
	switch kind {
	case "gdrive":
		return "Google Drive"
	case "dropbox":
		return "Dropbox"
	case "onedrive":
		return "OneDrive"
	case "icloud":
		return "iCloud Drive"
	}
	return "Cloud"
}

// ---------- options, folders, config ----------

// cloudOptions are a drive's plain settings.
type cloudOptions struct {
	SharedWithMe bool   `json:"shared_with_me,omitempty"` // Google Drive: also copy "Shared with me"
	Folder       string `json:"folder,omitempty"`         // one-time copies: the folder in My files
	Source       string `json:"source,omitempty"`         // only this folder of the cloud ("" = all of it)
	DestSpace    string `json:"dest_space,omitempty"`     // a folder the person picked: "home" or "mount:N"
	DestPath     string `json:"dest_path,omitempty"`
	Direction    string `json:"direction,omitempty"` // one_way (default) | two_way
	Conflict     string `json:"conflict,omitempty"`  // two-way: ask (default) | newer | cloud | pocket
	Write        bool   `json:"write,omitempty"`     // the sign-in has write access
	Ready        bool   `json:"ready,omitempty"`     // two-way: the first (union) run is done
}

// ownSpace reports whether the drive's copy is its own place ("cloud:<id>"): read-only one way,
// editable two way.
func ownSpace(c *store.Cloud) bool { return c.IntervalHours > 0 && optionsOf(c).DestSpace == "" }

// twoWay reports whether changes made here go back to the cloud.
func twoWay(c *store.Cloud) bool { return c.IntervalHours > 0 && optionsOf(c).Direction == "two_way" }

// writeless kinds sign in without scopes: what rclone can do is what the account can do.
func writeless(kind string) bool { return kind == "icloud" || kind == "rclone" }

func optionsOf(c *store.Cloud) cloudOptions {
	var o cloudOptions
	_ = json.Unmarshal([]byte(c.Options), &o)
	return o
}

func (o cloudOptions) json() string {
	b, _ := json.Marshal(o)
	return string(b)
}

// cloudDir is where a scheduled drive's copy lives.
func (s *Server) cloudDir(c *store.Cloud) string {
	return filepath.Join(s.cfg.CloudDir, strconv.FormatInt(c.UserID, 10), strconv.FormatInt(c.ID, 10))
}

// destOf is where the copy goes: a folder the person picked (in My files or a mount they can
// edit), the drive's own folder, or a new folder of My files for one-time copies. It returns
// the space too (nil for the drive's own folder) so the search index can be told.
func (s *Server) destOf(ctx context.Context, c *store.Cloud) (string, *vfs.Space, string, error) {
	o := optionsOf(c)
	if o.DestSpace != "" {
		sp, err := s.destSpace(ctx, c.UserID, o.DestSpace)
		if err != nil {
			return "", nil, "", err
		}
		rel := vfs.Clean(o.DestPath)
		return filepath.Join(sp.Root, filepath.FromSlash(rel)), sp, rel, nil
	}
	if c.IntervalHours == 0 && o.Folder != "" {
		home := s.homeSpace(c.UserID)
		rel := vfs.Clean(o.Folder)
		return filepath.Join(home.Root, filepath.FromSlash(rel)), home, rel, nil
	}
	return s.cloudDir(c), nil, "/", nil
}

// destSpace opens a place a drive may copy into: the person's home or a mount they can edit.
func (s *Server) destSpace(ctx context.Context, uid int64, id string) (*vfs.Space, error) {
	if id != "home" && !strings.HasPrefix(id, "mount:") {
		return nil, badRequest("a drive can copy into My files or a mount you can edit")
	}
	sp, err := s.space(ctx, uid, id)
	if err != nil {
		return nil, err
	}
	if !sp.Writable() {
		return nil, fmt.Errorf("%w: you cannot add files to %s", vfs.ErrForbidden, sp.Name)
	}
	return sp, nil
}

func (s *Server) cloudSpace(c *store.Cloud) *vfs.Space {
	dir := s.cloudDir(c)
	_ = os.MkdirAll(dir, 0o755)
	perm := vfs.View
	if twoWay(c) {
		perm = vfs.Edit
	}
	return &vfs.Space{ID: "cloud:" + strconv.FormatInt(c.ID, 10), Name: c.Name, Root: dir, Perm: perm}
}

// runtimeConfig is the remote as rclone needs it: the stored sign-in plus the box's OAuth client.
func (s *Server) runtimeConfig(c *store.Cloud) (clouds.Config, error) {
	plain, err := s.unseal(c.Config)
	if err != nil {
		return nil, err
	}
	cfg := clouds.Config{}
	if plain != "" {
		if err := json.Unmarshal([]byte(plain), &cfg); err != nil {
			return nil, err
		}
	}
	app := s.cloudAppFor(context.Background(), c.Kind)
	switch c.Kind {
	case "gdrive":
		cfg["type"], cfg["scope"] = "drive", "drive.readonly"
		if optionsOf(c).Write {
			cfg["scope"] = "drive"
		}
		cfg["client_id"], cfg["client_secret"] = app.ID, app.Secret
	case "dropbox":
		cfg["type"] = "dropbox"
		cfg["client_id"], cfg["client_secret"] = app.ID, app.Secret
	case "onedrive":
		cfg["type"] = "onedrive"
		cfg["client_id"], cfg["client_secret"] = app.ID, app.Secret
		if app.Tenant != "" && app.Tenant != "common" {
			cfg["tenant"] = app.Tenant
		}
	case "icloud":
		cfg["type"] = "iclouddrive"
	}
	return cfg, nil
}

// saveConfig seals the remote back (rclone may have refreshed a token or cookie). The
// box's own OAuth client never goes into the database.
func (s *Server) saveConfig(ctx context.Context, c *store.Cloud, cfg clouds.Config) error {
	if len(cfg) == 0 {
		return nil
	}
	keep := clouds.Config{}
	for k, v := range cfg {
		if k == "client_id" || k == "client_secret" {
			continue
		}
		keep[k] = v
	}
	b, _ := json.Marshal(keep)
	sealed, err := s.seal(string(b))
	if err != nil {
		return err
	}
	c.Config = sealed
	return s.store.SetCloudConfig(ctx, c.ID, sealed)
}

func (s *Server) rclone() *clouds.Rclone {
	return &clouds.Rclone{Bin: s.cfg.Rclone, Dir: filepath.Join(s.cfg.DataDir, "rclone")}
}

// ---------- the runner ----------

type cloudProgress struct {
	StartedAt  time.Time `json:"started_at"`
	Waiting    bool      `json:"waiting,omitempty"` // queued behind other drives
	Bytes      int64     `json:"bytes"`
	TotalBytes int64     `json:"total_bytes"`
	Files      int64     `json:"files"`
	TotalFiles int64     `json:"total_files"`
	Checked    int64     `json:"checked"`
	Speed      float64   `json:"speed"`
	ETA        *int64    `json:"eta,omitempty"`
}

type cloudRun struct {
	cancel   context.CancelFunc
	progress cloudProgress
	force    bool // past the two-way "too many deletes" brake, once
}

type cloudRunner struct {
	s       *Server
	mu      sync.Mutex
	running map[int64]*cloudRun
	pending map[int64]*clouds.Question // iCloud waiting for a code
	force   map[int64]bool             // "Sync anyway" asked for the next run
	sem     chan struct{}
}

func newCloudRunner(s *Server) *cloudRunner {
	return &cloudRunner{s: s, running: map[int64]*cloudRun{}, pending: map[int64]*clouds.Question{}, force: map[int64]bool{}, sem: make(chan struct{}, s.cfg.CloudSyncConcurrency)}
}

func (cr *cloudRunner) progress(id int64) *cloudProgress {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	if r := cr.running[id]; r != nil {
		p := r.progress
		return &p
	}
	return nil
}

func (cr *cloudRunner) question(id int64) *clouds.Question {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	return cr.pending[id]
}

func (cr *cloudRunner) setQuestion(id int64, q *clouds.Question) {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	if q == nil {
		delete(cr.pending, id)
	} else {
		cr.pending[id] = q
	}
}

func (cr *cloudRunner) stop(id int64) bool {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	if r := cr.running[id]; r != nil {
		r.cancel()
		return true
	}
	return false
}

// loop starts scheduled drives when they are due.
func (cr *cloudRunner) loop(ctx context.Context) {
	cr.s.store.ResetSyncingClouds(ctx)
	// Config files of runs cut short by a restart hold sign-ins: never leave them lying around.
	for _, pat := range []string{"remote-*.conf", "excludes-*.txt", "files-*.txt"} {
		if old, _ := filepath.Glob(filepath.Join(cr.s.cfg.DataDir, "rclone", pat)); len(old) > 0 {
			for _, f := range old {
				_ = os.Remove(f)
			}
		}
	}
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	lastPrune := time.Time{}
	for {
		if time.Since(lastPrune) > time.Hour {
			cr.s.pruneVersions()
			lastPrune = time.Now()
		}
		list, _ := cr.s.store.AllClouds(ctx)
		for i := range list {
			if c := &list[i]; cr.due(c) {
				cr.start(ctx, c)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (cr *cloudRunner) due(c *store.Cloud) bool {
	if c.IntervalHours <= 0 || (c.Status != "idle" && c.Status != "error") {
		return false
	}
	if c.LastSyncAt == nil {
		return true
	}
	last, err := time.Parse(time.RFC3339Nano, *c.LastSyncAt)
	return err != nil || time.Since(last) >= time.Duration(c.IntervalHours)*time.Hour
}

// start runs one drive's copy in the background (no-op when it is already running).
func (cr *cloudRunner) start(parent context.Context, c *store.Cloud) {
	cr.mu.Lock()
	if cr.running[c.ID] != nil {
		cr.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	run := &cloudRun{cancel: cancel, progress: cloudProgress{StartedAt: time.Now(), Waiting: true}, force: cr.force[c.ID]}
	delete(cr.force, c.ID)
	cr.running[c.ID] = run
	cr.mu.Unlock()
	_ = cr.s.store.SetCloudStatus(ctx, c.ID, "syncing", "")

	go func() {
		defer func() {
			cancel()
			cr.mu.Lock()
			delete(cr.running, c.ID)
			cr.mu.Unlock()
		}()
		select {
		case cr.sem <- struct{}{}:
			defer func() { <-cr.sem }()
		case <-ctx.Done():
			_ = cr.s.store.SetCloudStatus(context.Background(), c.ID, "idle", "")
			return
		}
		cr.mu.Lock()
		run.progress.Waiting = false
		run.progress.StartedAt = time.Now()
		cr.mu.Unlock()
		cr.s.syncCloud(ctx, c, run.force, func(st clouds.Stats) {
			cr.mu.Lock()
			run.progress.Bytes, run.progress.TotalBytes = st.Bytes, st.TotalBytes
			run.progress.Files, run.progress.TotalFiles = st.Transfers, st.TotalTransfers
			run.progress.Checked, run.progress.Speed, run.progress.ETA = st.Checks, st.Speed, st.ETA
			cr.mu.Unlock()
		})
	}()
}

// syncCloud does one copy and records how it went.
func (s *Server) syncCloud(ctx context.Context, c *store.Cloud, force bool, onStats func(clouds.Stats)) {
	bg := context.WithoutCancel(ctx)
	before := c.Status
	cfg, err := s.runtimeConfig(c)
	if err != nil {
		s.finishCloud(bg, c, before, err, 0)
		return
	}
	if twoWay(c) {
		copied, err := s.syncTwoWay(ctx, c, cfg, force, onStats)
		s.finishCloud(bg, c, before, err, copied)
		return
	}
	o := optionsOf(c)
	dest, _, _, err := s.destOf(ctx, c)
	if err != nil {
		s.finishCloud(bg, c, before, err, 0)
		return
	}
	flags := []string{"--transfers", "4", "--checkers", "8"}
	if o.DestSpace != "" {
		// A folder the person also works in: never replace a file they changed after the cloud did.
		flags = append(flags, "--update")
	}
	var extra []clouds.Job
	switch c.Kind {
	case "gdrive":
		flags = append(flags, "--drive-export-formats", "docx,xlsx,pptx,svg", "--drive-skip-shortcuts", "--tpslimit", "10", "--fast-list")
		if o.SharedWithMe {
			extra = append(extra, clouds.Job{Dest: filepath.Join(dest, "Shared with me"),
				Flags: append(append([]string{}, flags...), "--drive-shared-with-me")})
		}
	case "dropbox":
		flags = append(flags, "--tpslimit", "12")
	case "icloud":
		flags = append(flags, "--tpslimit", "5")
	}
	jobs := append([]clouds.Job{{Source: o.Source, Dest: dest, Flags: flags}}, extra...)
	var total clouds.Stats
	for _, job := range jobs {
		job.Config = cfg
		res, err := s.rclone().Copy(ctx, job, onStats)
		if res != nil {
			if res.Config != nil {
				if serr := s.saveConfig(bg, c, res.Config); serr != nil {
					s.log.Warn("cloud: saving refreshed sign-in failed", "cloud", c.ID, "err", serr)
				}
				cfg, _ = s.runtimeConfig(c)
			}
			total.Bytes += res.Stats.Bytes
			total.Transfers += res.Stats.Transfers
		}
		if err != nil {
			s.finishCloud(bg, c, before, err, total.Transfers)
			return
		}
	}
	s.finishCloud(bg, c, before, nil, total.Transfers)
}

func (s *Server) finishCloud(ctx context.Context, c *store.Cloud, before string, err error, copied int64) {
	dest, sp, rel, derr := s.destOf(ctx, c)
	var files, size int64
	if derr == nil {
		files, size = dirTotals(dest)
	}
	status, msg := "idle", ""
	if c.IntervalHours == 0 {
		status = "done"
	}
	switch {
	case errors.Is(err, context.Canceled):
		status, msg = "idle", "Stopped"
		if c.IntervalHours == 0 {
			status = "error"
		}
	case errors.Is(err, clouds.ErrReconnect):
		status, msg = "reconnect", err.Error()
	case err != nil:
		status, msg = "error", err.Error()
	}
	if len(msg) > 600 {
		msg = msg[:600] + "…"
	}
	_ = s.store.FinishCloudSync(ctx, c.ID, status, msg, files, size, err == nil)
	s.touch()
	if sp != nil {
		s.indexTouch(sp, rel)
	} else if s.search != nil {
		s.search.Touch("cloud:"+strconv.FormatInt(c.ID, 10), "/")
	}
	s.log.Info("cloud copy finished", "cloud", c.ID, "kind", c.Kind, "status", status, "copied", copied, "files", files, "err", msg)
	link := s.cfg.BaseURL + "/settings/clouds"
	switch {
	case status == "reconnect" && before != "reconnect":
		_ = s.notifyUser(ctx, c.UserID, "clouds", c.Name+" needs a new sign-in", "Pocket Files can no longer copy "+c.Name+": "+msg, link)
	case status == "error" && before != "error" && !errors.Is(err, context.Canceled):
		_ = s.notifyUser(ctx, c.UserID, "clouds", "Copying "+c.Name+" failed", msg, link)
	case status == "done":
		where := "My files" + optionsOf(c).Folder
		if sp != nil && optionsOf(c).DestSpace != "" {
			where = sp.Name + strings.TrimSuffix(rel, "/")
		}
		_ = s.notifyUser(ctx, c.UserID, "clouds", c.Name+" is copied", fmt.Sprintf("%d files are now in %s.", files, where), link)
	}
}

// dirTotals counts the files and bytes of a copy.
func dirTotals(root string) (files, size int64) {
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			files++
			size += info.Size()
		}
		return nil
	})
	return
}

// ---------- API ----------

type cloudView struct {
	store.Cloud
	Provider     string           `json:"provider"`
	SharedWithMe bool             `json:"shared_with_me,omitempty"`
	Folder       string           `json:"folder,omitempty"`
	Source       string           `json:"source,omitempty"`
	DestSpace    string           `json:"dest_space,omitempty"`
	DestPath     string           `json:"dest_path,omitempty"`
	DestName     string           `json:"dest_name,omitempty"` // the picked place's name, for display
	Direction    string           `json:"direction"`
	Conflict     string           `json:"conflict,omitempty"`
	Conflicts    int              `json:"conflicts"`
	NeedsWrite   bool             `json:"needs_write,omitempty"` // two-way without a write sign-in yet
	Space        string           `json:"space,omitempty"`       // where to browse the copy
	Path         string           `json:"path,omitempty"`
	NextSyncAt   *string          `json:"next_sync_at,omitempty"`
	Progress     *cloudProgress   `json:"progress,omitempty"`
	Question     *clouds.Question `json:"question,omitempty"`
}

func (s *Server) viewCloud(c *store.Cloud) cloudView {
	o := optionsOf(c)
	v := cloudView{Cloud: *c, Provider: providerName(c.Kind), SharedWithMe: o.SharedWithMe, Folder: o.Folder, Source: o.Source,
		DestSpace: o.DestSpace, DestPath: o.DestPath, Progress: s.clouds.progress(c.ID),
		Direction: firstNonEmpty(o.Direction, "one_way"), Conflict: firstNonEmpty(o.Conflict, "ask"),
		Conflicts: s.store.CountCloudConflicts(context.Background(), c.ID), NeedsWrite: twoWay(c) && !o.Write && !writeless(c.Kind)}
	if c.IntervalHours == 0 {
		v.Direction = "once"
	}
	if o.DestSpace != "" {
		v.Space, v.Path = o.DestSpace, vfs.Clean(o.DestPath)
		v.DestName = "My files"
		if sp, err := s.space(context.Background(), c.UserID, o.DestSpace); err == nil && o.DestSpace != "home" {
			v.DestName = sp.Name
		}
		if c.IntervalHours > 0 && c.LastSyncAt != nil {
			if t, err := time.Parse(time.RFC3339Nano, *c.LastSyncAt); err == nil {
				n := t.Add(time.Duration(c.IntervalHours) * time.Hour).UTC().Format(time.RFC3339)
				v.NextSyncAt = &n
			}
		}
	} else if c.IntervalHours > 0 {
		v.Space, v.Path = "cloud:"+strconv.FormatInt(c.ID, 10), "/"
		if c.LastSyncAt != nil {
			if t, err := time.Parse(time.RFC3339Nano, *c.LastSyncAt); err == nil {
				n := t.Add(time.Duration(c.IntervalHours) * time.Hour).UTC().Format(time.RFC3339)
				v.NextSyncAt = &n
			}
		}
	} else {
		v.Space, v.Path = "home", o.Folder
	}
	if q := s.clouds.question(c.ID); q != nil {
		v.Question = &clouds.Question{Name: q.Name, Help: q.Help, Error: q.Error} // the state blob stays here
	}
	return v
}

func (s *Server) handleCloudProviders(w http.ResponseWriter, r *http.Request) {
	ver, err := s.rclone().Version(r.Context())
	out := map[string]any{"providers": s.cloudProviders(r.Context()), "rclone": ver, "redirect": s.cfg.BaseURL + "/api/v1/clouds/oauth/{kind}/callback"}
	if err != nil {
		out["rclone_error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleListClouds(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.CloudsFor(r.Context(), me(r))
	if err != nil {
		s.fail(w, err, "clouds")
		return
	}
	out := make([]cloudView, 0, len(list))
	for i := range list {
		out = append(out, s.viewCloud(&list[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

// cloudIn is what the connect form and the edit dialog send.
type cloudIn struct {
	Kind          string            `json:"kind"`
	Name          string            `json:"name"`
	IntervalHours *int              `json:"interval_hours"`
	SharedWithMe  *bool             `json:"shared_with_me"`
	Source        *string           `json:"source"`
	DestSpace     string            `json:"dest_space"`
	DestPath      string            `json:"dest_path"`
	Direction     *string           `json:"direction"` // one_way | two_way (scheduled drives)
	Conflict      *string           `json:"conflict"`  // ask | newer | cloud | pocket
	Fields        map[string]string `json:"fields"`
}

func validConflict(p string) bool { return p == "ask" || p == "newer" || p == "cloud" || p == "pocket" }

// checkDest refuses a destination the person cannot write to, before anything is connected,
// and a two-way sync into the top of My files or a mount: everything there would go to the cloud.
func (s *Server) checkDest(ctx context.Context, uid int64, in cloudIn) error {
	if in.DestSpace == "" {
		return nil
	}
	if _, err := s.destSpace(ctx, uid, in.DestSpace); err != nil {
		return err
	}
	if in.Direction != nil && *in.Direction == "two_way" && vfs.Clean(in.DestPath) == "/" {
		return badRequest("two-way sync needs a folder of its own: everything in it goes to the cloud too. Pick or make a folder (for example “Google Drive”)")
	}
	return nil
}

// newCloud makes the row for a fresh connection; one-time copies without a picked folder get
// a new folder in My files.
func (s *Server) newCloud(ctx context.Context, uid int64, in cloudIn, login string) *store.Cloud {
	c := &store.Cloud{UserID: uid, Kind: in.Kind, Login: login, IntervalHours: 6}
	c.Name = strings.TrimSpace(in.Name)
	if c.Name == "" {
		c.Name = providerName(in.Kind)
	}
	if in.IntervalHours != nil && *in.IntervalHours >= 0 {
		c.IntervalHours = *in.IntervalHours
	}
	o := cloudOptions{}
	if in.SharedWithMe != nil {
		o.SharedWithMe = *in.SharedWithMe
	}
	if in.Source != nil {
		o.Source = strings.Trim(strings.TrimSpace(*in.Source), "/")
	}
	if in.Direction != nil && *in.Direction == "two_way" && c.IntervalHours > 0 {
		o.Direction = "two_way"
	}
	if in.Conflict != nil && validConflict(*in.Conflict) {
		o.Conflict = *in.Conflict
	}
	o.Write = writeless(in.Kind)
	if in.DestSpace != "" {
		o.DestSpace, o.DestPath = in.DestSpace, vfs.Clean(in.DestPath)
	} else if c.IntervalHours == 0 {
		o.Folder = s.freeFolder(uid, c.Name)
	}
	c.Options = o.json()
	return c
}

// freeFolder picks "/Name", "/Name (2)", … in My files.
func (s *Server) freeFolder(uid int64, name string) string {
	name = strings.NewReplacer("/", "-", "\\", "-").Replace(name)
	for i := 1; ; i++ {
		cand := "/" + name
		if i > 1 {
			cand = fmt.Sprintf("/%s (%d)", name, i)
		}
		if _, err := os.Stat(filepath.Join(s.homeDir(uid), cand)); errors.Is(err, os.ErrNotExist) {
			return cand
		}
	}
}

// handleCreateCloud connects the form kinds: iCloud (may then ask for a code) and pasted rclone remotes.
func (s *Server) handleCreateCloud(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in cloudIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := s.checkDest(ctx, me(r), in); err != nil {
		s.fail(w, err, "destination")
		return
	}
	var cfg clouds.Config
	switch in.Kind {
	case "icloud":
		id, pass := strings.TrimSpace(in.Fields["apple_id"]), in.Fields["password"]
		if id == "" || pass == "" {
			writeErr(w, http.StatusBadRequest, "Apple ID and password are needed")
			return
		}
		cfg = clouds.Config{"type": "iclouddrive", "apple_id": id, "password": pass}
	case "rclone":
		var err error
		if cfg, err = clouds.ParseINI(in.Fields["config"]); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if checked, err := s.rclone().Check(ctx, cfg); err != nil {
			writeErr(w, http.StatusBadRequest, "rclone could not open the remote: "+err.Error())
			return
		} else {
			cfg = checked
		}
		if in.Name == "" {
			in.Name = strings.ToUpper(cfg["type"][:1]) + cfg["type"][1:]
		}
	default:
		writeErr(w, http.StatusBadRequest, "this kind connects with a sign-in page, not a form")
		return
	}
	c := s.newCloud(ctx, me(r), in, strings.TrimSpace(in.Fields["apple_id"]))
	if in.Kind == "icloud" {
		c.Status = "code"
	}
	if err := s.store.CreateCloud(ctx, c); err != nil {
		s.fail(w, err, "create cloud")
		return
	}
	if in.Kind == "icloud" {
		if err := s.icloudStep(ctx, c, cfg, "", ""); err != nil {
			_ = s.store.DeleteCloud(ctx, c.UserID, c.ID)
			writeErr(w, http.StatusBadRequest, "iCloud: "+err.Error())
			return
		}
	} else {
		if err := s.saveConfig(ctx, c, cfg); err != nil {
			s.fail(w, err, "save cloud")
			return
		}
		s.clouds.start(ctx, c)
	}
	s.touch()
	got, _ := s.store.CloudFor(ctx, c.UserID, c.ID)
	writeJSON(w, http.StatusCreated, s.viewCloud(got))
}

// icloudStep runs one round of rclone's iCloud setup: sign in, then the 2FA code.
func (s *Server) icloudStep(ctx context.Context, c *store.Cloud, cfg clouds.Config, state, answer string) error {
	got, q, err := s.rclone().ConfigStep(ctx, cfg, state, answer)
	if err != nil {
		return err
	}
	if got != nil {
		if err := s.saveConfig(ctx, c, got); err != nil {
			return err
		}
	}
	s.clouds.setQuestion(c.ID, q)
	if q != nil {
		return s.store.SetCloudStatus(ctx, c.ID, "code", q.Error)
	}
	if err := s.store.SetCloudStatus(ctx, c.ID, "idle", ""); err != nil {
		return err
	}
	c.Status = "idle"
	s.clouds.start(ctx, c)
	return nil
}

// handleCloudCode answers iCloud's question (the code Apple sent).
func (s *Server) handleCloudCode(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, err := s.store.CloudFor(ctx, me(r), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "cloud")
		return
	}
	var in struct {
		Code string `json:"code"`
	}
	_ = decode(r, &in)
	q := s.clouds.question(c.ID)
	if q == nil {
		writeErr(w, http.StatusConflict, "nothing is waiting for a code; connect again")
		return
	}
	cfg, err := s.runtimeConfig(c)
	if err != nil {
		s.fail(w, err, "cloud")
		return
	}
	if err := s.icloudStep(ctx, c, cfg, q.State, strings.TrimSpace(in.Code)); err != nil {
		writeErr(w, http.StatusBadRequest, "iCloud: "+err.Error())
		return
	}
	got, _ := s.store.CloudFor(ctx, c.UserID, c.ID)
	writeJSON(w, http.StatusOK, s.viewCloud(got))
}

// handleCloudReconnect signs an iCloud drive in again (password, then a new code).
// OAuth drives reconnect through /clouds/oauth/{kind}/start?cloud=ID instead.
func (s *Server) handleCloudReconnect(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, err := s.store.CloudFor(ctx, me(r), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "cloud")
		return
	}
	if c.Kind != "icloud" {
		writeErr(w, http.StatusBadRequest, "use the sign-in page for this drive")
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	_ = decode(r, &in)
	if in.Password == "" {
		writeErr(w, http.StatusBadRequest, "the Apple ID password is needed")
		return
	}
	cfg := clouds.Config{"type": "iclouddrive", "apple_id": c.Login, "password": in.Password}
	if err := s.icloudStep(ctx, c, cfg, "", ""); err != nil {
		writeErr(w, http.StatusBadRequest, "iCloud: "+err.Error())
		return
	}
	got, _ := s.store.CloudFor(ctx, c.UserID, c.ID)
	writeJSON(w, http.StatusOK, s.viewCloud(got))
}

func (s *Server) handleUpdateCloud(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, err := s.store.CloudFor(ctx, me(r), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "cloud")
		return
	}
	var in cloudIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	o := optionsOf(c)
	if n := strings.TrimSpace(in.Name); n != "" {
		c.Name = n
	}
	// A schedule can change, but a drive stays scheduled or one-time: they keep their copy in different places.
	if in.IntervalHours != nil && *in.IntervalHours > 0 && c.IntervalHours > 0 {
		c.IntervalHours = *in.IntervalHours
	}
	if in.SharedWithMe != nil {
		o.SharedWithMe = *in.SharedWithMe
	}
	if in.Source != nil {
		if src := strings.Trim(strings.TrimSpace(*in.Source), "/"); src != o.Source {
			o.Source, o.Ready = src, false // a different folder: the two-way listings start over
		}
	}
	if in.Conflict != nil && validConflict(*in.Conflict) {
		o.Conflict = *in.Conflict
	}
	if in.Direction != nil && c.IntervalHours > 0 {
		dir := "one_way"
		if *in.Direction == "two_way" {
			dir = "two_way"
		}
		if dir == "two_way" && o.DestSpace != "" && vfs.Clean(o.DestPath) == "/" {
			writeErr(w, http.StatusBadRequest, "two-way sync needs a folder of its own, not the top of "+map[bool]string{true: "My files", false: "a mount"}[o.DestSpace == "home"]+": connect the drive again into a folder")
			return
		}
		if dir != firstNonEmpty(o.Direction, "one_way") {
			// Either way, the next two-way run starts with a union of both sides (nothing deleted).
			o.Direction, o.Ready = dir, false
			_ = s.store.ClearCloudDocs(ctx, c.ID)
			_ = os.RemoveAll(s.bisyncDir(c))
		}
	}
	c.Options = o.json()
	if err := s.store.UpdateCloud(ctx, c); err != nil {
		s.fail(w, err, "cloud")
		return
	}
	if twoWay(c) && !o.Write && !writeless(c.Kind) {
		_ = s.store.SetCloudStatus(ctx, c.ID, "reconnect", "Two-way sync needs write access: sign in again")
	} else if c.Status == "reconnect" && strings.HasPrefix(c.Error, "Two-way sync needs write access") {
		_ = s.store.SetCloudStatus(ctx, c.ID, "idle", "")
	}
	s.touch()
	got, _ := s.store.CloudFor(ctx, c.UserID, c.ID)
	writeJSON(w, http.StatusOK, s.viewCloud(got))
}

func (s *Server) handleCloudSync(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, err := s.store.CloudFor(ctx, me(r), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "cloud")
		return
	}
	if c.Status == "reconnect" || c.Status == "code" {
		writeErr(w, http.StatusConflict, "connect the drive again first")
		return
	}
	if queryBool(r, "force") {
		s.clouds.mu.Lock()
		s.clouds.force[c.ID] = true
		s.clouds.mu.Unlock()
	}
	s.clouds.start(ctx, c)
	got, _ := s.store.CloudFor(ctx, c.UserID, c.ID)
	writeJSON(w, http.StatusAccepted, s.viewCloud(got))
}

func (s *Server) handleCloudStop(w http.ResponseWriter, r *http.Request) {
	c, err := s.store.CloudFor(r.Context(), me(r), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "cloud")
		return
	}
	s.clouds.stop(c.ID)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleDeleteCloud disconnects a drive. A scheduled drive's copy is deleted, or with
// ?keep=1 moved into My files; one-time copies already live in My files and stay.
func (s *Server) handleDeleteCloud(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, err := s.store.CloudFor(ctx, me(r), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "cloud")
		return
	}
	if s.clouds.stop(c.ID) {
		time.Sleep(time.Second) // let rclone let go of the folder
	}
	s.clouds.setQuestion(c.ID, nil)
	moved := ""
	if ownSpace(c) {
		dir := s.cloudDir(c)
		if queryBool(r, "keep") {
			moved = s.freeFolder(c.UserID, c.Name)
			if err := os.MkdirAll(s.homeDir(c.UserID), 0o755); err == nil {
				err = os.Rename(dir, filepath.Join(s.homeDir(c.UserID), moved))
			}
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				s.fail(w, err, "keep the copy")
				return
			}
		} else if err := os.RemoveAll(dir); err != nil {
			s.fail(w, err, "delete the copy")
			return
		}
	}
	if err := s.store.DeleteCloud(ctx, c.UserID, c.ID); err != nil {
		s.fail(w, err, "cloud")
		return
	}
	s.touch()
	if moved != "" {
		s.search.Touch("home:"+strconv.FormatInt(c.UserID, 10), moved)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "folder": moved})
}

// ---------- OAuth ----------

const cloudStateCookie = "pf_cloud_oauth"

func (s *Server) cloudRedirect(kind string) string {
	return s.cfg.BaseURL + "/api/v1/clouds/oauth/" + kind + "/callback"
}

func (s *Server) signState(v string) string {
	m := hmac.New(sha256.New, s.secret())
	m.Write([]byte("cloud-oauth:" + v))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// handleCloudOAuthStart answers {url} for the browser; the connect choices ride in a signed cookie.
func (s *Server) handleCloudOAuthStart(w http.ResponseWriter, r *http.Request) {
	kind := chi.URLParam(r, "kind")
	q := r.URL.Query()
	// Write access only for two-way drives: a new one asked for so, or one being reconnected.
	write := q.Get("direction") == "two_way"
	if id, _ := strconv.ParseInt(q.Get("cloud"), 10, 64); id > 0 {
		if c, err := s.store.CloudFor(r.Context(), me(r), id); err == nil {
			write = twoWay(c)
		}
	}
	app := s.oauthApp(r.Context(), kind, write)
	if app == nil {
		writeErr(w, http.StatusBadRequest, providerName(kind)+" is not set up on this box yet (the admin adds its OAuth client; see docs/clouds.md)")
		return
	}
	in := cloudIn{Kind: kind, Name: q.Get("name")}
	if d := q.Get("direction"); d != "" {
		in.Direction = &d
	}
	if p := q.Get("conflict"); p != "" {
		in.Conflict = &p
	}
	if v, err := strconv.Atoi(q.Get("interval_hours")); err == nil {
		in.IntervalHours = &v
	}
	if q.Has("shared_with_me") {
		b := queryBool(r, "shared_with_me")
		in.SharedWithMe = &b
	}
	if q.Has("source") {
		src := q.Get("source")
		in.Source = &src
	}
	in.DestSpace, in.DestPath = q.Get("dest_space"), q.Get("dest_path")
	if err := s.checkDest(r.Context(), me(r), in); err != nil {
		s.fail(w, err, "destination")
		return
	}
	payload, _ := json.Marshal(map[string]any{"in": in, "cloud": q.Get("cloud"), "uid": me(r), "write": write, "nonce": store.RandomToken(12)})
	val := base64.RawURLEncoding.EncodeToString(payload)
	state := s.signState(val)
	http.SetCookie(w, &http.Cookie{Name: cloudStateCookie, Value: val + "." + state, Path: "/api/v1/clouds/oauth/", MaxAge: 900,
		HttpOnly: true, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteLaxMode})
	writeJSON(w, http.StatusOK, map[string]string{"url": app.AuthCodeURL(s.cloudRedirect(kind), state)})
}

func (s *Server) handleCloudOAuthCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	kind := chi.URLParam(r, "kind")
	back := func(q string) { http.Redirect(w, r, "/settings/clouds?"+q, http.StatusFound) }
	fail := func(msg string) { back("error=" + url.QueryEscape(msg)) }
	http.SetCookie(w, &http.Cookie{Name: cloudStateCookie, Value: "", Path: "/api/v1/clouds/oauth/", MaxAge: -1})
	if e := r.URL.Query().Get("error"); e != "" {
		fail(providerName(kind) + ": " + firstNonEmpty(r.URL.Query().Get("error_description"), e))
		return
	}
	ck, err := r.Cookie(cloudStateCookie)
	val, sig, ok := strings.Cut(func() string {
		if err != nil {
			return ""
		}
		return ck.Value
	}(), ".")
	if !ok || sig != r.URL.Query().Get("state") || !hmac.Equal([]byte(sig), []byte(s.signState(val))) {
		fail("The sign-in took too long or came from another browser. Try again.")
		return
	}
	raw, _ := base64.RawURLEncoding.DecodeString(val)
	var st struct {
		In    cloudIn `json:"in"`
		Cloud string  `json:"cloud"`
		UID   int64   `json:"uid"`
		Write bool    `json:"write"`
	}
	if json.Unmarshal(raw, &st) != nil || st.UID != me(r) || st.In.Kind != kind {
		fail("The sign-in does not match this account. Try again.")
		return
	}
	app := s.oauthApp(r.Context(), kind, st.Write)
	if app == nil {
		fail(providerName(kind) + " is not set up on this box")
		return
	}
	hc := &http.Client{Timeout: 30 * time.Second}
	tok, err := app.Exchange(ctx, hc, r.URL.Query().Get("code"), s.cloudRedirect(kind))
	if err != nil {
		fail(err.Error())
		return
	}
	login, extra, err := app.Account(ctx, hc, tok)
	if err != nil {
		fail(providerName(kind) + ": " + err.Error())
		return
	}
	cfg := clouds.Config{"token": tok.JSON()}
	for k, v := range extra {
		cfg[k] = v
	}

	var c *store.Cloud
	if id, _ := strconv.ParseInt(st.Cloud, 10, 64); id > 0 {
		// Reconnect: same drive, new sign-in.
		if c, err = s.store.CloudFor(ctx, me(r), id); err != nil || c.Kind != kind {
			fail("That drive is gone.")
			return
		}
		_ = s.store.SetCloudLogin(ctx, c.ID, login)
		_ = s.store.SetCloudStatus(ctx, c.ID, "idle", "")
		c.Status = "idle"
	} else {
		c = s.newCloud(ctx, me(r), st.In, login)
		if st.In.Name == "" && login != "" {
			c.Name = providerName(kind)
		}
		if err := s.store.CreateCloud(ctx, c); err != nil {
			fail(err.Error())
			return
		}
	}
	if err := s.saveConfig(ctx, c, cfg); err != nil {
		fail(err.Error())
		return
	}
	// Remember whether this sign-in may write (two-way needs it).
	o := optionsOf(c)
	o.Write = st.Write
	c.Options = o.json()
	_ = s.store.UpdateCloud(ctx, c)
	s.clouds.start(ctx, c)
	s.touch()
	back("connected=" + strconv.FormatInt(c.ID, 10))
}
