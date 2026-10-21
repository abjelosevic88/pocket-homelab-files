package server

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/abjelosevic88/pocket-homelab-files/internal/store"
	"github.com/abjelosevic88/pocket-homelab-files/internal/vfs"
)

// A send is a link for anyone, after SkySend / Firefox Send:
//
//   - files or a note uploaded for the link, normally encrypted in the
//     browser (AES-256-GCM in 64 KiB records, keys from HKDF over a random
//     secret that only travels in the link's #fragment, so this server
//     stores ciphertext it cannot read). A password, when set, is mixed into
//     the key (PBKDF2), so the server cannot check it either: it only checks
//     an auth token derived from the same key.
//   - a live link to files that stay where they are (not encrypted; the
//     password is checked here).
//
// Every send has an expiry and an optional download limit; a download
// "ticket" counts once and unlocks every file for a few hours.

const ticketTTL = 6 * time.Hour

var hexHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

// parseExpiry turns "5m", "1h", "1d", "7d", "30d" or "never" into a time.
func (s *Server) parseExpiry(v string) (string, error) {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" {
		v = "7d"
	}
	if v == "never" {
		if s.cfg.MaxSendDays > 0 {
			return "", badRequest(fmt.Sprintf("links on this box last at most %d days", s.cfg.MaxSendDays))
		}
		return "", nil
	}
	var d time.Duration
	if strings.HasSuffix(v, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(v, "d"))
		if err != nil || n <= 0 {
			return "", badRequest("unknown expiry")
		}
		d = time.Duration(n) * 24 * time.Hour
	} else {
		var err error
		if d, err = time.ParseDuration(v); err != nil || d <= 0 {
			return "", badRequest("unknown expiry")
		}
	}
	if s.cfg.MaxSendDays > 0 && d > time.Duration(s.cfg.MaxSendDays)*24*time.Hour {
		return "", badRequest(fmt.Sprintf("links on this box last at most %d days", s.cfg.MaxSendDays))
	}
	return time.Now().Add(d).UTC().Format(time.RFC3339), nil
}

