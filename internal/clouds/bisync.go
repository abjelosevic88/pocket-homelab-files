package clouds

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Two-way sync is rclone bisync between the cloud (Path1) and a local folder (Path2). Its
// listings live in a work folder per drive, so a run only acts on what changed since the last.

// Conflict suffixes: "Report.docx" changed on both sides becomes "Report.cloud.docx" (the
// cloud's) and "Report.pocket.docx" (this side's), on both sides, until someone picks.
const (
	SuffixCloud  = "cloud"
	SuffixPocket = "pocket"
)

// Policy is what happens to a file changed on both sides.
type Policy string

const (
	PolicyAsk    Policy = "ask"    // keep both, renamed; the person picks
	PolicyNewer  Policy = "newer"  // the later change wins
	PolicyCloud  Policy = "cloud"  // the cloud's version wins
	PolicyPocket Policy = "pocket" // this side's version wins
)

// BisyncJob is one two-way run.
type BisyncJob struct {
	Config    Config
	Source    string // folder inside the cloud ("" = all of it)
	Local     string
	WorkDir   string // rclone's listings for this drive
	Versions  string // where this side's overwritten and deleted files go (not inside Local)
	Policy    Policy
	Resync    bool     // first run, or after the listings were lost: a union of both sides, nothing deleted
	Force     bool     // run despite the "too many deletes" brake
	MaxDelete int      // percent of files one run may delete (default 30)
	Excludes  []string // paths (relative) bisync must leave alone (native Google Docs)
	Flags     []string
}

// BisyncResult is how a two-way run went.
type BisyncResult struct {
	Stats     Stats
	Config    Config
	Conflicts []string // paths (relative, original names) changed on both sides
	Errors    []string
	Aborted   string // "too_many_deletes" | "all_changed" | "needs_resync" when bisync stopped itself
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// Bisync runs one two-way sync.
func (r *Rclone) Bisync(ctx context.Context, job BisyncJob, onStats func(Stats)) (*BisyncResult, error) {
	path, done, err := r.session(job.Config)
	if err != nil {
		return nil, err
	}
	defer done()
	for _, d := range []string{job.Local, job.WorkDir, job.Versions} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	maxDel := job.MaxDelete
	if maxDel <= 0 {
		maxDel = 30
	}
	args := []string{"bisync", Remote + ":" + strings.TrimPrefix(job.Source, "/"), job.Local,
		"--config", path, "--workdir", job.WorkDir, "--use-json-log", "--log-level", "NOTICE", "--color", "NEVER",
		"--stats", "5s", "--stats-log-level", "NOTICE", "--create-empty-src-dirs", "--resilient", "--recover",
		"--max-lock", "2m", "--max-delete", fmt.Sprint(maxDel), "--backup-dir2", job.Versions,
		"--retries", "3", "--low-level-retries", "10"}
	switch job.Policy {
	case PolicyNewer:
		args = append(args, "--conflict-resolve", "newer", "--conflict-loser", "delete")
	case PolicyCloud:
		args = append(args, "--conflict-resolve", "path1", "--conflict-loser", "delete")
	case PolicyPocket:
		args = append(args, "--conflict-resolve", "path2", "--conflict-loser", "delete")
	default:
		args = append(args, "--conflict-resolve", "none", "--conflict-loser", "pathname",
			"--conflict-suffix", SuffixCloud+","+SuffixPocket, "--suffix-keep-extension")
	}
	if job.Resync {
		// A union of both sides: nothing is deleted; where both have a file, the newer wins.
		args = append(args, "--resync", "--resync-mode", "newer")
	}
	if job.Force {
		args = append(args, "--force")
	}
	if len(job.Excludes) > 0 {
		f, err := os.CreateTemp(r.Dir, "excludes-*.txt")
		if err != nil {
			return nil, err
		}
		for _, p := range job.Excludes {
			fmt.Fprintln(f, "/"+strings.TrimPrefix(escapeGlob(p), "/"))
		}
		f.Close()
		defer os.Remove(f.Name())
		args = append(args, "--exclude-from", f.Name())
	}
	args = append(args, job.Flags...)

	cmd := exec.CommandContext(ctx, r.Bin, args...)
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 30 * time.Second
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start rclone: %w", err)
	}
	res := &BisyncResult{}
	seen := map[string]bool{}
	sc := bufio.NewScanner(stderr)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		var l logLine
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			if t := strings.TrimSpace(ansiRe.ReplaceAllString(sc.Text(), "")); t != "" {
				res.Errors = appendTail(res.Errors, t)
			}
			continue
		}
		msg := ansiRe.ReplaceAllString(l.Msg, "")
		if l.Stats != nil {
			res.Stats = *l.Stats
			if onStats != nil {
				onStats(*l.Stats)
			}
		}
		if p, ok := conflictPath(msg); ok && !seen[p] {
			seen[p] = true
			res.Conflicts = append(res.Conflicts, p)
		}
		low := strings.ToLower(msg)
		switch {
		case strings.Contains(low, "too many deletes"):
			res.Aborted = "too_many_deletes"
		case strings.Contains(low, "all files were changed"):
			res.Aborted = "all_changed"
		case strings.Contains(low, "must run --resync"), strings.Contains(low, "cannot find prior path"):
			res.Aborted = "needs_resync"
		}
		if l.Level == "error" || l.Level == "critical" {
			res.Errors = appendTail(res.Errors, msg)
		}
	}
	waitErr := cmd.Wait()
	if c, err := r.dump(context.WithoutCancel(ctx), path); err == nil {
		res.Config = c
	}
	if waitErr != nil {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		msg := firstNonEmpty(res.Stats.LastError, last(res.Errors), waitErr.Error())
		if res.Aborted == "" && IsAuthMessage(strings.Join(res.Errors, "\n")+"\n"+res.Stats.LastError) {
			return res, &AuthError{Msg: msg}
		}
		return res, errors.New(msg)
	}
	return res, nil
}

