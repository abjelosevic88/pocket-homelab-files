// Package search keeps a full-text index of every place Pocket Files serves: file and folder
// names (FTS5 trigram, so any part of a name matches) and the text inside documents (FTS5
// unicode61 without diacritics, ranked with bm25, with highlighted snippets). It lives in its
// own SQLite file next to the app's database and can be deleted at any time; it rebuilds.
package search

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

// Root is one indexed place: "home:3" → /store/files/3, "mount:2" → /mnt/media, "cloud:5" → …
type Root struct {
	Key string
	Dir string
	// Slow roots (network mounts) are walked less often.
	Slow bool
	// NamesOnly skips reading documents (big media and book libraries).
	NamesOnly bool
}

// Index is the search database plus the workers that keep it current.
type Index struct {
	db    *sql.DB
	log   *slog.Logger
	roots func(context.Context) []Root

	// Rescan intervals for local and slow roots.
	Every, SlowEvery time.Duration

	mu      sync.Mutex
	writeMu sync.Mutex                 // one write transaction at a time
	dirty   map[string]map[string]bool // key → folders to look at again
	wake    chan struct{}
	scanned map[string]time.Time

	gen     atomic.Int64
	Scanned atomic.Int64 // files seen by the last full pass (for status)
}

const schema = `
PRAGMA journal_mode=WAL;
PRAGMA synchronous=NORMAL;
CREATE TABLE IF NOT EXISTS docs (
  id         INTEGER PRIMARY KEY,
  space      TEXT NOT NULL,
  path       TEXT NOT NULL,
  name       TEXT NOT NULL,
  is_dir     INTEGER NOT NULL,
  size       INTEGER NOT NULL,
  mtime      INTEGER NOT NULL,
  ext        TEXT NOT NULL DEFAULT '',
  text_state INTEGER NOT NULL DEFAULT 0, -- 0 to read, 1 read, 2 nothing to read, 3 failed
  seen       INTEGER NOT NULL DEFAULT 0,
  UNIQUE(space, path)
);
CREATE INDEX IF NOT EXISTS docs_todo ON docs(text_state) WHERE text_state = 0;
CREATE VIRTUAL TABLE IF NOT EXISTS names USING fts5(name, folder, tokenize = 'trigram remove_diacritics 1', content = '', contentless_delete = 1);
CREATE VIRTUAL TABLE IF NOT EXISTS body USING fts5(text, tokenize = 'unicode61 remove_diacritics 2', prefix = '2 3');
CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
`

// Open opens (or makes) the index at path.
func Open(dbPath string, log *slog.Logger, roots func(context.Context) []Root) (*Index, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, err
	}
	// Writers take the lock up front (BEGIN IMMEDIATE) so the busy timeout applies instead of
	// a lock upgrade failing at once; writeMu also keeps our own writers from racing.
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(15000)&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("search index: %w", err)
	}
	ix := &Index{db: db, log: log, roots: roots, Every: 10 * time.Minute, SlowEvery: time.Hour,
		dirty: map[string]map[string]bool{}, wake: make(chan struct{}, 1), scanned: map[string]time.Time{}}
	var g int64
	_ = db.QueryRow(`SELECT COALESCE(MAX(seen), 0) FROM docs`).Scan(&g)
	ix.gen.Store(g)
	return ix, nil
}

func (ix *Index) Close() error { return ix.db.Close() }

// Touch asks for one folder (and everything under it) to be looked at again soon: called
// after every change made through the app, WebDAV or a drive copy.
func (ix *Index) Touch(key, dir string) {
	if ix == nil || key == "" {
		return
	}
	ix.mu.Lock()
	if ix.dirty[key] == nil {
		ix.dirty[key] = map[string]bool{}
	}
	ix.dirty[key][cleanRel(dir)] = true
	ix.mu.Unlock()
	select {
	case ix.wake <- struct{}{}:
	default:
	}
}

// Run keeps the index current until ctx ends: touched folders within a second or two,
// full passes on the intervals, and document text in the background.
func (ix *Index) Run(ctx context.Context) {
	go ix.extractLoop(ctx)
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	ix.fullPass(ctx, false)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ix.wake:
			time.Sleep(1500 * time.Millisecond) // let a burst of changes settle
			ix.flushDirty(ctx)
		case <-tick.C:
			ix.fullPass(ctx, false)
		}
	}
}