type sendView struct {
	store.Send
	URL       string `json:"url"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

func (s *Server) sendView(sd *store.Send) sendView {
	v := sendView{Send: *sd, URL: s.publicLink("s", sd.ID), Available: store.SendAvailable(sd)}
	v.Reason = sendReason(sd)
	v.Meta = ""
	return v
}

func sendReason(sd *store.Send) string {
	switch {
	case sd.Status != "ready":
		return "uploading"
	case sd.ExpiresAt != "" && store.ParseTime(sd.ExpiresAt).Before(time.Now()):
		return "expired"
	case sd.MaxDownloads > 0 && sd.Downloads >= sd.MaxDownloads:
		return "used up"
	}
	return ""
}

func (s *Server) handleListSends(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListSends(r.Context(), me(r))
	if err != nil {
		s.fail(w, err, "sends")
		return
	}
	out := make([]sendView, 0, len(list))
	for i := range list {
		out = append(out, s.sendView(&list[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetSend(w http.ResponseWriter, r *http.Request) {
	sd, err := s.store.OwnSend(r.Context(), me(r), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err, "send")
		return
	}
	sd.Log = s.store.SendLog(r.Context(), sd.ID)
	writeJSON(w, http.StatusOK, s.sendView(sd))
}

type sendIn struct {
	Kind         string           `json:"kind"`
	Encrypted    bool             `json:"encrypted"`
	Label        string           `json:"label"`
	Meta         string           `json:"meta"`
	Files        []store.SendFile `json:"files"`
	Live         *liveIn          `json:"live"`
	Expires      string           `json:"expires"`
	MaxDownloads int              `json:"max_downloads"`
	Notify       bool             `json:"notify"`
	Password     string           `json:"password"`
	PasswordSalt string           `json:"password_salt"`
	AuthHash     string           `json:"auth_hash"`
}

type liveIn struct {
	Space string   `json:"space"`
	Paths []string `json:"paths"`
}

func (s *Server) handleCreateSend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in sendIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	exp, err := s.parseExpiry(in.Expires)
	if err != nil {
		s.fail(w, err, "send")
		return
	}
	if in.MaxDownloads < 0 || in.MaxDownloads > 100000 {
		in.MaxDownloads = 0
	}
	sd := &store.Send{OwnerID: me(r), Kind: in.Kind, Encrypted: in.Encrypted, Label: strings.TrimSpace(in.Label), ExpiresAt: exp, MaxDownloads: in.MaxDownloads, Notify: in.Notify, Status: "uploading"}
	switch in.Kind {
	case "live":
		if in.Live == nil || len(in.Live.Paths) == 0 {
			writeErr(w, http.StatusBadRequest, "pick the files to link to")
			return
		}
		sp, err := s.space(ctx, me(r), in.Live.Space)
		if err != nil {
			s.fail(w, err, "open")
			return
		}
		sd.Encrypted, sd.LiveSpace, sd.Status = false, sp.ID, "ready"
		for _, p := range in.Live.Paths {
			e, err := sp.Stat(p)
			if err != nil {
				s.fail(w, err, "send")
				return
			}
			sd.LivePaths = append(sd.LivePaths, e.Path)
			sd.FileCount++
			sd.TotalSize += e.Size
			if sd.Label == "" {
				sd.Label = e.Name
			} else if len(in.Live.Paths) > 1 && sd.FileCount == 2 && in.Label == "" {
				sd.Label += fmt.Sprintf(" and %d more", len(in.Live.Paths)-1)
			}
		}
	case "files", "note":
		if len(in.Files) == 0 || len(in.Files) > 1000 {
			writeErr(w, http.StatusBadRequest, "a send holds 1 to 1000 files")
			return
		}
		var total int64
		for i := range in.Files {
			f := &in.Files[i]
			f.Idx = i
			if f.Expected < 0 {
				writeErr(w, http.StatusBadRequest, "bad file size")
				return
			}
			total += f.Expected
			if in.Encrypted {
				f.Name, f.Type = "", "" // names are inside the encrypted meta
			} else {
				f.Name = strings.TrimSpace(strings.ReplaceAll(f.Name, "\\", "/"))
				if f.Name == "" {
					f.Name = fmt.Sprintf("file-%d", i+1)
				}
			}
		}
		if s.cfg.MaxSendMB > 0 && total > s.cfg.MaxSendMB<<20 {
			writeErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("a send may hold up to %d MB", s.cfg.MaxSendMB))
			return
		}
		sd.Files, sd.FileCount, sd.TotalSize = in.Files, len(in.Files), total
	default:
		writeErr(w, http.StatusBadRequest, "kind is files, note or live")
		return
	}
	if sd.Encrypted {
		in.AuthHash = strings.ToLower(in.AuthHash)
		if !hexHash.MatchString(in.AuthHash) || in.Meta == "" || len(in.Meta) > 4<<20 {
			writeErr(w, http.StatusBadRequest, "an encrypted send needs its auth hash and encrypted metadata")
			return
		}
		sd.AuthHash, sd.Meta = in.AuthHash, in.Meta
		if in.PasswordSalt != "" {
			sd.HasPassword, sd.PasswordSalt = true, in.PasswordSalt
		}
	} else if in.Password != "" {
		h, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "password too long")
			return
		}
		sd.HasPassword, sd.PasswordHash = true, string(h)
	}
	if err := s.store.CreateSend(ctx, sd); err != nil {
		s.fail(w, err, "create send")
		return
	}
	if sd.Kind == "live" {
		s.sendCreated(ctx, sd)
	} else if err := os.MkdirAll(s.sendDir(sd.ID), 0o755); err != nil {
		s.fail(w, err, "create send")
		return
	}
	s.touch()
	got, _ := s.store.GetSend(ctx, sd.ID)
	writeJSON(w, http.StatusCreated, s.sendView(got))
}

func (s *Server) sendDir(id string) string { return filepath.Join(s.cfg.SendsDir, id) }

func (s *Server) sendBlob(id string, idx int) string {
	return filepath.Join(s.sendDir(id), strconv.Itoa(idx))
}

func (s *Server) sendCreated(ctx context.Context, sd *store.Send) {
	s.publish(ctx, "files.send_created", "send:"+sd.ID, sd.OwnerID, map[string]any{"id": sd.ID, "kind": sd.Kind, "encrypted": sd.Encrypted, "files": sd.FileCount, "size": sd.TotalSize, "expires_at": sd.ExpiresAt, "max_downloads": sd.MaxDownloads, "label": sd.Label})
}

// handleSendChunk appends one piece of a file: ?offset= must be the bytes
// already stored (a retried piece truncates back to its offset first).
func (s *Server) handleSendChunk(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sd, err := s.store.OwnSend(ctx, me(r), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err, "send")
		return
	}
	if sd.Status != "uploading" {
		writeErr(w, http.StatusConflict, "this send is already complete")
		return
	}
	idx, err := strconv.Atoi(chi.URLParam(r, "idx"))
	if err != nil || idx < 0 || idx >= len(sd.Files) {
		writeErr(w, http.StatusNotFound, "no such file in this send")
		return
	}
	offset, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	size, err := appendChunk(s.sendBlob(sd.ID, idx), offset, sd.Files[idx].Expected, r.Body)
	if err != nil {
		if ce, ok := err.(chunkErr); ok {
			writeJSON(w, ce.status, map[string]any{"error": ce.msg, "size": size})
			return
		}
		s.fail(w, err, "upload")
		return
	}
	_ = s.store.SetSendFileSize(ctx, sd.ID, idx, size)
	writeJSON(w, http.StatusOK, map[string]any{"size": size})
}

type chunkErr struct {
	status int
	msg    string
}

func (e chunkErr) Error() string { return e.msg }

// appendChunk writes body at offset of file p, never past limit.
func appendChunk(p string, offset, limit int64, body io.Reader) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return 0, err
	}
	size := fi.Size()
	if offset > size || offset < 0 {
		return size, chunkErr{http.StatusConflict, "offset does not match what is stored"}
	}
	if offset < size {
		if err := f.Truncate(offset); err != nil {
			return size, err
		}
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return offset, err
	}
	n, err := io.Copy(f, io.LimitReader(body, limit-offset+1))
	size = offset + n
	if size > limit {
		_ = f.Truncate(offset)
		return offset, chunkErr{http.StatusRequestEntityTooLarge, "more data than announced"}
	}
	if err != nil {
		return size, err
	}
	return size, f.Sync()
}

func (s *Server) handleFinishSend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sd, err := s.store.OwnSend(ctx, me(r), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err, "send")
		return
	}
	if sd.Status == "ready" {
		writeJSON(w, http.StatusOK, s.sendView(sd))
		return
	}
	var total int64
	for _, f := range sd.Files {
		fi, err := os.Stat(s.sendBlob(sd.ID, f.Idx))
		size := int64(0)
		if err == nil {
			size = fi.Size()
		}
		if size != f.Expected {
			writeErr(w, http.StatusConflict, fmt.Sprintf("file %d has %d of %d bytes", f.Idx+1, size, f.Expected))
			return
		}
		total += size
	}
	if err := s.store.FinishSend(ctx, sd.ID, total); err != nil {
		s.fail(w, err, "send")
		return
	}
	sd.Status, sd.TotalSize = "ready", total
	s.sendCreated(ctx, sd)
	s.touch()
	writeJSON(w, http.StatusOK, s.sendView(sd))
}

func (s *Server) handleUpdateSend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sd, err := s.store.OwnSend(ctx, me(r), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err, "send")
		return
	}
	var in struct {
		Label        *string `json:"label"`
		Expires      *string `json:"expires"`
		MaxDownloads *int    `json:"max_downloads"`
		Notify       *bool   `json:"notify"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Label != nil {
		sd.Label = strings.TrimSpace(*in.Label)
	}
	if in.Expires != nil {
		if *in.Expires == "now" {
			sd.ExpiresAt = time.Now().Add(-time.Second).UTC().Format(time.RFC3339)
		} else if sd.ExpiresAt, err = s.parseExpiry(*in.Expires); err != nil {
			s.fail(w, err, "send")
			return
		}
	}
	if in.MaxDownloads != nil && *in.MaxDownloads >= 0 {
		sd.MaxDownloads = *in.MaxDownloads
	}
	if in.Notify != nil {
		sd.Notify = *in.Notify
	}
	if err := s.store.UpdateSendLimits(ctx, sd); err != nil {
		s.fail(w, err, "send")
		return
	}
	s.touch()
	writeJSON(w, http.StatusOK, s.sendView(sd))
}

