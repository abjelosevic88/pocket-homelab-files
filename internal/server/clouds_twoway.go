package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-files/internal/clouds"
	"github.com/abjelosevic88/pocket-homelab-files/internal/store"
)

// Two-way drives (docs/clouds.md): rclone bisync for files, and for native Google Docs,
// Sheets and Slides a pass of our own that uploads edits INTO the same Doc. Files changed on
// both sides wait under Conflicts (or follow the drive's conflict rule). Whatever this side
// loses goes to a versions folder kept 30 days; the cloud side has its own trash.

const versionsKeep = 30 * 24 * time.Hour

// errSafetyStop marks a run bisync stopped by itself (too many deletes at once, …).
var errSafetyStop = errors.New("stopped for safety")

func (s *Server) bisyncDir(c *store.Cloud) string {
	return filepath.Join(s.cfg.DataDir, "rclone", "bisync", strconv.FormatInt(c.ID, 10))
}

// versionsDir is on the same disk as the drive's folder when it is its own place.
func (s *Server) versionsDir(c *store.Cloud) string {
	return filepath.Join(s.cfg.CloudDir, strconv.FormatInt(c.UserID, 10), "versions", strconv.FormatInt(c.ID, 10))
}

func (s *Server) syncTwoWay(ctx context.Context, c *store.Cloud, cfg clouds.Config, force bool, onStats func(clouds.Stats)) (int64, error) {
	o := optionsOf(c)
	if !o.Write && !writeless(c.Kind) {
		return 0, &clouds.AuthError{Msg: "Two-way sync needs write access: sign in again"}
	}
	dest, _, _, err := s.destOf(ctx, c)
	if err != nil {
		return 0, err
	}
	stamp := time.Now().Format("2006-01-02 15.04")
	versions := filepath.Join(s.versionsDir(c), stamp)
	flags := []string{"--transfers", "4", "--checkers", "8"}
	var excludes []string
	switch c.Kind {
	case "gdrive":
		// "Shared with me" is not part of the drive: it stays a one-way copy (below), never uploaded.
		flags = append(flags, "--drive-skip-gdocs", "--drive-skip-shortcuts", "--tpslimit", "10", "--exclude", "/Shared with me/**")
		docs, err := s.googleDocsPass(ctx, c, cfg, dest, versions)
		if err != nil {
			return 0, err
		}
		excludes = docs
		cfg, _ = s.runtimeConfig(c) // the pass may have refreshed the sign-in
	case "dropbox":
		// Paper docs exist only as exports: they come down one-way (below), and bisync leaves them be.
		flags = append(flags, "--tpslimit", "12", "--dropbox-skip-exports")
		exports, err := s.exportsOnly(ctx, cfg, o.Source, "--dropbox-skip-exports")
		if err != nil {
			return 0, err
		}
		excludes = exports
	case "icloud":
		flags = append(flags, "--tpslimit", "5")
	}
	job := clouds.BisyncJob{Config: cfg, Source: o.Source, Local: dest, WorkDir: s.bisyncDir(c), Versions: versions,
		Policy: clouds.Policy(firstNonEmpty(o.Conflict, "ask")), Resync: !o.Ready, Force: force, Excludes: excludes, Flags: flags}
	conflictsBefore := s.store.CountCloudConflicts(ctx, c.ID)
	res, err := s.rclone().Bisync(ctx, job, onStats)
	if res != nil && res.Aborted == "needs_resync" && !job.Resync {
		// rclone lost its listings: start again with a union (copies only, never deletes).
		job.Resync = true
		res, err = s.rclone().Bisync(ctx, job, onStats)
	}
	if res != nil && res.Config != nil {
		if serr := s.saveConfig(context.WithoutCancel(ctx), c, res.Config); serr != nil {
			s.log.Warn("cloud: saving refreshed sign-in failed", "cloud", c.ID, "err", serr)
		}
	}
	if res != nil {
		for _, p := range res.Conflicts {
			cn, pn := clouds.ConflictNames(p)
			_ = s.store.AddCloudConflict(ctx, store.CloudConflict{CloudID: c.ID, Path: p, CloudName: cn, PocketName: pn})
		}
	}
	if err != nil {
		if res != nil && (res.Aborted == "too_many_deletes" || res.Aborted == "all_changed") {
			what := "it would delete many files at once"
			if res.Aborted == "all_changed" {
				what = "every file looks changed on one side"
			}
			return 0, fmt.Errorf("%w: %s. Check the drive, then use Sync anyway", errSafetyStop, what)
		}
		return 0, err
	}
	if !o.Ready {
		o = optionsOf(c)
		o.Ready = true
		c.Options = o.json()
		_ = s.store.UpdateCloud(context.WithoutCancel(ctx), c)
	}
	copied := res.Stats.Transfers
	if c.Kind == "gdrive" && o.SharedWithMe {
		cfg, _ = s.runtimeConfig(c)
		shared, err := s.rclone().Copy(ctx, clouds.Job{Config: cfg, Dest: filepath.Join(dest, "Shared with me"),
			Flags: []string{"--transfers", "4", "--checkers", "8", "--drive-export-formats", "docx,xlsx,pptx,svg", "--drive-skip-shortcuts",
				"--tpslimit", "10", "--fast-list", "--drive-shared-with-me", "--update"}}, onStats)
		if shared != nil && shared.Config != nil {
			_ = s.saveConfig(context.WithoutCancel(ctx), c, shared.Config)
			copied += shared.Stats.Transfers
		}
		if err != nil {
			return copied, err
		}
	}
	if c.Kind == "dropbox" && len(excludes) > 0 {
		cfg, _ = s.runtimeConfig(c)
		got, err := s.rclone().Copy(ctx, clouds.Job{Config: cfg, Source: o.Source, Dest: dest, Files: excludes,
			Flags: []string{"--tpslimit", "12", "--update"}}, onStats)
		if got != nil && got.Config != nil {
			_ = s.saveConfig(context.WithoutCancel(ctx), c, got.Config)
		}
		if err != nil {
			return copied, err
		}
	}
	if n := s.store.CountCloudConflicts(ctx, c.ID); n > conflictsBefore {
		_ = s.notifyUser(context.WithoutCancel(ctx), c.UserID, "clouds", c.Name+": files changed on both sides",
			fmt.Sprintf("%d waiting: pick which version to keep in Settings → Connected drives.", n), s.cfg.BaseURL+"/settings/clouds")
	}
	return copied, nil
}

