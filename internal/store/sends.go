package store

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"time"
)

// NewID returns a random URL-safe id of n characters (no look-alikes).
func NewID(n int) string {
	const alphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, n)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

const sendCols = `id, owner_id, kind, encrypted, label, meta, live_space, live_paths, file_count, total_size, has_password, password_hash, password_salt, auth_hash, expires_at, max_downloads, downloads, notify, status, last_download_at, created_at`

func scanSend(row interface{ Scan(...any) error }) (*Send, error) {
	var s Send
	var enc, pw, notify int
	var paths string
	if err := row.Scan(&s.ID, &s.OwnerID, &s.Kind, &enc, &s.Label, &s.Meta, &s.LiveSpace, &paths, &s.FileCount, &s.TotalSize, &pw, &s.PasswordHash, &s.PasswordSalt, &s.AuthHash, &s.ExpiresAt, &s.MaxDownloads, &s.Downloads, &notify, &s.Status, &s.LastDownloadAt, &s.CreatedAt); err != nil {
		return nil, notFound(err)
	}
	s.Encrypted, s.HasPassword, s.Notify = enc == 1, pw == 1, notify == 1
	_ = json.Unmarshal([]byte(paths), &s.LivePaths)
	return &s, nil
}

// CreateSend stores a new send with its file slots.
func (s *Store) CreateSend(ctx context.Context, sd *Send) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	for i := 0; ; i++ {
		sd.ID = NewID(10)
		_, err = tx.ExecContext(ctx, `INSERT INTO sends (id, owner_id, kind, encrypted, label, meta, live_space, live_paths, file_count, total_size, has_password, password_hash, password_salt, auth_hash, expires_at, max_downloads, notify, status)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			sd.ID, sd.OwnerID, sd.Kind, boolInt(sd.Encrypted), sd.Label, sd.Meta, sd.LiveSpace, jsonMarshal(nonNil(sd.LivePaths)), sd.FileCount, sd.TotalSize,
			boolInt(sd.HasPassword), sd.PasswordHash, sd.PasswordSalt, sd.AuthHash, sd.ExpiresAt, sd.MaxDownloads, boolInt(sd.Notify), sd.Status)
		if err == nil || i > 5 || conflict(err) != ErrConflict {
			break
		}
	}
	if err != nil {
		return err
	}
	for _, f := range sd.Files {
		if _, err := tx.ExecContext(ctx, `INSERT INTO send_files (send_id, idx, size, expected, name, type) VALUES (?, ?, 0, ?, ?, ?)`, sd.ID, f.Idx, f.Expected, f.Name, f.Type); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// GetSend loads a send with its files (no owner check: public pages use it).
func (s *Store) GetSend(ctx context.Context, id string) (*Send, error) {
	sd, err := scanSend(s.DB.QueryRowContext(ctx, `SELECT `+sendCols+` FROM sends WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT idx, size, expected, name, type FROM send_files WHERE send_id = ? ORDER BY idx`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var f SendFile
		if err := rows.Scan(&f.Idx, &f.Size, &f.Expected, &f.Name, &f.Type); err != nil {
			return nil, err
		}
		sd.Files = append(sd.Files, f)
	}
	return sd, rows.Err()
}

// OwnSend loads one of the owner's sends; anyone else's answers not found.
func (s *Store) OwnSend(ctx context.Context, owner int64, id string) (*Send, error) {
	sd, err := s.GetSend(ctx, id)
	if err != nil {
		return nil, err
	}
	if sd.OwnerID != owner {
		return nil, ErrNotFound
	}
	return sd, nil
}

// ListSends returns the owner's sends, newest first.
func (s *Store) ListSends(ctx context.Context, owner int64) ([]Send, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+sendCols+` FROM sends WHERE owner_id = ? ORDER BY created_at DESC`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Send{}
	for rows.Next() {
		sd, err := scanSend(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sd)
	}
	return out, rows.Err()
}

// SetSendFileSize records how many bytes of one file are stored.
func (s *Store) SetSendFileSize(ctx context.Context, id string, idx int, size int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE send_files SET size = ? WHERE send_id = ? AND idx = ?`, size, id, idx)
	return err
}

