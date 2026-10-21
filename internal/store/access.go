package store

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
)

// ---------- mounts ----------

const mountCols = `id, name, path, read_only, everyone_perm, color, COALESCE(created_by, 0), created_at`

func scanMount(row interface{ Scan(...any) error }) (*Mount, error) {
	var m Mount
	var ro int
	if err := row.Scan(&m.ID, &m.Name, &m.Path, &ro, &m.EveryonePerm, &m.Color, &m.CreatedBy, &m.CreatedAt); err != nil {
		return nil, notFound(err)
	}
	m.ReadOnly = ro == 1
	return &m, nil
}

// ListMounts returns every mount with its access list (admin view).
func (s *Store) ListMounts(ctx context.Context) ([]Mount, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+mountCols+` FROM mounts ORDER BY lower(name)`)
	if err != nil {
		return nil, err
	}
	var out []Mount
	for rows.Next() {
		m, err := scanMount(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, *m)
	}
	rows.Close()
	for i := range out {
		out[i].Access, _ = s.mountAccess(ctx, out[i].ID)
	}
	return out, nil
}

func (s *Store) mountAccess(ctx context.Context, id int64) ([]Access, error) {
	return s.accessList(ctx, `SELECT a.user_id, u.name, u.email, a.perm FROM mount_access a JOIN users u ON u.id = a.user_id WHERE a.mount_id = ? ORDER BY lower(u.name), u.email`, id)
}