// Rebuild forgets everything and walks every root again.
func (ix *Index) Rebuild(ctx context.Context) error {
	ix.writeMu.Lock()
	defer ix.writeMu.Unlock()
	for _, q := range []string{`DELETE FROM docs`, `DELETE FROM names`, `DELETE FROM body`} {
		if _, err := ix.db.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	ix.mu.Lock()
	ix.scanned = map[string]time.Time{}
	ix.mu.Unlock()
	go ix.fullPass(context.WithoutCancel(ctx), true)
	return nil
}

func (ix *Index) flushDirty(ctx context.Context) {
	ix.mu.Lock()
	work := ix.dirty
	ix.dirty = map[string]map[string]bool{}
	ix.mu.Unlock()
	roots := map[string]Root{}
	for _, r := range ix.roots(ctx) {
		roots[r.Key] = r
	}
	for key, dirs := range work {
		r, ok := roots[key]
		if !ok {
			ix.dropSpace(ctx, key)
			continue
		}
		for d := range topmost(dirs) {
			if err := ix.scan(ctx, r, d); err != nil {
				ix.log.Warn("search: rescan failed", "space", key, "dir", d, "err", err)
			}
		}
	}
}

// fullPass walks the roots that are due, and forgets spaces that are gone.
func (ix *Index) fullPass(ctx context.Context, all bool) {
	roots := ix.roots(ctx)
	keys := map[string]bool{}
	var seen int64
	for _, r := range roots {
		keys[r.Key] = true
		every := ix.Every
		if r.Slow {
			every = ix.SlowEvery
		}
		ix.mu.Lock()
		last := ix.scanned[r.Key]
		ix.mu.Unlock()
		if !all && time.Since(last) < every {
			continue
		}
		start := time.Now()
		if err := ix.scan(ctx, r, "/"); err != nil {
			ix.log.Warn("search: scan failed", "space", r.Key, "err", err)
			continue
		}
		ix.mu.Lock()
		ix.scanned[r.Key] = time.Now()
		ix.mu.Unlock()
		if d := time.Since(start); d > 5*time.Second {
			ix.log.Info("search: scanned", "space", r.Key, "took", d.Round(time.Millisecond))
		}
	}
	rows, err := ix.db.QueryContext(ctx, `SELECT DISTINCT space FROM docs`)
	if err == nil {
		var gone []string
		for rows.Next() {
			var k string
			if rows.Scan(&k) == nil && !keys[k] {
				gone = append(gone, k)
			}
		}
		rows.Close()
		for _, k := range gone {
			ix.dropSpace(ctx, k)
		}
	}
	_ = ix.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM docs`).Scan(&seen)
	ix.Scanned.Store(seen)
}

type row struct {
	id    int64
	size  int64
	mtime int64
	isDir bool
}

// scan walks dir (relative to the root) and brings its part of the index up to date:
// new and changed entries are written, unchanged ones only marked seen, missing ones removed.
func (ix *Index) scan(ctx context.Context, r Root, dir string) error {
	dir = cleanRel(dir)
	base := filepath.Join(r.Dir, filepath.FromSlash(dir))
	if _, err := os.Stat(base); err != nil {
		if os.IsNotExist(err) {
			return ix.removeUnder(ctx, r.Key, dir, true)
		}
		return err
	}
	gen := ix.gen.Add(1)

	// What the index holds for this part now.
	known := map[string]row{}
	q, args := underClause(dir)
	rows, err := ix.db.QueryContext(ctx, `SELECT id, path, size, mtime, is_dir FROM docs WHERE space = ? AND `+q, append([]any{r.Key}, args...)...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var p string
		var k row
		if err := rows.Scan(&k.id, &p, &k.size, &k.mtime, &k.isDir); err != nil {
			rows.Close()
			return err
		}
		known[p] = k
	}
	rows.Close()

	ix.writeMu.Lock()
	locked := true
	defer func() {
		if locked {
			ix.writeMu.Unlock()
		}
	}()
	tx, err := ix.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // the tx in use at return (commit() swaps it)
	n := 0
	// Commit every couple of thousand entries and let the document reader in between.
	commit := func() error {
		if err := tx.Commit(); err != nil {
			return err
		}
		ix.writeMu.Unlock()
		locked = false
		ix.writeMu.Lock()
		locked = true
		tx, err = ix.db.BeginTx(ctx, nil)
		return err
	}
	walkErr := filepath.WalkDir(base, func(full string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(r.Dir, full)
		rel = cleanRel(filepath.ToSlash(rel))
		if rel == "/" {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		size, mtime := info.Size(), info.ModTime().UnixNano()
		if d.IsDir() {
			size = 0
		}
		k, ok := known[rel]
		delete(known, rel)
		if ok && k.size == size && k.mtime == mtime && k.isDir == d.IsDir() {
			_, err = tx.ExecContext(ctx, `UPDATE docs SET seen = ? WHERE id = ?`, gen, k.id)
		} else {
			err = ix.upsert(ctx, tx, r.Key, rel, d.IsDir(), size, mtime, gen, k.id, r.NamesOnly)
		}
		if err != nil {
			return err
		}
		if n++; n%2000 == 0 {
			return commit()
		}
		return nil
	})
	if walkErr != nil {
		return walkErr
	}
	// Whatever was known but not met on the walk is gone.
	for _, k := range known {
		if err := deleteDoc(ctx, tx, k.id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (ix *Index) upsert(ctx context.Context, tx *sql.Tx, key, rel string, isDir bool, size, mtime, gen, id int64, namesOnly bool) error {
	name := path.Base(rel)
	ext := ""
	if !isDir {
		ext = strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
	}
	state := 2
	if !isDir && !namesOnly && Readable(ext, size) {
		state = 0
	}
	if id != 0 {
		// Changed: drop the old name and text rows; they are written again.
		if _, err := tx.ExecContext(ctx, `DELETE FROM names WHERE rowid = ?`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM body WHERE rowid = ?`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE docs SET name = ?, is_dir = ?, size = ?, mtime = ?, ext = ?, text_state = ?, seen = ? WHERE id = ?`,
			name, isDir, size, mtime, ext, state, gen, id); err != nil {
			return err
		}
	} else {
		res, err := tx.ExecContext(ctx, `INSERT INTO docs (space, path, name, is_dir, size, mtime, ext, text_state, seen) VALUES (?,?,?,?,?,?,?,?,?)
			ON CONFLICT(space, path) DO UPDATE SET name = excluded.name, is_dir = excluded.is_dir, size = excluded.size, mtime = excluded.mtime,
			ext = excluded.ext, text_state = excluded.text_state, seen = excluded.seen`,
			key, rel, name, isDir, size, mtime, ext, state, gen)
		if err != nil {
			return err
		}
		id, _ = res.LastInsertId()
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO names (rowid, name, folder) VALUES (?, ?, ?)`, id, fold(name), fold(path.Dir(rel)))
	return err
}

