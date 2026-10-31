package search

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxText is how much text one document keeps in the index (enough for any search a person
// remembers; snippets come from it too).
const MaxText = 256 << 10

var plainExt = map[string]bool{
	"txt": true, "md": true, "markdown": true, "csv": true, "tsv": true, "json": true, "yaml": true, "yml": true, "xml": true,
	"html": true, "htm": true, "log": true, "ini": true, "toml": true, "conf": true, "cfg": true,
	"tex": true, "rst": true, "org": true, "rtf": true, "sql": true, "go": true, "py": true, "js": true, "ts": true, "tsx": true,
	"jsx": true, "java": true, "kt": true, "swift": true, "c": true, "h": true, "cpp": true, "hpp": true, "cs": true, "rs": true,
	"rb": true, "php": true, "sh": true, "zsh": true, "bash": true, "css": true, "scss": true, "vue": true, "svelte": true,
	"lua": true, "pl": true, "r": true, "dart": true, "nix": true, "dockerfile": true, "gradle": true, "properties": true, "eml": true,
}

var officeExt = map[string]bool{"docx": true, "xlsx": true, "pptx": true, "odt": true, "ods": true, "odp": true, "epub": true, "pages": false}

// Readable reports whether a file's text is worth reading (by type, and not absurdly large).
func Readable(ext string, size int64) bool {
	switch {
	case plainExt[ext]:
		return size <= 8<<20
	case officeExt[ext]:
		return size <= 100<<20
	case ext == "pdf":
		return size <= 200<<20
	}
	return false
}

// extractLoop reads the text of new and changed documents, a few at a time.
func (ix *Index) extractLoop(ctx context.Context) {
	for {
		n, err := ix.extractBatch(ctx, 20)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			ix.log.Warn("search: reading documents failed", "err", err)
		}
		if n == 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}
}

func (ix *Index) extractBatch(ctx context.Context, limit int) (int, error) {
	rows, err := ix.db.QueryContext(ctx, `SELECT id, space, path, ext FROM docs WHERE text_state = 0 LIMIT ?`, limit)
	if err != nil {
		return 0, err
	}
	type job struct {
		id              int64
		space, rel, ext string
	}
	var jobs []job
	for rows.Next() {
		var j job
		if rows.Scan(&j.id, &j.space, &j.rel, &j.ext) == nil {
			jobs = append(jobs, j)
		}
	}
	rows.Close()
	if len(jobs) == 0 {
		return 0, nil
	}
	dirs := map[string]string{}
	for _, r := range ix.roots(ctx) {
		dirs[r.Key] = r.Dir
	}
	for _, j := range jobs {
		state, text := 3, ""
		if dir, ok := dirs[j.space]; ok {
			full := filepath.Join(dir, filepath.FromSlash(j.rel))
			t, err := Extract(ctx, full, j.ext)
			switch {
			case err == nil && strings.TrimSpace(t) != "":
				state, text = 1, t
			case err == nil:
				state = 2
			default:
				ix.log.Debug("search: no text", "path", j.rel, "err", err)
			}
		}
		if err := ix.saveText(ctx, j.id, state, text); err != nil {
			return 0, err
		}
	}
	return len(jobs), nil
}

