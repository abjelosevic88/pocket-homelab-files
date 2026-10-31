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