// googleDocsPass brings native Docs/Sheets/Slides up to date in both directions and returns
// their paths (relative), which bisync must leave alone.
func (s *Server) googleDocsPass(ctx context.Context, c *store.Cloud, cfg clouds.Config, dest, versions string) ([]string, error) {
	o := optionsOf(c)
	rc := s.rclone()
	exportFlags := []string{"--drive-export-formats", "docx,xlsx,pptx", "--drive-skip-shortcuts", "--fast-list"}
	all, err := rc.List(ctx, cfg, o.Source, exportFlags...)
	if err != nil {
		return nil, err
	}
	plain, err := rc.List(ctx, cfg, o.Source, "--drive-skip-gdocs", "--drive-skip-shortcuts", "--fast-list")
	if err != nil {
		return nil, err
	}
	isFile := map[string]bool{}
	for _, e := range plain {
		isFile[e.Path] = true
	}
	app := s.oauthApp(ctx, "gdrive", true)
	tok, _ := clouds.ParseToken(cfg["token"])
	api := &clouds.DriveAPI{App: app, Token: tok, OnToken: func(t *clouds.Token) {
		cfg["token"] = t.JSON()
		_ = s.saveConfig(context.WithoutCancel(ctx), c, cfg)
	}}
	state, err := s.store.CloudDocs(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	policy := firstNonEmpty(o.Conflict, "ask")
	src := strings.Trim(o.Source, "/")
	remotePath := func(p string) string {
		if src == "" {
			return p
		}
		return src + "/" + p
	}
	download := func(p, local string) error {
		got, err := rc.CopyFile(ctx, cfg, remotePath(p), local, exportFlags...)
		if err == nil && got != nil {
			cfg = got
			_ = s.saveConfig(context.WithoutCancel(ctx), c, cfg)
		}
		return err
	}
	keepVersion := func(local, rel string) {
		to := filepath.Join(versions, filepath.FromSlash(rel))
		if os.MkdirAll(filepath.Dir(to), 0o755) == nil {
			_ = os.Rename(local, to)
		}
	}
	record := func(p, id, format string, remote time.Time, local string) {
		if fi, err := os.Stat(local); err == nil {
			_ = s.store.SaveCloudDoc(ctx, c.ID, store.CloudDoc{Path: p, FileID: id, Format: format, RemoteMtime: remote,
				LocalMtime: fi.ModTime().UnixNano(), LocalSize: fi.Size()})
		}
	}

	var docs []string
	seen := map[string]bool{}
	for _, e := range all {
		ext := strings.TrimPrefix(filepath.Ext(e.Name), ".")
		if isFile[e.Path] || e.ID == "" || (ext != "docx" && ext != "xlsx" && ext != "pptx") {
			continue
		}
		p, local := e.Path, filepath.Join(dest, filepath.FromSlash(e.Path))
		docs = append(docs, p)
		seen[p] = true
		st, known := state[p]
		fi, statErr := os.Stat(local)
		exists := statErr == nil
		remoteChanged := !known || !e.ModTime.Equal(st.RemoteMtime)
		localChanged := known && exists && (fi.ModTime().UnixNano() != st.LocalMtime || fi.Size() != st.LocalSize)
		switch {
		case known && !exists && !remoteChanged:
			// Deleted here: to Google's trash (restorable there for 30 days).
			if err := api.Trash(ctx, e.ID); err != nil {
				return nil, err
			}
			_ = s.store.DeleteCloudDoc(ctx, c.ID, p)
		case !known && exists && fi.ModTime().After(e.ModTime.Add(2*time.Second)):
			// Already here and newer than the Doc (first two-way run after edits): treat as both changed.
			if err := s.docConflict(ctx, c, policy, api, e, p, local, versions, download, record, keepVersion); err != nil {
				return nil, err
			}
		case localChanged && remoteChanged:
			if err := s.docConflict(ctx, c, policy, api, e, p, local, versions, download, record, keepVersion); err != nil {
				return nil, err
			}
		case localChanged:
			mt, err := api.UploadInto(ctx, e.ID, local, ext)
			if err != nil {
				return nil, err
			}
			record(p, e.ID, ext, mt, local)
		case remoteChanged || !exists:
			if err := download(p, local); err != nil {
				return nil, err
			}
			record(p, e.ID, ext, e.ModTime, local)
		}
	}
	// Docs gone from Google: drop the local copy unless it was edited here (then it stays, and
	// syncs up as an ordinary file).
	for p, st := range state {
		if seen[p] {
			continue
		}
		local := filepath.Join(dest, filepath.FromSlash(p))
		if fi, err := os.Stat(local); err == nil && fi.ModTime().UnixNano() == st.LocalMtime && fi.Size() == st.LocalSize {
			keepVersion(local, p)
		}
		_ = s.store.DeleteCloudDoc(ctx, c.ID, p)
	}
	return docs, nil
}

// docConflict handles a Doc changed on both sides, by the drive's rule.
func (s *Server) docConflict(ctx context.Context, c *store.Cloud, policy string, api *clouds.DriveAPI, e clouds.LsEntry, p, local, versions string,
	download func(string, string) error, record func(string, string, string, time.Time, string), keepVersion func(string, string)) error {
	ext := strings.TrimPrefix(filepath.Ext(p), ".")
	fi, _ := os.Stat(local)
	mine := policy == "pocket" || (policy == "newer" && fi != nil && fi.ModTime().After(e.ModTime))
	switch {
	case policy == "ask":
		// Keep both: the Doc stays the Doc, this side's edit waits beside it.
		_, pocket := clouds.ConflictNames(p)
		if err := os.Rename(local, filepath.Join(filepath.Dir(local), filepath.Base(pocket))); err != nil {
			return err
		}
		if err := download(p, local); err != nil {
			return err
		}
		record(p, e.ID, ext, e.ModTime, local)
		return s.store.AddCloudConflict(ctx, store.CloudConflict{CloudID: c.ID, Path: p, CloudName: p, PocketName: pocket, Doc: true})
	case mine:
		mt, err := api.UploadInto(ctx, e.ID, local, ext) // Google keeps the old version in the Doc's history
		if err != nil {
			return err
		}
		record(p, e.ID, ext, mt, local)
	default:
		keepVersion(local, p)
		if err := download(p, local); err != nil {
			return err
		}
		record(p, e.ID, ext, e.ModTime, local)
	}
	return nil
}

// pruneVersions drops versions older than 30 days.
func (s *Server) pruneVersions() {
	root := filepath.Join(s.cfg.CloudDir)
	cut := time.Now().Add(-versionsKeep)
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() || filepath.Base(filepath.Dir(filepath.Dir(p))) != "versions" {
			return nil
		}
		// <CloudDir>/<uid>/versions/<drive>/<stamp>
		if t, err := time.ParseInLocation("2006-01-02 15.04", d.Name(), time.Local); err == nil && t.Before(cut) {
			_ = os.RemoveAll(p)
			return fs.SkipDir
		}
		return nil
	})
}