// FinishSend marks the upload complete.
func (s *Store) FinishSend(ctx context.Context, id string, total int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE sends SET status = 'ready', total_size = ? WHERE id = ?`, total, id)
	return err
}

// UpdateSendLimits changes expiry, download limit, notify and label.
func (s *Store) UpdateSendLimits(ctx context.Context, sd *Send) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE sends SET label = ?, expires_at = ?, max_downloads = ?, notify = ? WHERE id = ? AND owner_id = ?`, sd.Label, sd.ExpiresAt, sd.MaxDownloads, boolInt(sd.Notify), sd.ID, sd.OwnerID)
	return err
}

// DeleteSend removes a send row (blobs are the caller's).
func (s *Store) DeleteSend(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sends WHERE id = ?`, id)
	return err
}

// TakeTicket counts one download and returns a ticket that unlocks the
// files for ttl. It refuses when the send is used up or expired.
func (s *Store) TakeTicket(ctx context.Context, id, ip, agent string, ttl time.Duration) (string, *Send, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	sd, err := scanSend(tx.QueryRowContext(ctx, `SELECT `+sendCols+` FROM sends WHERE id = ?`, id))
	if err != nil {
		return "", nil, err
	}
	if !SendAvailable(sd) {
		return "", sd, ErrGone
	}
	now := Now()
	if _, err := tx.ExecContext(ctx, `UPDATE sends SET downloads = downloads + 1, last_download_at = ? WHERE id = ?`, now, id); err != nil {
		return "", nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO send_log (send_id, at, ip, agent) VALUES (?, ?, ?, ?)`, id, now, ip, truncate(agent, 300)); err != nil {
		return "", nil, err
	}
	tok := RandomToken(24)
	if _, err := tx.ExecContext(ctx, `INSERT INTO send_tickets (token_hash, send_id, expires_at) VALUES (?, ?, ?)`, HashToken(tok), id, time.Now().Add(ttl).UTC().Format(time.RFC3339)); err != nil {
		return "", nil, err
	}
	if err := tx.Commit(); err != nil {
		return "", nil, err
	}
	sd.Downloads++
	sd.LastDownloadAt = now
	return tok, sd, nil
}

// TicketValid reports whether a ticket unlocks this send right now.
func (s *Store) TicketValid(ctx context.Context, id, tok string) bool {
	if tok == "" {
		return false
	}
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM send_tickets WHERE token_hash = ? AND send_id = ? AND expires_at > ?`, HashToken(tok), id, time.Now().UTC().Format(time.RFC3339)).Scan(&n)
	return n > 0
}

// SendLog lists the counted downloads of a send.
func (s *Store) SendLog(ctx context.Context, id string) []SendVisit {
	rows, err := s.DB.QueryContext(ctx, `SELECT at, ip, agent FROM send_log WHERE send_id = ? ORDER BY id DESC LIMIT 100`, id)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []SendVisit{}
	for rows.Next() {
		var v SendVisit
		if rows.Scan(&v.At, &v.IP, &v.Agent) == nil {
			out = append(out, v)
		}
	}
	return out
}

// SendAvailable reports whether a send can still be downloaded.
func SendAvailable(sd *Send) bool {
	if sd.Status != "ready" {
		return false
	}
	if sd.ExpiresAt != "" && ParseTime(sd.ExpiresAt).Before(time.Now()) {
		return false
	}
	if sd.MaxDownloads > 0 && sd.Downloads >= sd.MaxDownloads {
		return false
	}
	return true
}

// DeadSends lists sends whose blobs can go: expired or used up with no
// ticket still open, and uploads abandoned for a day.
func (s *Store) DeadSends(ctx context.Context) []string {
	now := time.Now().UTC()
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM sends s WHERE
		((s.expires_at <> '' AND s.expires_at < ?) OR (s.max_downloads > 0 AND s.downloads >= s.max_downloads) OR (s.status = 'uploading' AND s.created_at < ?))
		AND NOT EXISTS (SELECT 1 FROM send_tickets t WHERE t.send_id = s.id AND t.expires_at > ?)`,
		now.Format(time.RFC3339), now.Add(-24*time.Hour).Format(time.RFC3339Nano), now.Format(time.RFC3339))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out
}

