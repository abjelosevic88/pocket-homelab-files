package search

import (
	"context"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Scope is one place the person may search, and how its paths look to them.
type Scope struct {
	Key    string // index key: "home:3", "mount:2", "cloud:5"
	Prefix string // only below this path ("/" = all of it)
	Space  string // the space id the person knows it by ("home", "share:7", …)
	Base   string // trimmed from paths for that space (a share's folder in its owner's home)
}

// Hit is one result.
type Hit struct {
	Space    string    `json:"space"`
	Path     string    `json:"path"`
	Name     string    `json:"name"`
	Folder   string    `json:"folder"`
	IsDir    bool      `json:"is_dir"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	Ext      string    `json:"ext,omitempty"`
	Match    string    `json:"match"`             // name | content
	Snippet  string    `json:"snippet,omitempty"` // matched words between  and 
}

// Query is a parsed search box: words, "exact phrases", -excluded words, ext:pdf, type:folder.
type Query struct {
	Terms, Phrases, Excludes, Exts []string
	Type                           string // "", file, folder
}

// Parse reads what the person typed.
func Parse(q string) Query {
	var out Query
	q = fold(q)
	for len(q) > 0 {
		q = strings.TrimLeftFunc(q, unicode.IsSpace)
		if q == "" {
			break
		}
		neg := false
		if q[0] == '-' && len(q) > 1 {
			neg, q = true, q[1:]
		}
		var word string
		if q[0] == '"' {
			end := strings.IndexByte(q[1:], '"')
			if end < 0 {
				word, q = q[1:], ""
			} else {
				word, q = q[1:end+1], q[end+2:]
			}
			word = strings.TrimSpace(word)
			if word == "" {
				continue
			}
			if neg {
				out.Excludes = append(out.Excludes, word)
			} else if strings.ContainsAny(word, " \t") {
				out.Phrases = append(out.Phrases, word)
			} else {
				out.Terms = append(out.Terms, word)
			}
			continue
		}
		end := strings.IndexFunc(q, unicode.IsSpace)
		if end < 0 {
			end = len(q)
		}
		word, q = q[:end], q[end:]
		lw := strings.ToLower(word)
		switch {
		case !neg && strings.HasPrefix(lw, "ext:") && len(lw) > 4:
			for _, e := range strings.Split(lw[4:], ",") {
				if e = strings.TrimPrefix(strings.TrimSpace(e), "."); e != "" {
					out.Exts = append(out.Exts, e)
				}
			}
		case !neg && (lw == "type:folder" || lw == "type:dir"):
			out.Type = "folder"
		case !neg && lw == "type:file":
			out.Type = "file"
		case neg:
			out.Excludes = append(out.Excludes, word)
		default:
			out.Terms = append(out.Terms, word)
		}
	}
	return out
}

func (q Query) Empty() bool {
	return len(q.Terms) == 0 && len(q.Phrases) == 0 && len(q.Exts) == 0 && q.Type == ""
}

func quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func hasWordChar(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }) >= 0
}

// Search answers names first (any part of a name or folder), then documents whose text
// matches, each ranked by relevance. Only the given scopes are searched.
func (ix *Index) Search(ctx context.Context, q Query, scopes []Scope, limit int) ([]Hit, error) {
	if q.Empty() || len(scopes) == 0 {
		return []Hit{}, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	scopeSQL, scopeArgs := scopeClause(scopes)
	filter, filterArgs := "", []any{}
	switch q.Type {
	case "folder":
		filter += " AND d.is_dir = 1"
	case "file":
		filter += " AND d.is_dir = 0"
	}
	if len(q.Exts) > 0 {
		filter += " AND d.ext IN (" + strings.TrimSuffix(strings.Repeat("?,", len(q.Exts)), ",") + ")"
		for _, e := range q.Exts {
			filterArgs = append(filterArgs, e)
		}
	}

	seen := map[string]bool{}
	var hits []Hit

	// 1. Names. The trigram index needs three characters; shorter words use LIKE.
	var match []string
	var like []string
	for _, t := range append(append([]string{}, q.Terms...), q.Phrases...) {
		if utf8.RuneCountInString(t) >= 3 {
			match = append(match, quote(t))
		} else {
			like = append(like, t)
		}
	}
	var notMatch []string
	for _, t := range q.Excludes {
		if utf8.RuneCountInString(t) >= 3 {
			notMatch = append(notMatch, quote(t))
		} else {
			like = append(like, "-"+t)
		}
	}
	likeSQL, likeArgs := "", []any{}
	for _, t := range like {
		op := "LIKE"
		if strings.HasPrefix(t, "-") {
			op, t = "NOT LIKE", t[1:]
		}
		likeSQL += " AND (d.name || ' ' || d.path) " + op + " ? ESCAPE '\\'"
		likeArgs = append(likeArgs, "%"+escapeLike(t)+"%")
	}
	var sqlText string
	var args []any
	if len(match) > 0 {
		m := strings.Join(match, " AND ")
		if len(notMatch) > 0 {
			m += " NOT " + strings.Join(notMatch, " NOT ")
		}
		sqlText = `SELECT d.space, d.path, d.name, d.is_dir, d.size, d.mtime, d.ext FROM names JOIN docs d ON d.id = names.rowid
			WHERE names MATCH ? AND (` + scopeSQL + `)` + filter + likeSQL + ` ORDER BY bm25(names, 10.0, 1.0), length(d.path) LIMIT ?`
		args = append(append(append(append([]any{m}, scopeArgs...), filterArgs...), likeArgs...), limit)
	} else {
		sqlText = `SELECT d.space, d.path, d.name, d.is_dir, d.size, d.mtime, d.ext FROM docs d
			WHERE (` + scopeSQL + `)` + filter + likeSQL + ` ORDER BY d.is_dir DESC, d.mtime DESC LIMIT ?`
		args = append(append(append(scopeArgs, filterArgs...), likeArgs...), limit)
	}
	if err := ix.collect(ctx, sqlText, args, scopes, "name", seen, &hits); err != nil {
		return nil, err
	}

	// 2. Text inside documents.
	var body []string
	for _, t := range q.Terms {
		if hasWordChar(t) {
			body = append(body, quote(t)+"*")
		}
	}
	for _, p := range q.Phrases {
		if hasWordChar(p) {
			body = append(body, quote(p))
		}
	}
	if len(body) > 0 && q.Type != "folder" && len(hits) < limit {
		m := strings.Join(body, " AND ")
		for _, t := range q.Excludes {
			if hasWordChar(t) {
				m += " NOT " + quote(t)
			}
		}
		sqlText = `SELECT d.space, d.path, d.name, d.is_dir, d.size, d.mtime, d.ext, snippet(body, 0, char(57344), char(57345), '…', 16)
			FROM body JOIN docs d ON d.id = body.rowid WHERE body MATCH ? AND (` + scopeSQL + `)` + filter + ` ORDER BY rank LIMIT ?`
		args = append(append(append([]any{m}, scopeArgs...), filterArgs...), limit-len(hits))
		if err := ix.collect(ctx, sqlText, args, scopes, "content", seen, &hits); err != nil {
			return nil, err
		}
	}
	if hits == nil {
		hits = []Hit{}
	}
	return hits, nil
}

func (ix *Index) collect(ctx context.Context, q string, args []any, scopes []Scope, kind string, seen map[string]bool, hits *[]Hit) error {
	rows, err := ix.db.QueryContext(ctx, q, args...)
	if err != nil {
		if strings.Contains(err.Error(), "fts5") {
			return nil // a query FTS5 cannot parse matches nothing
		}
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key, p, name, ext string
		var isDir bool
		var size, mtime int64
		dest := []any{&key, &p, &name, &isDir, &size, &mtime, &ext}
		var snip string
		if kind == "content" {
			dest = append(dest, &snip)
		}
		if err := rows.Scan(dest...); err != nil {
			return err
		}
		sc, ok := scopeOf(scopes, key, p)
		if !ok {
			continue
		}
		rel := p
		if sc.Base != "" && sc.Base != "/" {
			rel = "/" + strings.TrimPrefix(strings.TrimPrefix(p, sc.Base), "/")
		}
		id := sc.Space + "\x00" + rel
		if seen[id] {
			continue
		}
		seen[id] = true
		*hits = append(*hits, Hit{Space: sc.Space, Path: rel, Name: name, Folder: path.Dir(rel), IsDir: isDir, Size: size,
			Modified: time.Unix(0, mtime), Ext: ext, Match: kind, Snippet: snip})
	}
	return rows.Err()
}

func scopeClause(scopes []Scope) (string, []any) {
	var parts []string
	var args []any
	for _, s := range scopes {
		if s.Prefix == "" || s.Prefix == "/" {
			parts = append(parts, "d.space = ?")
			args = append(args, s.Key)
			continue
		}
		parts = append(parts, "(d.space = ? AND (d.path = ? OR (d.path > ? AND d.path < ?)))")
		args = append(args, s.Key, s.Prefix, s.Prefix+"/", s.Prefix+"0")
	}
	return strings.Join(parts, " OR "), args
}

// scopeOf picks the most specific scope a result belongs to.
func scopeOf(scopes []Scope, key, p string) (Scope, bool) {
	best, found := Scope{}, false
	for _, s := range scopes {
		if s.Key != key {
			continue
		}
		if s.Prefix == "" || s.Prefix == "/" || p == s.Prefix || strings.HasPrefix(p, s.Prefix+"/") {
			if !found || len(s.Prefix) > len(best.Prefix) {
				best, found = s, true
			}
		}
	}
	return best, found
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