func (s *Server) handleDeleteSend(w http.ResponseWriter, r *http.Request) {
	sd, err := s.store.OwnSend(r.Context(), me(r), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err, "send")
		return
	}
	s.removeSend(r.Context(), sd.ID)
	s.touch()
	w.WriteHeader(http.StatusNoContent)
}

// removeSend deletes a send and its blobs (live sends own no blobs).
func (s *Server) removeSend(ctx context.Context, id string) {
	_ = os.RemoveAll(s.sendDir(id))
	_ = s.store.DeleteSend(ctx, id)
}

// ---------- public side ----------

type publicSend struct {
	ID           string           `json:"id"`
	Kind         string           `json:"kind"`
	Encrypted    bool             `json:"encrypted"`
	HasPassword  bool             `json:"has_password"`
	PasswordSalt string           `json:"password_salt,omitempty"`
	Meta         string           `json:"meta,omitempty"`
	FileCount    int              `json:"file_count"`
	TotalSize    int64            `json:"total_size"`
	ExpiresAt    string           `json:"expires_at"`
	DownloadsTop int              `json:"downloads_left"` // -1 = unlimited
	Available    bool             `json:"available"`
	Reason       string           `json:"reason,omitempty"`
	Files        []store.SendFile `json:"files,omitempty"`
	Entries      []vfs.Entry      `json:"entries,omitempty"`
	Ticket       string           `json:"ticket,omitempty"`
	Sizes        []int64          `json:"sizes,omitempty"`
}