// PurgeTickets drops expired tickets.
func (s *Store) PurgeTickets(ctx context.Context) {
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM send_tickets WHERE expires_at <= ?`, time.Now().UTC().Format(time.RFC3339))
}

// ---------- requests ----------

const requestCols = `id, owner_id, title, message, space, path, expires_at, max_files, max_file_mb, password_hash, notify, closed, files_received, bytes_received, last_upload_at, created_at`

func scanRequest(row interface{ Scan(...any) error }) (*Request, error) {
	var r Request
	var notify, closed int
	if err := row.Scan(&r.ID, &r.OwnerID, &r.Title, &r.Message, &r.Space, &r.Path, &r.ExpiresAt, &r.MaxFiles, &r.MaxFileMB, &r.PasswordHash, &notify, &closed, &r.FilesReceived, &r.BytesReceived, &r.LastUploadAt, &r.CreatedAt); err != nil {
		return nil, notFound(err)
	}
	r.Notify, r.Closed, r.HasPassword = notify == 1, closed == 1, r.PasswordHash != ""
	return &r, nil
}

// SaveRequest creates (ID empty) or updates a request.
func (s *Store) SaveRequest(ctx context.Context, r *Request) error {
	if r.ID == "" {
		r.ID = NewID(10)
		_, err := s.DB.ExecContext(ctx, `INSERT INTO requests (id, owner_id, title, message, space, path, expires_at, max_files, max_file_mb, password_hash, notify) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.ID, r.OwnerID, r.Title, r.Message, r.Space, r.Path, r.ExpiresAt, r.MaxFiles, r.MaxFileMB, r.PasswordHash, boolInt(r.Notify))
		return err
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE requests SET title = ?, message = ?, space = ?, path = ?, expires_at = ?, max_files = ?, max_file_mb = ?, password_hash = ?, notify = ?, closed = ? WHERE id = ? AND owner_id = ?`,
		r.Title, r.Message, r.Space, r.Path, r.ExpiresAt, r.MaxFiles, r.MaxFileMB, r.PasswordHash, boolInt(r.Notify), boolInt(r.Closed), r.ID, r.OwnerID)
	return err
}

// GetRequest loads a request (no owner check: the public page uses it).
func (s *Store) GetRequest(ctx context.Context, id string) (*Request, error) {
	return scanRequest(s.DB.QueryRowContext(ctx, `SELECT `+requestCols+` FROM requests WHERE id = ?`, id))
}

// OwnRequest loads one of the owner's requests.
func (s *Store) OwnRequest(ctx context.Context, owner int64, id string) (*Request, error) {
	return scanRequest(s.DB.QueryRowContext(ctx, `SELECT `+requestCols+` FROM requests WHERE id = ? AND owner_id = ?`, id, owner))
}

// ListRequests returns the owner's requests, newest first.
func (s *Store) ListRequests(ctx context.Context, owner int64) ([]Request, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+requestCols+` FROM requests WHERE owner_id = ? ORDER BY created_at DESC`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Request{}
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// DeleteRequest removes one of the owner's requests (received files stay).
func (s *Store) DeleteRequest(ctx context.Context, owner int64, id string) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM requests WHERE id = ? AND owner_id = ?`, id, owner)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CountRequestUpload records one file received.
func (s *Store) CountRequestUpload(ctx context.Context, id string, size int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE requests SET files_received = files_received + 1, bytes_received = bytes_received + ?, last_upload_at = ? WHERE id = ?`, size, Now(), id)
	return err
}

// RequestOpen reports whether a request still takes files.
func RequestOpen(r *Request) bool {
	if r.Closed {
		return false
	}
	if r.ExpiresAt != "" && ParseTime(r.ExpiresAt).Before(time.Now()) {
		return false
	}
	if r.MaxFiles > 0 && r.FilesReceived >= r.MaxFiles {
		return false
	}
	return true
}

// ---------- uploads in pieces ----------

// CreateUpload starts a piecewise upload.
func (s *Store) CreateUpload(ctx context.Context, u *Upload) error {
	u.ID = NewID(16)
	_, err := s.DB.ExecContext(ctx, `INSERT INTO uploads (id, owner, name, size, received, temp, extra) VALUES (?, ?, ?, ?, 0, ?, ?)`, u.ID, u.Owner, u.Name, u.Size, u.Temp, u.Extra)
	return err
}

// GetUpload loads an upload of one owner.
func (s *Store) GetUpload(ctx context.Context, owner, id string) (*Upload, error) {
	var u Upload
	err := s.DB.QueryRowContext(ctx, `SELECT id, owner, name, size, received, temp, extra FROM uploads WHERE id = ? AND owner = ?`, id, owner).Scan(&u.ID, &u.Owner, &u.Name, &u.Size, &u.Received, &u.Temp, &u.Extra)
	if err != nil {
		return nil, notFound(err)
	}
	return &u, nil
}

// SetUploadReceived records progress.
func (s *Store) SetUploadReceived(ctx context.Context, id string, n int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE uploads SET received = ? WHERE id = ?`, n, id)
	return err
}