func deleteDoc(ctx context.Context, tx *sql.Tx, id int64) error {
	for _, q := range []string{`DELETE FROM names WHERE rowid = ?`, `DELETE FROM body WHERE rowid = ?`, `DELETE FROM docs WHERE id = ?`} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return err
		}
	}
	return nil
}

func (ix *Index) removeUnder(ctx context.Context, key, dir string, self bool) error {
	q, args := underClause(dir)
	if self && dir != "/" {
		q = "(" + q + " OR path = ?)"
		args = append(args, dir)
	}
	rows, err := ix.db.QueryContext(ctx, `SELECT id FROM docs WHERE space = ? AND `+q, append([]any{key}, args...)...)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	ix.writeMu.Lock()
	defer ix.writeMu.Unlock()
	tx, err := ix.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if err := deleteDoc(ctx, tx, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (ix *Index) dropSpace(ctx context.Context, key string) {
	if err := ix.removeUnder(ctx, key, "/", true); err != nil {
		ix.log.Warn("search: dropping a space failed", "space", key, "err", err)
	}
}

// underClause matches everything below dir (not dir itself).
func underClause(dir string) (string, []any) {
	if dir == "/" {
		return "1 = 1", nil
	}
	// Byte order: everything starting with "dir/" sorts between "dir/" and "dir0" ('0' follows '/').
	return "path > ? AND path < ?", []any{dir + "/", dir + "0"}
}

// topmost keeps only folders not inside another one of the set.
func topmost(dirs map[string]bool) map[string]bool {
	out := map[string]bool{}
	for d := range dirs {
		inside := false
		for o := range dirs {
			if o != d && (o == "/" || strings.HasPrefix(d, o+"/")) {
				inside = true
				break
			}
		}
		if !inside {
			out[d] = true
		}
	}
	return out
}

func cleanRel(p string) string { return path.Clean("/" + strings.ReplaceAll(p, "\\", "/")) }

// fold makes text match the way people type: case is handled by FTS5, accents by
// remove_diacritics; letters that do not decompose are spelled out here.
var folder = strings.NewReplacer("đ", "dj", "Đ", "Dj", "ß", "ss", "ø", "o", "Ø", "O", "ł", "l", "Ł", "L", "æ", "ae", "Æ", "Ae", "œ", "oe")

func fold(s string) string { return folder.Replace(s) }

// Status is what Settings → About shows.
type Status struct {
	Files       int64 `json:"files"`
	Folders     int64 `json:"folders"`
	TextRead    int64 `json:"text_read"`
	TextPending int64 `json:"text_pending"`
	Bytes       int64 `json:"index_bytes"`
}

func (ix *Index) Status(ctx context.Context, dbPath string) Status {
	var st Status
	_ = ix.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(is_dir = 0), 0), COALESCE(SUM(is_dir = 1), 0), COALESCE(SUM(text_state = 1), 0), COALESCE(SUM(text_state = 0), 0) FROM docs`).
		Scan(&st.Files, &st.Folders, &st.TextRead, &st.TextPending)
	for _, suffix := range []string{"", "-wal"} {
		if fi, err := os.Stat(dbPath + suffix); err == nil {
			st.Bytes += fi.Size()
		}
	}
	return st
}