func (s *Server) publicSendOf(ctx context.Context, sd *store.Send) publicSend {
	p := publicSend{ID: sd.ID, Kind: sd.Kind, Encrypted: sd.Encrypted, HasPassword: sd.HasPassword, FileCount: sd.FileCount, TotalSize: sd.TotalSize, ExpiresAt: sd.ExpiresAt, Available: store.SendAvailable(sd), Reason: sendReason(sd), DownloadsTop: -1}
	if sd.MaxDownloads > 0 {
		p.DownloadsTop = max(0, sd.MaxDownloads-sd.Downloads)
	}
	if !p.Available {
		return p
	}
	if sd.Encrypted {
		p.PasswordSalt, p.Meta = sd.PasswordSalt, sd.Meta
		for _, f := range sd.Files {
			p.Sizes = append(p.Sizes, f.Size)
		}
	} else if !sd.HasPassword {
		s.fillPlain(ctx, sd, &p)
	}
	return p
}

// fillPlain lists what an unencrypted send holds.
func (s *Server) fillPlain(ctx context.Context, sd *store.Send, p *publicSend) {
	if sd.Kind != "live" {
		p.Files = sd.Files
		return
	}
	sp, err := s.space(ctx, sd.OwnerID, sd.LiveSpace)
	if err != nil {
		p.Available, p.Reason = false, "the files are no longer there"
		return
	}
	for _, lp := range sd.LivePaths {
		if e, err := sp.Stat(lp); err == nil {
			p.Entries = append(p.Entries, *e)
		}
	}
	if len(p.Entries) == 0 {
		p.Available, p.Reason = false, "the files are no longer there"
	}
}