// ---------- conflicts ----------

func (s *Server) handleCloudConflicts(w http.ResponseWriter, r *http.Request) {
	c, err := s.store.CloudFor(r.Context(), me(r), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "cloud")
		return
	}
	list, err := s.store.CloudConflicts(r.Context(), c.ID)
	if err != nil {
		s.fail(w, err, "conflicts")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleResolveConflict: POST /clouds/{id}/conflicts/{cid} {keep: "pocket" | "cloud" | "both"}.
// The choice is made here, in the drive's folder; the next sync carries it to the cloud.
func (s *Server) handleResolveConflict(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, err := s.store.CloudFor(ctx, me(r), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "cloud")
		return
	}
	cf, err := s.store.CloudConflict(ctx, c.ID, idParam(r, "cid"))
	if err != nil {
		s.fail(w, err, "conflict")
		return
	}
	var in struct {
		Keep string `json:"keep"`
	}
	_ = decode(r, &in)
	dest, sp, rel, err := s.destOf(ctx, c)
	if err != nil {
		s.fail(w, err, "drive")
		return
	}
	at := func(p string) string { return filepath.Join(dest, filepath.FromSlash(p)) }
	orig, cloudV, pocketV := at(cf.Path), at(cf.CloudName), at(cf.PocketName)
	versions := filepath.Join(s.versionsDir(c), time.Now().Format("2006-01-02 15.04"))
	shelve := func(p, relName string) error {
		to := filepath.Join(versions, filepath.FromSlash(relName))
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		return os.Rename(p, to)
	}
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	switch in.Keep {
	case "pocket":
		if !exists(pocketV) {
			writeErr(w, http.StatusConflict, "this side's version is gone; sync again")
			return
		}
		if exists(cloudV) && cloudV != pocketV {
			if err := shelve(cloudV, cf.CloudName); err != nil {
				s.fail(w, err, "resolve")
				return
			}
		}
		if err := os.Rename(pocketV, orig); err != nil {
			s.fail(w, err, "resolve")
			return
		}
	case "cloud":
		if exists(pocketV) {
			if err := shelve(pocketV, cf.PocketName); err != nil {
				s.fail(w, err, "resolve")
				return
			}
		}
		if !cf.Doc && exists(cloudV) {
			if err := os.Rename(cloudV, orig); err != nil {
				s.fail(w, err, "resolve")
				return
			}
		}
	case "both":
		// Both stay, with plain names: the original name for the cloud's, "(edited here)" for this side's.
		ext := filepath.Ext(cf.Path)
		mineName := strings.TrimSuffix(cf.Path, ext) + " (edited here)" + ext
		if exists(pocketV) {
			if err := os.Rename(pocketV, at(mineName)); err != nil {
				s.fail(w, err, "resolve")
				return
			}
		}
		if !cf.Doc && exists(cloudV) {
			if err := os.Rename(cloudV, orig); err != nil {
				s.fail(w, err, "resolve")
				return
			}
		}
	default:
		writeErr(w, http.StatusBadRequest, `keep is "pocket", "cloud" or "both"`)
		return
	}
	_ = s.store.DeleteCloudConflict(ctx, cf.ID)
	s.touch()
	if sp != nil {
		s.indexTouch(sp, rel)
	} else if s.search != nil {
		s.search.Touch("cloud:"+strconv.FormatInt(c.ID, 10), filepath.ToSlash(filepath.Dir("/"+cf.Path)))
	}
	if !s.clouds.isRunning(c.ID) {
		s.clouds.start(context.WithoutCancel(ctx), c) // carry the choice to the cloud now
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (cr *cloudRunner) isRunning(id int64) bool {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	return cr.running[id] != nil
}

// exportsOnly lists the files a cloud only has as exports (Dropbox Paper): everything listed
// normally that disappears with the skip flag.
func (s *Server) exportsOnly(ctx context.Context, cfg clouds.Config, source, skipFlag string) ([]string, error) {
	all, err := s.rclone().List(ctx, cfg, source)
	if err != nil {
		return nil, err
	}
	plain, err := s.rclone().List(ctx, cfg, source, skipFlag)
	if err != nil {
		return nil, err
	}
	real := map[string]bool{}
	for _, e := range plain {
		real[e.Path] = true
	}
	var out []string
	for _, e := range all {
		if !real[e.Path] {
			out = append(out, e.Path)
		}
	}
	return out, nil
}
