package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Cloud is a connected drive: another cloud copied one way into its own read-only space.
type Cloud struct {
	ID            int64   `json:"id"`
	UserID        int64   `json:"-"`
	Kind          string  `json:"kind"`
	Name          string  `json:"name"`
	Login         string  `json:"login,omitempty"`
	Config        string  `json:"-"` // sealed
	Options       string  `json:"-"`
	IntervalHours int     `json:"interval_hours"`
	Status        string  `json:"status"`
	Error         string  `json:"error,omitempty"`
	LastSyncAt    *string `json:"last_sync_at,omitempty"`
	LastOKAt      *string `json:"last_ok_at,omitempty"`
	Files         int64   `json:"files"`
	Bytes         int64   `json:"bytes"`
	CreatedAt     string  `json:"created_at"`
}

const cloudCols = `id, user_id, kind, name, login, config, options, interval_hours, status, error, last_sync_at, last_ok_at, files, bytes, created_at`

func scanCloud(row interface{ Scan(...any) error }) (*Cloud, error) {
	var c Cloud
	if err := row.Scan(&c.ID, &c.UserID, &c.Kind, &c.Name, &c.Login, &c.Config, &c.Options, &c.IntervalHours, &c.Status, &c.Error,
		&c.LastSyncAt, &c.LastOKAt, &c.Files, &c.Bytes, &c.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &c, nil
}

func (s *Store) CreateCloud(ctx context.Context, c *Cloud) error {
	if c.Options == "" {
		c.Options = "{}"
	}
	if c.Status == "" {
		c.Status = "idle"
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO clouds (user_id, kind, name, login, config, options, interval_hours, status) VALUES (?,?,?,?,?,?,?,?)`,
		c.UserID, c.Kind, c.Name, c.Login, c.Config, c.Options, c.IntervalHours, c.Status)
	if err != nil {
		return err
	}
	c.ID, _ = res.LastInsertId()
	got, err := s.CloudFor(ctx, c.UserID, c.ID)
	if err == nil {
		*c = *got
	}
	return err
}

// CloudFor is one of the person's drives; anyone else's answers ErrNotFound.
func (s *Store) CloudFor(ctx context.Context, uid, id int64) (*Cloud, error) {
	return scanCloud(s.DB.QueryRowContext(ctx, `SELECT `+cloudCols+` FROM clouds WHERE id = ? AND user_id = ?`, id, uid))
}

func (s *Store) CloudByID(ctx context.Context, id int64) (*Cloud, error) {
	return scanCloud(s.DB.QueryRowContext(ctx, `SELECT `+cloudCols+` FROM clouds WHERE id = ?`, id))
}

func (s *Store) CloudsFor(ctx context.Context, uid int64) ([]Cloud, error) {
	return s.clouds(ctx, `SELECT `+cloudCols+` FROM clouds WHERE user_id = ? ORDER BY lower(name), id`, uid)
}

// AllClouds is every drive on the box, for the scheduler.
func (s *Store) AllClouds(ctx context.Context) ([]Cloud, error) {
	return s.clouds(ctx, `SELECT `+cloudCols+` FROM clouds ORDER BY id`)
}

func (s *Store) clouds(ctx context.Context, q string, args ...any) ([]Cloud, error) {
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Cloud{}
	for rows.Next() {
		c, err := scanCloud(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func (s *Store) UpdateCloud(ctx context.Context, c *Cloud) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE clouds SET name = ?, login = ?, options = ?, interval_hours = ? WHERE id = ? AND user_id = ?`,
		c.Name, c.Login, c.Options, c.IntervalHours, c.ID, c.UserID)
	return err
}

func (s *Store) SetCloudConfig(ctx context.Context, id int64, sealed string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE clouds SET config = ? WHERE id = ?`, sealed, id)
	return err
}

func (s *Store) SetCloudLogin(ctx context.Context, id int64, login string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE clouds SET login = ? WHERE id = ?`, login, id)
	return err
}

