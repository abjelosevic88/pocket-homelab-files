// Package clouds connects other clouds (Google Drive, Dropbox, OneDrive, iCloud Drive and any
// rclone remote) and copies them one way into Pocket Files with rclone.
package clouds

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Remote is the name every generated config gives the drive's remote.
const Remote = "drive"

// Rclone runs the rclone binary against a throwaway config file per call.
type Rclone struct {
	Bin string
	Dir string // where config files live while rclone runs (0600, removed afterwards)
}

// Version reports the rclone version, or an error when rclone is missing.
func (r *Rclone) Version(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, r.Bin, "version").Output()
	if err != nil {
		return "", fmt.Errorf("rclone is not available (%s): %w", r.Bin, err)
	}
	line, _, _ := strings.Cut(string(out), "\n")
	return strings.TrimSpace(line), nil
}

// Config is one remote's options, as rclone's config file holds them.
type Config map[string]string

// WriteINI renders a config file holding just this remote.
func WriteINI(w io.Writer, name string, c Config) error {
	keys := make([]string, 0, len(c))
	for k := range c {
		if strings.ContainsAny(k, "\n\r=[]") {
			return fmt.Errorf("bad option name %q", k)
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Fprintf(w, "[%s]\n", name)
	for _, k := range keys {
		v := c[k]
		if strings.ContainsAny(v, "\n\r") {
			return fmt.Errorf("option %s spans lines", k)
		}
		fmt.Fprintf(w, "%s = %s\n", k, v)
	}
	return nil
}

// ParseINI reads a pasted rclone remote ("[name]" then "key = value" lines). The section
// name is ignored; one remote only.
func ParseINI(text string) (Config, error) {
	c := Config{}
	sections := 0
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";"):
		case strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]"):
			sections++
			if sections > 1 {
				return nil, errors.New("paste one remote only")
			}
		default:
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				return nil, fmt.Errorf("not an option line: %q", line)
			}
			c[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	if c["type"] == "" {
		return nil, errors.New("the remote needs a type (for example type = sftp)")
	}
	return c, nil
}

// session writes a config file for one call and returns its path and a cleanup.
func (r *Rclone) session(c Config) (string, func(), error) {
	if err := os.MkdirAll(r.Dir, 0o700); err != nil {
		return "", nil, err
	}
	f, err := os.CreateTemp(r.Dir, "remote-*.conf")
	if err != nil {
		return "", nil, err
	}
	if err := WriteINI(f, Remote, c); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", nil, err
	}
	return f.Name(), func() { os.Remove(f.Name()) }, nil
}

// dump reads the remote back after rclone ran: it rewrites refreshed tokens and cookies.
func (r *Rclone) dump(ctx context.Context, path string) (Config, error) {
	out, err := exec.CommandContext(ctx, r.Bin, "config", "dump", "--config", path).Output()
	if err != nil {
		return nil, fmt.Errorf("rclone config dump: %w", err)
	}
	var all map[string]Config
	if err := json.Unmarshal(out, &all); err != nil {
		return nil, err
	}
	c, ok := all[Remote]
	if !ok {
		return nil, errors.New("the remote vanished from rclone's config")
	}
	return c, nil
}

// Stats is rclone's progress report (--use-json-log).
type Stats struct {
	Bytes          int64   `json:"bytes"`
	TotalBytes     int64   `json:"totalBytes"`
	Transfers      int64   `json:"transfers"`
	TotalTransfers int64   `json:"totalTransfers"`
	Checks         int64   `json:"checks"`
	TotalChecks    int64   `json:"totalChecks"`
	Errors         int64   `json:"errors"`
	Speed          float64 `json:"speed"`
	ETA            *int64  `json:"eta"`
	LastError      string  `json:"lastError"`
}

type logLine struct {
	Level string `json:"level"`
	Msg   string `json:"msg"`
	Stats *Stats `json:"stats"`
}

// Job is one copy.
type Job struct {
	Config Config   // the remote
	Source string   // path inside the remote ("" = its root)
	Dest   string   // local folder
	Files  []string // only these paths (relative to Source), when given
	Flags  []string // extra rclone flags
}

// Result is how a copy went.
type Result struct {
	Stats  Stats
	Config Config   // the remote after the run (tokens may have been refreshed)
	Errors []string // last few error lines
}