// conflictPath reads "New or changed in both paths - Report.docx" from bisync's log.
func conflictPath(msg string) (string, bool) {
	i := strings.Index(msg, "New or changed in both paths")
	if i < 0 {
		return "", false
	}
	rest := msg[i+len("New or changed in both paths"):]
	if j := strings.Index(rest, " - "); j >= 0 {
		p := strings.TrimSpace(rest[j+3:])
		return p, p != ""
	}
	return "", false
}

// ConflictNames are the two names a conflicted file gets: "a/Report.docx" →
// "a/Report.cloud.docx", "a/Report.pocket.docx".
func ConflictNames(p string) (cloud, pocket string) {
	ext := filepath.Ext(p)
	base := strings.TrimSuffix(p, ext)
	return base + "." + SuffixCloud + ext, base + "." + SuffixPocket + ext
}

func escapeGlob(p string) string {
	return strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`, `]`, `\]`, `{`, `\{`, `}`, `\}`).Replace(p)
}

// LsEntry is one line of `rclone lsjson`.
type LsEntry struct {
	Path     string    `json:"Path"`
	Name     string    `json:"Name"`
	Size     int64     `json:"Size"`
	ModTime  time.Time `json:"ModTime"`
	IsDir    bool      `json:"IsDir"`
	ID       string    `json:"ID"`
	MimeType string    `json:"MimeType"`
}

// List lists the remote recursively (files only).
func (r *Rclone) List(ctx context.Context, c Config, source string, flags ...string) ([]LsEntry, error) {
	path, done, err := r.session(c)
	if err != nil {
		return nil, err
	}
	defer done()
	args := append([]string{"lsjson", Remote + ":" + strings.TrimPrefix(source, "/"), "-R", "--files-only", "--config", path}, flags...)
	out, err := exec.CommandContext(ctx, r.Bin, args...).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, errors.New(firstNonEmpty(cleanLog(string(ee.Stderr)), err.Error()))
		}
		return nil, err
	}
	var list []LsEntry
	return list, json.Unmarshal(out, &list)
}

// CopyFile downloads one remote file to a local path.
func (r *Rclone) CopyFile(ctx context.Context, c Config, remote, local string, flags ...string) (Config, error) {
	path, done, err := r.session(c)
	if err != nil {
		return nil, err
	}
	defer done()
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		return nil, err
	}
	args := append([]string{"copyto", Remote + ":" + strings.TrimPrefix(remote, "/"), local, "--config", path}, flags...)
	if out, err := exec.CommandContext(ctx, r.Bin, args...).CombinedOutput(); err != nil {
		return nil, errors.New(firstNonEmpty(cleanLog(string(out)), err.Error()))
	}
	return r.dump(context.WithoutCancel(ctx), path)
}