func (s *Store) accessList(ctx context.Context, q string, id int64) ([]Access, error) {
	rows, err := s.DB.QueryContext(ctx, q, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Access{}
	for rows.Next() {
		var a Access
		if err := rows.Scan(&a.UserID, &a.Name, &a.Email, &a.Perm); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetMount returns one mount with its access list.
func (s *Store) GetMount(ctx context.Context, id int64) (*Mount, error) {
	m, err := scanMount(s.DB.QueryRowContext(ctx, `SELECT `+mountCols+` FROM mounts WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	m.Access, _ = s.mountAccess(ctx, id)
	return m, nil
}

// SaveMount creates or updates a mount and replaces its access list.
func (s *Store) SaveMount(ctx context.Context, m *Mount) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if m.ID == 0 {
		res, err := tx.ExecContext(ctx, `INSERT INTO mounts (name, path, read_only, everyone_perm, color, created_by) VALUES (?, ?, ?, ?, ?, ?)`, m.Name, m.Path, boolInt(m.ReadOnly), m.EveryonePerm, m.Color, nullInt(&m.CreatedBy))
		if err != nil {
			return conflict(err)
		}
		m.ID, _ = res.LastInsertId()
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE mounts SET name = ?, path = ?, read_only = ?, everyone_perm = ?, color = ? WHERE id = ?`, m.Name, m.Path, boolInt(m.ReadOnly), m.EveryonePerm, m.Color, m.ID); err != nil {
			return conflict(err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM mount_access WHERE mount_id = ?`, m.ID); err != nil {
			return err
		}
	}
	for _, a := range m.Access {
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO mount_access (mount_id, user_id, perm) VALUES (?, ?, ?)`, m.ID, a.UserID, a.Perm); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteMount removes a mount (the folder on the server stays).
func (s *Store) DeleteMount(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM mounts WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM stars WHERE space = ?`, "mount:"+itoa(id))
	return nil
}

// MountsFor lists the mounts a person may open, with their permission.
func (s *Store) MountsFor(ctx context.Context, uid int64) ([]Mount, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+mountCols+`, COALESCE((SELECT perm FROM mount_access a WHERE a.mount_id = mounts.id AND a.user_id = ?), '')
		FROM mounts ORDER BY lower(name)`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Mount{}
	for rows.Next() {
		var m Mount
		var ro int
		var own string
		if err := rows.Scan(&m.ID, &m.Name, &m.Path, &ro, &m.EveryonePerm, &m.Color, &m.CreatedBy, &m.CreatedAt, &own); err != nil {
			return nil, err
		}
		m.ReadOnly = ro == 1
		m.Perm = bestPerm(own, m.EveryonePerm)
		if m.Perm == "" {
			continue
		}
		if m.ReadOnly {
			m.Perm = "view"
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MountFor returns one mount if the person may open it.
func (s *Store) MountFor(ctx context.Context, uid, id int64) (*Mount, error) {
	ms, err := s.MountsFor(ctx, uid)
	if err != nil {
		return nil, err
	}
	for i := range ms {
		if ms[i].ID == id {
			return &ms[i], nil
		}
	}
	return nil, ErrNotFound
}

func bestPerm(a, b string) string {
	if a == "edit" || b == "edit" {
		return "edit"
	}
	if a == "view" || b == "view" {
		return "view"
	}
	return ""
}

// ---------- shares ----------

const shareCols = `s.id, s.owner_id, u.name, u.email, s.path, s.is_dir, s.everyone_perm, s.note, s.created_at`

func scanShare(row interface{ Scan(...any) error }, extra ...any) (*Share, error) {
	var sh Share
	var dir int
	dest := append([]any{&sh.ID, &sh.OwnerID, &sh.OwnerName, &sh.OwnerEmail, &sh.Path, &dir, &sh.EveryonePerm, &sh.Note, &sh.CreatedAt}, extra...)
	if err := row.Scan(dest...); err != nil {
		return nil, notFound(err)
	}
	sh.IsDir = dir == 1
	return &sh, nil
}

// SharesBy lists what a person shared, with the members of each.
func (s *Store) SharesBy(ctx context.Context, owner int64) ([]Share, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+shareCols+` FROM shares s JOIN users u ON u.id = s.owner_id WHERE s.owner_id = ? ORDER BY lower(s.path)`, owner)
	if err != nil {
		return nil, err
	}
	var out []Share
	for rows.Next() {
		sh, err := scanShare(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, *sh)
	}
	rows.Close()
	for i := range out {
		out[i].Members, _ = s.shareMembers(ctx, out[i].ID)
		out[i].Perm = "edit"
	}
	if out == nil {
		out = []Share{}
	}
	return out, nil
}

func (s *Store) shareMembers(ctx context.Context, id int64) ([]Access, error) {
	return s.accessList(ctx, `SELECT m.user_id, u.name, u.email, m.perm FROM share_members m JOIN users u ON u.id = m.user_id WHERE m.share_id = ? ORDER BY lower(u.name), u.email`, id)
}

// SharesWith lists what others shared with a person, with their permission.
func (s *Store) SharesWith(ctx context.Context, uid int64) ([]Share, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+shareCols+`, COALESCE((SELECT perm FROM share_members m WHERE m.share_id = s.id AND m.user_id = ?), '')
		FROM shares s JOIN users u ON u.id = s.owner_id WHERE s.owner_id <> ? ORDER BY s.id`, uid, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Share{}
	for rows.Next() {
		var own string
		sh, err := scanShare(rows, &own)
		if err != nil {
			return nil, err
		}
		sh.Perm = bestPerm(own, sh.EveryonePerm)
		if sh.Perm != "" {
			out = append(out, *sh)
		}
	}
	return out, rows.Err()
}

// ShareFor returns one share the person may open (owner or member).
func (s *Store) ShareFor(ctx context.Context, uid, id int64) (*Share, error) {
	var own string
	sh, err := scanShare(s.DB.QueryRowContext(ctx, `SELECT `+shareCols+`, COALESCE((SELECT perm FROM share_members m WHERE m.share_id = s.id AND m.user_id = ?), '')
		FROM shares s JOIN users u ON u.id = s.owner_id WHERE s.id = ?`, uid, id), &own)
	if err != nil {
		return nil, err
	}
	if sh.OwnerID == uid {
		sh.Perm = "edit"
	} else {
		sh.Perm = bestPerm(own, sh.EveryonePerm)
	}
	if sh.Perm == "" {
		return nil, ErrNotFound
	}
	return sh, nil
}

// ShareByPath returns the owner's share of one path, if any.
func (s *Store) ShareByPath(ctx context.Context, owner int64, path string) (*Share, error) {
	sh, err := scanShare(s.DB.QueryRowContext(ctx, `SELECT `+shareCols+` FROM shares s JOIN users u ON u.id = s.owner_id WHERE s.owner_id = ? AND s.path = ?`, owner, path))
	if err != nil {
		return nil, err
	}
	sh.Members, _ = s.shareMembers(ctx, sh.ID)
	sh.Perm = "edit"
	return sh, nil
}

// SaveShare creates or updates the owner's share of a path; no members and
// no everyone permission removes it. Returns nil when removed.
func (s *Store) SaveShare(ctx context.Context, sh *Share) (*Share, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	var id int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM shares WHERE owner_id = ? AND path = ?`, sh.OwnerID, sh.Path).Scan(&id)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if len(sh.Members) == 0 && sh.EveryonePerm == "" {
		if id != 0 {
			if _, err := tx.ExecContext(ctx, `DELETE FROM shares WHERE id = ?`, id); err != nil {
				return nil, err
			}
		}
		return nil, tx.Commit()
	}
	if id == 0 {
		res, err := tx.ExecContext(ctx, `INSERT INTO shares (owner_id, path, is_dir, everyone_perm, note) VALUES (?, ?, ?, ?, ?)`, sh.OwnerID, sh.Path, boolInt(sh.IsDir), sh.EveryonePerm, sh.Note)
		if err != nil {
			return nil, err
		}
		id, _ = res.LastInsertId()
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE shares SET is_dir = ?, everyone_perm = ?, note = ? WHERE id = ?`, boolInt(sh.IsDir), sh.EveryonePerm, sh.Note, id); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM share_members WHERE share_id = ?`, id); err != nil {
			return nil, err
		}
	}
	for _, m := range sh.Members {
		if m.UserID == sh.OwnerID {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO share_members (share_id, user_id, perm) VALUES (?, ?, ?)`, id, m.UserID, m.Perm); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.ShareByPath(ctx, sh.OwnerID, sh.Path)
}

// DeleteShare removes one of the owner's shares.
func (s *Store) DeleteShare(ctx context.Context, owner, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM shares WHERE id = ? AND owner_id = ?`, id, owner)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM stars WHERE space = ?`, "share:"+itoa(id))
	return nil
}

// MoveSharePaths follows a rename in the owner's home: shares of the path
// and of anything below it move along.
func (s *Store) MoveSharePaths(ctx context.Context, owner int64, from, to string) {
	_, _ = s.DB.ExecContext(ctx, `UPDATE shares SET path = ? || substr(path, ?) WHERE owner_id = ? AND (path = ? OR path LIKE ? ESCAPE '\')`,
		to, len(from)+1, owner, from, likeEscape(strings.TrimSuffix(from, "/"))+"/%")
}

// DropSharePaths forgets shares of a deleted path and anything below it.
func (s *Store) DropSharePaths(ctx context.Context, owner int64, p string) {
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM shares WHERE owner_id = ? AND (path = ? OR path LIKE ? ESCAPE '\')`, owner, p, likeEscape(strings.TrimSuffix(p, "/"))+"/%")
}

// SharedPaths lists the owner's shared paths with how many people see each.
func (s *Store) SharedPaths(ctx context.Context, owner int64) map[string]int {
	out := map[string]int{}
	rows, err := s.DB.QueryContext(ctx, `SELECT s.path, (SELECT COUNT(1) FROM share_members m WHERE m.share_id = s.id) + CASE WHEN s.everyone_perm <> '' THEN 1000 ELSE 0 END FROM shares s WHERE s.owner_id = ?`, owner)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		var n int
		if rows.Scan(&p, &n) == nil {
			out[p] = n
		}
	}
	return out
}

// ---------- stars ----------

// Stars lists a person's pinned files and folders.
func (s *Store) Stars(ctx context.Context, uid int64) ([]Star, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT space, path, is_dir, created_at FROM stars WHERE user_id = ? ORDER BY created_at DESC`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Star{}
	for rows.Next() {
		var st Star
		var d int
		if err := rows.Scan(&st.Space, &st.Path, &d, &st.CreatedAt); err != nil {
			return nil, err
		}
		st.IsDir = d == 1
		out = append(out, st)
	}
	return out, rows.Err()
}

// SetStar pins or unpins a path.
func (s *Store) SetStar(ctx context.Context, uid int64, space, path string, isDir, on bool) error {
	if !on {
		_, err := s.DB.ExecContext(ctx, `DELETE FROM stars WHERE user_id = ? AND space = ? AND path = ?`, uid, space, path)
		return err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO stars (user_id, space, path, is_dir) VALUES (?, ?, ?, ?)`, uid, space, path, boolInt(isDir))
	return err
}

// MoveStars follows a rename inside one space for every person.
func (s *Store) MoveStars(ctx context.Context, space, from, to string) {
	_, _ = s.DB.ExecContext(ctx, `UPDATE OR IGNORE stars SET path = ? || substr(path, ?) WHERE space = ? AND (path = ? OR path LIKE ? ESCAPE '\')`,
		to, len(from)+1, space, from, likeEscape(strings.TrimSuffix(from, "/"))+"/%")
}

// DropStars forgets stars of a deleted path in one space.
func (s *Store) DropStars(ctx context.Context, space, p string) {
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM stars WHERE space = ? AND (path = ? OR path LIKE ? ESCAPE '\')`, space, p, likeEscape(strings.TrimSuffix(p, "/"))+"/%")
}

// StarredSet returns the starred paths of one space for a person.
func (s *Store) StarredSet(ctx context.Context, uid int64, space string) map[string]bool {
	out := map[string]bool{}
	rows, err := s.DB.QueryContext(ctx, `SELECT path FROM stars WHERE user_id = ? AND space = ?`, uid, space)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		if rows.Scan(&p) == nil {
			out[p] = true
		}
	}
	return out
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