// DeleteUpload forgets an upload (the temp file is the caller's).
func (s *Store) DeleteUpload(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM uploads WHERE id = ?`, id)
	return err
}

// StaleUploads lists uploads untouched for a day.
func (s *Store) StaleUploads(ctx context.Context) []Upload {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, owner, name, size, received, temp, extra FROM uploads WHERE created_at < ?`, time.Now().Add(-24*time.Hour).UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Upload
	for rows.Next() {
		var u Upload
		if rows.Scan(&u.ID, &u.Owner, &u.Name, &u.Size, &u.Received, &u.Temp, &u.Extra) == nil {
			out = append(out, u)
		}
	}
	return out
}

// ---------- trash ----------

// AddTrash records an item moved to the trash.
func (s *Store) AddTrash(ctx context.Context, t *TrashItem) error {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO trash (user_id, name, orig_path, stored, is_dir, size, deleted_by) VALUES (?, ?, ?, ?, ?, ?, ?)`, t.UserID, t.Name, t.OrigPath, t.Stored, boolInt(t.IsDir), t.Size, t.DeletedBy)
	if err != nil {
		return err
	}
	t.ID, _ = res.LastInsertId()
	return nil
}

const trashCols = `id, user_id, name, orig_path, stored, is_dir, size, COALESCE(deleted_by, 0), deleted_at`

func scanTrash(row interface{ Scan(...any) error }) (*TrashItem, error) {
	var t TrashItem
	var d int
	if err := row.Scan(&t.ID, &t.UserID, &t.Name, &t.OrigPath, &t.Stored, &d, &t.Size, &t.DeletedBy, &t.DeletedAt); err != nil {
		return nil, notFound(err)
	}
	t.IsDir = d == 1
	return &t, nil
}

// ListTrash lists a person's trash, newest first.
func (s *Store) ListTrash(ctx context.Context, uid int64) ([]TrashItem, error) {
	return s.trashQuery(ctx, `SELECT `+trashCols+` FROM trash WHERE user_id = ? ORDER BY deleted_at DESC`, uid)
}

func (s *Store) trashQuery(ctx context.Context, q string, args ...any) ([]TrashItem, error) {
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TrashItem{}
	for rows.Next() {
		t, err := scanTrash(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// GetTrash loads one of a person's trash items.
func (s *Store) GetTrash(ctx context.Context, uid, id int64) (*TrashItem, error) {
	return scanTrash(s.DB.QueryRowContext(ctx, `SELECT `+trashCols+` FROM trash WHERE id = ? AND user_id = ?`, id, uid))
}

// DeleteTrash forgets a trash row.
func (s *Store) DeleteTrash(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM trash WHERE id = ?`, id)
	return err
}

// OldTrash lists a person's items deleted more than days ago.
func (s *Store) OldTrash(ctx context.Context, uid int64, days int) []TrashItem {
	out, _ := s.trashQuery(ctx, `SELECT `+trashCols+` FROM trash WHERE user_id = ? AND deleted_at < ?`, uid, time.Now().AddDate(0, 0, -days).UTC().Format(time.RFC3339Nano))
	return out
}

// ErrGone is returned when a send or request is used up or expired.
var ErrGone = sqlErr("gone")

type sqlErr string

func (e sqlErr) Error() string { return string(e) }

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