func (s *Server) handlePublicSend(w http.ResponseWriter, r *http.Request) {
	sd, err := s.store.GetSend(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "this link does not exist (any more)")
		return
	}
	writeJSON(w, http.StatusOK, s.publicSendOf(r.Context(), sd))
}

// handleOpenSend checks the auth token (encrypted) or password (plain),
// counts one download and hands out a ticket for the files.
func (s *Server) handleOpenSend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if !s.limiter.allowAttempt("send:"+id, clientIP(r)) {
		writeErr(w, http.StatusTooManyRequests, "too many wrong attempts — wait a few minutes")
		return
	}
	sd, err := s.store.GetSend(ctx, id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "this link does not exist (any more)")
		return
	}
	var in struct {
		Auth     string `json:"auth"`
		Password string `json:"password"`
	}
	_ = decode(r, &in)
	if sd.Encrypted {
		if !tokenMatches(in.Auth, sd.AuthHash) {
			s.limiter.fail("send:"+id, clientIP(r))
			msg := "the link is incomplete or wrong"
			if sd.HasPassword {
				msg = "wrong password"
			}
			writeErr(w, http.StatusUnauthorized, msg)
			return
		}
	} else if sd.HasPassword && bcrypt.CompareHashAndPassword([]byte(sd.PasswordHash), []byte(in.Password)) != nil {
		s.limiter.fail("send:"+id, clientIP(r))
		writeErr(w, http.StatusUnauthorized, "wrong password")
		return
	}
	tok, sd, err := s.store.TakeTicket(ctx, id, clientIP(r), r.UserAgent(), ticketTTL)
	if err != nil {
		s.fail(w, err, "open")
		return
	}
	full, _ := s.store.GetSend(ctx, id)
	if full != nil {
		sd = full
	}
	p := s.publicSendOf(ctx, sd)
	if !sd.Encrypted && sd.HasPassword {
		s.fillPlain(ctx, sd, &p)
	}
	p.Ticket, p.Available = tok, true
	s.sendOpened(ctx, sd, r)
	writeJSON(w, http.StatusOK, p)
}

func tokenMatches(tok, wantHex string) bool {
	if tok == "" || wantHex == "" {
		return false
	}
	got := store.HashToken(tok)
	a, _ := hex.DecodeString(got)
	b, _ := hex.DecodeString(wantHex)
	if len(a) != len(b) || len(a) == 0 {
		return false
	}
	var d byte
	for i := range a {
		d |= a[i] ^ b[i]
	}
	return d == 0
}

func (s *Server) sendOpened(ctx context.Context, sd *store.Send, r *http.Request) {
	left := "no limit"
	if sd.MaxDownloads > 0 {
		left = fmt.Sprintf("%d of %d", sd.Downloads, sd.MaxDownloads)
	}
	s.publish(ctx, "files.send_downloaded", "send:"+sd.ID, sd.OwnerID, map[string]any{"id": sd.ID, "label": sd.Label, "downloads": sd.Downloads, "max_downloads": sd.MaxDownloads})
	if sd.Notify {
		label := sd.Label
		if label == "" {
			label = "your link"
		} else {
			label = "“" + label + "”"
		}
		s.pending.add(sd.OwnerID, "sends", "send:"+sd.ID, "Link downloaded", fmt.Sprintf("Someone opened %s (%s).", label, left), s.cfg.BaseURL+"/send")
	}
}

// ticketSend loads a send and checks the ticket in ?t=.
func (s *Server) ticketSend(w http.ResponseWriter, r *http.Request) *store.Send {
	id := chi.URLParam(r, "id")
	if !s.store.TicketValid(r.Context(), id, r.URL.Query().Get("t")) {
		writeErr(w, http.StatusForbidden, "this download has expired — open the link again")
		return nil
	}
	sd, err := s.store.GetSend(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "this link does not exist (any more)")
		return nil
	}
	return sd
}