func (s *Store) SetCloudStatus(ctx context.Context, id int64, status, msg string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE clouds SET status = ?, error = ? WHERE id = ?`, status, msg, id)
	return err
}

// FinishCloudSync records a run: when, how it went, and what the copy now holds.
func (s *Store) FinishCloudSync(ctx context.Context, id int64, status, msg string, files, bytes int64, ok bool) error {
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	q := `UPDATE clouds SET status = ?, error = ?, last_sync_at = ?, files = ?, bytes = ? WHERE id = ?`
	args := []any{status, msg, now, files, bytes, id}
	if ok {
		q = `UPDATE clouds SET status = ?, error = ?, last_sync_at = ?, files = ?, bytes = ?, last_ok_at = ? WHERE id = ?`
		args = []any{status, msg, now, files, bytes, now, id}
	}
	_, err := s.DB.ExecContext(ctx, q, args...)
	return err
}

// ResetSyncingClouds runs at start: a sync that was running when the app stopped did not finish.
func (s *Store) ResetSyncingClouds(ctx context.Context) {
	_, _ = s.DB.ExecContext(ctx, `UPDATE clouds SET status = 'idle' WHERE status = 'syncing'`)
}

func (s *Store) DeleteCloud(ctx context.Context, uid, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM clouds WHERE id = ? AND user_id = ?`, id, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CloudDoc is a native Google Doc as the two-way sync last left it.
type CloudDoc struct {
	Path, FileID, Format string
	RemoteMtime          time.Time
	LocalMtime           int64
	LocalSize            int64
}

func (s *Store) CloudDocs(ctx context.Context, cloudID int64) (map[string]CloudDoc, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT path, file_id, format, remote_mtime, local_mtime, local_size FROM cloud_docs WHERE cloud_id = ?`, cloudID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]CloudDoc{}
	for rows.Next() {
		var d CloudDoc
		var rm string
		if err := rows.Scan(&d.Path, &d.FileID, &d.Format, &rm, &d.LocalMtime, &d.LocalSize); err != nil {
			return nil, err
		}
		d.RemoteMtime, _ = time.Parse(time.RFC3339Nano, rm)
		out[d.Path] = d
	}
	return out, rows.Err()
}

func (s *Store) SaveCloudDoc(ctx context.Context, cloudID int64, d CloudDoc) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO cloud_docs (cloud_id, path, file_id, format, remote_mtime, local_mtime, local_size) VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(cloud_id, path) DO UPDATE SET file_id = excluded.file_id, format = excluded.format, remote_mtime = excluded.remote_mtime,
		local_mtime = excluded.local_mtime, local_size = excluded.local_size`,
		cloudID, d.Path, d.FileID, d.Format, d.RemoteMtime.UTC().Format(time.RFC3339Nano), d.LocalMtime, d.LocalSize)
	return err
}

func (s *Store) DeleteCloudDoc(ctx context.Context, cloudID int64, path string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM cloud_docs WHERE cloud_id = ? AND path = ?`, cloudID, path)
	return err
}

func (s *Store) ClearCloudDocs(ctx context.Context, cloudID int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM cloud_docs WHERE cloud_id = ?`, cloudID)
	return err
}

// CloudConflict is a file changed on both sides, waiting for the person to pick.
type CloudConflict struct {
	ID         int64  `json:"id"`
	CloudID    int64  `json:"cloud_id"`
	Path       string `json:"path"`
	CloudName  string `json:"cloud_name"`
	PocketName string `json:"pocket_name"`
	Doc        bool   `json:"doc"`
	CreatedAt  string `json:"created_at"`
}

func (s *Store) AddCloudConflict(ctx context.Context, c CloudConflict) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO cloud_conflicts (cloud_id, path, cloud_name, pocket_name, doc) VALUES (?,?,?,?,?)
		ON CONFLICT(cloud_id, path) DO UPDATE SET cloud_name = excluded.cloud_name, pocket_name = excluded.pocket_name, doc = excluded.doc`,
		c.CloudID, c.Path, c.CloudName, c.PocketName, c.Doc)
	return err
}

func (s *Store) CloudConflicts(ctx context.Context, cloudID int64) ([]CloudConflict, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, cloud_id, path, cloud_name, pocket_name, doc, created_at FROM cloud_conflicts WHERE cloud_id = ? ORDER BY path`, cloudID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CloudConflict{}
	for rows.Next() {
		var c CloudConflict
		if err := rows.Scan(&c.ID, &c.CloudID, &c.Path, &c.CloudName, &c.PocketName, &c.Doc, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) CloudConflict(ctx context.Context, cloudID, id int64) (*CloudConflict, error) {
	var c CloudConflict
	err := s.DB.QueryRowContext(ctx, `SELECT id, cloud_id, path, cloud_name, pocket_name, doc, created_at FROM cloud_conflicts WHERE cloud_id = ? AND id = ?`, cloudID, id).
		Scan(&c.ID, &c.CloudID, &c.Path, &c.CloudName, &c.PocketName, &c.Doc, &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &c, err
}

func (s *Store) DeleteCloudConflict(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM cloud_conflicts WHERE id = ?`, id)
	return err
}

func (s *Store) CountCloudConflicts(ctx context.Context, cloudID int64) int {
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM cloud_conflicts WHERE cloud_id = ?`, cloudID).Scan(&n)
	return n
}