// Copy copies Job.Source into Job.Dest, never deleting anything in Dest. onStats gets
// progress every few seconds.
func (r *Rclone) Copy(ctx context.Context, job Job, onStats func(Stats)) (*Result, error) {
	path, done, err := r.session(job.Config)
	if err != nil {
		return nil, err
	}
	defer done()
	if err := os.MkdirAll(job.Dest, 0o755); err != nil {
		return nil, err
	}
	args := []string{"copy", Remote + ":" + strings.TrimPrefix(job.Source, "/"), job.Dest,
		"--config", path, "--use-json-log", "--log-level", "NOTICE", "--stats", "5s", "--stats-log-level", "NOTICE",
		"--create-empty-src-dirs", "--retries", "3", "--low-level-retries", "10"}
	if len(job.Files) > 0 {
		f, err := os.CreateTemp(r.Dir, "files-*.txt")
		if err != nil {
			return nil, err
		}
		for _, p := range job.Files {
			fmt.Fprintln(f, p)
		}
		f.Close()
		defer os.Remove(f.Name())
		args = append(args, "--files-from-raw", f.Name())
	}
	args = append(args, job.Flags...)
	cmd := exec.CommandContext(ctx, r.Bin, args...)
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 20 * time.Second
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start rclone: %w", err)
	}
	res := &Result{}
	sc := bufio.NewScanner(stderr)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		var l logLine
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			if t := strings.TrimSpace(sc.Text()); t != "" {
				res.Errors = appendTail(res.Errors, t)
			}
			continue
		}
		if l.Stats != nil {
			res.Stats = *l.Stats
			if onStats != nil {
				onStats(*l.Stats)
			}
		}
		if l.Level == "error" || l.Level == "critical" {
			res.Errors = appendTail(res.Errors, l.Msg)
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
		if IsAuthMessage(strings.Join(res.Errors, "\n") + "\n" + res.Stats.LastError) {
			return res, &AuthError{Msg: msg}
		}
		return res, errors.New(msg)
	}
	return res, nil
}

// Check lists the remote's root once: proves a pasted remote or a fresh sign-in works.
func (r *Rclone) Check(ctx context.Context, c Config) (Config, error) {
	path, done, err := r.session(c)
	if err != nil {
		return nil, err
	}
	defer done()
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, r.Bin, "lsf", Remote+":", "--max-depth", "1", "--config", path, "--low-level-retries", "2")
	cmd.Stdout, cmd.Stderr = io.Discard, &stderr
	if err := cmd.Run(); err != nil {
		msg := cleanLog(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		if IsAuthMessage(msg) {
			return nil, &AuthError{Msg: msg}
		}
		return nil, errors.New(msg)
	}
	return r.dump(ctx, path)
}

// Question is what a non-interactive `rclone config` asks next (iCloud's 2FA code).
type Question struct {
	State string `json:"State"`
	Name  string `json:"name"`
	Help  string `json:"help"`
	Error string `json:"error,omitempty"`
}

// ConfigStep runs `rclone config create` (state == "") or `update --continue` with an answer.
// It returns the remote once rclone has all it needs, or the next question.
func (r *Rclone) ConfigStep(ctx context.Context, c Config, state, answer string) (Config, *Question, error) {
	path, done, err := r.session(c)
	if err != nil {
		return nil, nil, err
	}
	defer done()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var args []string
	if state == "" {
		args = []string{"config", "create", Remote, c["type"]}
		for k, v := range c {
			if k != "type" {
				args = append(args, k+"="+v)
			}
		}
		args = append(args, "--non-interactive", "--obscure")
	} else {
		args = []string{"config", "update", Remote, "--continue", "--state", state, "--result", answer, "--non-interactive"}
	}
	args = append(args, "--config", path)
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, r.Bin, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	var out struct {
		State  string `json:"State"`
		Option *struct {
			Name string `json:"Name"`
			Help string `json:"Help"`
		} `json:"Option"`
		Error string `json:"Error"`
	}
	if s := strings.TrimSpace(stdout.String()); s != "" {
		_ = json.Unmarshal([]byte(s[strings.Index(s, "{"):]), &out)
	}
	if out.Option != nil && out.State != "" {
		help, _, _ := strings.Cut(out.Option.Help, "\n")
		// The half-made remote is needed to continue; keep it with the question.
		partial, _ := r.dump(ctx, path)
		return partial, &Question{State: out.State, Name: out.Option.Name, Help: help, Error: out.Error}, nil
	}
	if out.Error != "" {
		return nil, nil, errors.New(out.Error)
	}
	if runErr != nil {
		return nil, nil, errors.New(firstNonEmpty(cleanLog(stderr.String()), runErr.Error()))
	}
	got, err := r.dump(ctx, path)
	return got, nil, err
}

var authRe = regexp.MustCompile(`(?i)invalid_grant|unauthorized|401|token (has been )?(expired|revoked)|couldn't fetch token|failed to get token|re-?authenticate|reconnect|trust token|2fa|missing_scope|expired_access_token|invalid_access_token|AADSTS`)

// IsAuthMessage reports whether rclone's complaint means "sign in again".
func IsAuthMessage(s string) bool { return authRe.MatchString(s) }

func cleanLog(s string) string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		// "2026/10/31 08:00:00 ERROR : drive: ..." → the message
		if i := strings.Index(l, " : "); i >= 0 && i < 60 {
			l = strings.TrimSpace(l[i+3:])
		}
		lines = append(lines, l)
	}
	return strings.Join(lines, "\n")
}

func appendTail(list []string, s string) []string {
	list = append(list, s)
	if len(list) > 8 {
		list = list[len(list)-8:]
	}
	return list
}

func last(list []string) string {
	if len(list) == 0 {
		return ""
	}
	return list[len(list)-1]
}