// saveText stores one document's text (or that it has none).
func (ix *Index) saveText(ctx context.Context, id int64, state int, text string) error {
	ix.writeMu.Lock()
	defer ix.writeMu.Unlock()
	tx, err := ix.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM body WHERE rowid = ?`, id); err != nil {
		return err
	}
	if state == 1 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO body (rowid, text) VALUES (?, ?)`, id, fold(text)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE docs SET text_state = ? WHERE id = ?`, state, id); err != nil {
		return err
	}
	return tx.Commit()
}

// Extract reads the searchable text of one file.
func Extract(ctx context.Context, full, ext string) (string, error) {
	switch {
	case plainExt[ext]:
		return plainText(full, ext)
	case ext == "pdf":
		return pdfText(ctx, full)
	case officeExt[ext]:
		return zipText(full, ext)
	}
	return "", nil
}

func plainText(full, ext string) (string, error) {
	f, err := os.Open(full)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 2*MaxText))
	if err != nil {
		return "", err
	}
	if bytes.IndexByte(b[:min(len(b), 8192)], 0) >= 0 {
		return "", nil // binary after all
	}
	s := strings.ToValidUTF8(string(b), " ")
	if ext == "html" || ext == "htm" || ext == "xml" {
		s = stripTags(s)
	}
	return clip(s), nil
}

func pdfText(ctx context.Context, full string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, "pdftotext", "-q", "-l", "300", "-enc", "UTF-8", full, "-")
	cmd.Stdout = &limitWriter{w: &out, n: MaxText * 2}
	if err := cmd.Run(); err != nil {
		var ee *exec.Error
		if errors.As(err, &ee) {
			return "", nil // pdftotext missing: names only
		}
		if out.Len() == 0 {
			return "", err
		}
	}
	return clip(out.String()), nil
}

type limitWriter struct {
	w io.Writer
	n int
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if l.n <= 0 {
		return len(p), nil
	}
	k := min(len(p), l.n)
	l.n -= k
	_, err := l.w.Write(p[:k])
	return len(p), err
}

// zipText reads Office Open XML, OpenDocument and EPUB files: zip archives of XML.
func zipText(full, ext string) (string, error) {
	zr, err := zip.OpenReader(full)
	if err != nil {
		return "", err
	}
	defer zr.Close()
	var parts []*zip.File
	for _, f := range zr.File {
		n := f.Name
		switch ext {
		case "docx":
			if n == "word/document.xml" || strings.HasPrefix(n, "word/header") || strings.HasPrefix(n, "word/footer") || n == "word/footnotes.xml" {
				parts = append(parts, f)
			}
		case "xlsx":
			if n == "xl/sharedStrings.xml" || (strings.HasPrefix(n, "xl/worksheets/sheet") && strings.HasSuffix(n, ".xml")) {
				parts = append(parts, f)
			}
		case "pptx":
			if (strings.HasPrefix(n, "ppt/slides/slide") || strings.HasPrefix(n, "ppt/notesSlides/")) && strings.HasSuffix(n, ".xml") {
				parts = append(parts, f)
			}
		case "odt", "ods", "odp":
			if n == "content.xml" {
				parts = append(parts, f)
			}
		case "epub":
			if strings.HasSuffix(n, ".xhtml") || strings.HasSuffix(n, ".html") || strings.HasSuffix(n, ".htm") {
				parts = append(parts, f)
			}
		}
	}
	sort.SliceStable(parts, func(i, j int) bool { return naturalLess(parts[i].Name, parts[j].Name) })
	var out strings.Builder
	for _, f := range parts {
		if out.Len() >= MaxText {
			break
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		xmlText(io.LimitReader(rc, 64<<20), &out)
		rc.Close()
		out.WriteString("\n")
	}
	return clip(out.String()), nil
}

// xmlText writes an XML document's character data, with a space between elements so words
// in neighbouring cells, runs and paragraphs stay apart.
func xmlText(r io.Reader, out *strings.Builder) {
	d := xml.NewDecoder(r)
	d.Strict = false
	d.AutoClose = xml.HTMLAutoClose
	d.Entity = xml.HTMLEntity
	inSkip := 0
	for out.Len() < MaxText {
		tok, err := d.Token()
		if err != nil {
			return
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "script" || t.Name.Local == "style" {
				inSkip++
			}
		case xml.EndElement:
			if t.Name.Local == "script" || t.Name.Local == "style" {
				inSkip = max(0, inSkip-1)
			}
			switch t.Name.Local {
			case "p", "tr", "br", "div", "h1", "h2", "h3", "h4", "li", "si", "row", "tab":
				out.WriteString("\n")
			default:
				out.WriteString(" ")
			}
		case xml.CharData:
			if inSkip == 0 {
				out.Write(t)
			}
		}
	}
}

var tagRe = regexp.MustCompile(`(?s)<script.*?</script>|<style.*?</style>|<[^>]+>`)

func stripTags(s string) string { return tagRe.ReplaceAllString(s, " ") }

var spaceRe = regexp.MustCompile(`[ \t\r\f\v]+`)
var linesRe = regexp.MustCompile(`\n\s*\n+`)

// clip squeezes whitespace and caps the length on a character boundary.
func clip(s string) string {
	s = linesRe.ReplaceAllString(spaceRe.ReplaceAllString(s, " "), "\n")
	s = strings.TrimSpace(s)
	if len(s) > MaxText {
		s = s[:MaxText]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	return s
}

// naturalLess orders slide2 before slide10.
func naturalLess(a, b string) bool {
	na, nb := numSuffix(a), numSuffix(b)
	if na >= 0 && nb >= 0 && strings.TrimRight(strings.TrimSuffix(a, ".xml"), "0123456789") == strings.TrimRight(strings.TrimSuffix(b, ".xml"), "0123456789") {
		return na < nb
	}
	return a < b
}

func numSuffix(s string) int {
	s = strings.TrimSuffix(s, ".xml")
	i := len(s)
	for i > 0 && s[i-1] >= '0' && s[i-1] <= '9' {
		i--
	}
	if i == len(s) {
		return -1
	}
	n := 0
	for _, c := range s[i:] {
		n = n*10 + int(c-'0')
	}
	return n
}