func (s *Server) handleSendFile(w http.ResponseWriter, r *http.Request) {
	sd := s.ticketSend(w, r)
	if sd == nil {
		return
	}
	idx, err := strconv.Atoi(chi.URLParam(r, "idx"))
	if err != nil || idx < 0 || idx >= len(sd.Files) {
		writeErr(w, http.StatusNotFound, "no such file")
		return
	}
	f, err := os.Open(s.sendBlob(sd.ID, idx))
	if err != nil {
		writeErr(w, http.StatusGone, "the file is gone")
		return
	}
	defer f.Close()
	fi, _ := f.Stat()
	if sd.Encrypted {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Cache-Control", "no-store")
		http.ServeContent(w, r, "", fi.ModTime(), f)
		return
	}
	serveFile(w, r, f, fi, pathBase(sd.Files[idx].Name), !queryBool(r, "inline"))
}

func pathBase(n string) string {
	if i := strings.LastIndex(n, "/"); i >= 0 {
		return n[i+1:]
	}
	return n
}

// liveTarget checks that p is one of the linked paths or inside one.
func liveTarget(sd *store.Send, p string) bool {
	p = vfs.Clean(p)
	for _, lp := range sd.LivePaths {
		if p == lp || strings.HasPrefix(p, strings.TrimSuffix(lp, "/")+"/") || lp == "/" {
			return true
		}
	}
	return false
}

func (s *Server) liveSpace(w http.ResponseWriter, r *http.Request) (*store.Send, *vfs.Space) {
	sd := s.ticketSend(w, r)
	if sd == nil {
		return nil, nil
	}
	if sd.Kind != "live" {
		writeErr(w, http.StatusNotFound, "not a live link")
		return nil, nil
	}
	sp, err := s.space(r.Context(), sd.OwnerID, sd.LiveSpace)
	if err != nil {
		writeErr(w, http.StatusGone, "the files are no longer there")
		return nil, nil
	}
	return sd, sp
}

func (s *Server) handleLiveList(w http.ResponseWriter, r *http.Request) {
	sd, sp := s.liveSpace(w, r)
	if sd == nil {
		return
	}
	p := r.URL.Query().Get("path")
	if !liveTarget(sd, p) {
		writeErr(w, http.StatusNotFound, "not part of this link")
		return
	}
	entries, err := sp.List(p, false)
	if err != nil {
		s.fail(w, err, "list")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": vfs.Clean(p), "entries": nonNilEntries(entries)})
}

func (s *Server) handleLiveFile(w http.ResponseWriter, r *http.Request) {
	sd, sp := s.liveSpace(w, r)
	if sd == nil {
		return
	}
	p := r.URL.Query().Get("path")
	if !liveTarget(sd, p) {
		writeErr(w, http.StatusNotFound, "not part of this link")
		return
	}
	f, fi, err := sp.Open(p)
	if err != nil {
		s.fail(w, err, "open")
		return
	}
	defer f.Close()
	if fi.IsDir() {
		writeErr(w, http.StatusBadRequest, "that is a folder")
		return
	}
	name := fi.Name()
	if sp.File != "" {
		name = sp.File
	}
	serveFile(w, r, f, fi, name, !queryBool(r, "inline"))
}

func (s *Server) handleLiveZip(w http.ResponseWriter, r *http.Request) {
	sd, sp := s.liveSpace(w, r)
	if sd == nil {
		return
	}
	paths := r.URL.Query()["path"]
	if len(paths) == 0 {
		paths = sd.LivePaths
	}
	for _, p := range paths {
		if !liveTarget(sd, p) {
			writeErr(w, http.StatusNotFound, "not part of this link")
			return
		}
	}
	name := firstNonEmpty(sd.Label, "Files")
	if len(paths) == 1 {
		if e, err := sp.Stat(paths[0]); err == nil {
			name = e.Name
		}
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", contentDisposition("attachment", name+".zip"))
	writeZip(r.Context(), w, sp, paths, false)
}
