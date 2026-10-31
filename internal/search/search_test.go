package search

import (
	"archive/zip"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func docx(t *testing.T, root, rel, text string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	f, _ := os.Create(p)
	zw := zip.NewWriter(f)
	w, _ := zw.Create("word/document.xml")
	io.WriteString(w, `<?xml version="1.0"?><w:document xmlns:w="x"><w:body><w:p><w:r><w:t>`+text+`</w:t></w:r></w:p><w:p><w:r><w:t>Second</w:t></w:r></w:p></w:body></w:document>`)
	zw.Close()
	f.Close()
}

func setup(t *testing.T) (*Index, string, string) {
	t.Helper()
	a, b := t.TempDir(), t.TempDir()
	write(t, a, "Invoices/2026-Invoice-OVH.pdf", "%PDF-1.4 not a real pdf")
	write(t, a, "Notes/recept.md", "Ćevapi sa lukom i kajmakom. Đuveč za vikend.")
	write(t, a, "Notes/todo.txt", "buy milk\npay the electricity bill")
	write(t, a, ".hidden/secret.txt", "electricity hidden")
	docx(t, a, "Work/Report Q3.docx", "Quarterly revenue grew in Banja Luka")
	write(t, b, "Other/electricity.txt", "someone else's bill")
	roots := []Root{{Key: "home:1", Dir: a}, {Key: "home:2", Dir: b}}
	ix, err := Open(filepath.Join(t.TempDir(), "search.db"), slog.New(slog.NewTextHandler(io.Discard, nil)), func(context.Context) []Root { return roots })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	ctx := context.Background()
	ix.fullPass(ctx, true)
	for {
		n, err := ix.extractBatch(ctx, 50)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
	}
	return ix, a, b
}

func names(h []Hit) string {
	var out []string
	for _, x := range h {
		out = append(out, x.Match+":"+x.Space+x.Path)
	}
	return strings.Join(out, " ")
}

var mine = []Scope{{Key: "home:1", Space: "home"}}

func TestNamesMatchAnyPart(t *testing.T) {
	ix, _, _ := setup(t)
	h, err := ix.Search(context.Background(), Parse("voice ovh"), mine, 20)
	if err != nil {
		t.Fatal(err)
	}
	if names(h) != "name:home/Invoices/2026-Invoice-OVH.pdf" {
		t.Fatalf("got %s", names(h))
	}
	// Folder names match too, and short words fall back to LIKE.
	h, _ = ix.Search(context.Background(), Parse("Q3"), mine, 20)
	if names(h) != "name:home/Work/Report Q3.docx" {
		t.Fatalf("short word: %s", names(h))
	}
}

func TestContentWithoutAccentsAndSnippets(t *testing.T) {
	ix, _, _ := setup(t)
	h, _ := ix.Search(context.Background(), Parse("cevapi"), mine, 20)
	if len(h) != 1 || h[0].Path != "/Notes/recept.md" || !strings.Contains(h[0].Snippet, "") {
		t.Fatalf("cevapi: %+v", h)
	}
	h, _ = ix.Search(context.Background(), Parse("djuvec"), mine, 20)
	if len(h) != 1 {
		t.Fatalf("đ typed as dj: %s", names(h))
	}
	h, _ = ix.Search(context.Background(), Parse("revenue banja"), mine, 20)
	if names(h) != "content:home/Work/Report Q3.docx" {
		t.Fatalf("docx text: %s", names(h))
	}
	h, _ = ix.Search(context.Background(), Parse(`"banja luka" ext:docx`), mine, 20)
	if len(h) != 1 {
		t.Fatalf("phrase + ext: %s", names(h))
	}
	h, _ = ix.Search(context.Background(), Parse("revenue -banja"), mine, 20)
	if len(h) != 0 {
		t.Fatalf("exclusion: %s", names(h))
	}
}

func TestOnlyOwnScopesAndNoHiddenFiles(t *testing.T) {
	ix, _, _ := setup(t)
	h, _ := ix.Search(context.Background(), Parse("electricity"), mine, 20)
	if names(h) != "content:home/Notes/todo.txt" {
		t.Fatalf("got %s", names(h))
	}
	// A share of person 2's Other folder, as person 1 sees it.
	share := []Scope{{Key: "home:2", Prefix: "/Other", Space: "share:9", Base: "/Other"}}
	h, _ = ix.Search(context.Background(), Parse("electricity"), share, 20)
	if names(h) != "name:share:9/electricity.txt" {
		t.Fatalf("share: %s", names(h))
	}
}

func TestChangesAndDeletes(t *testing.T) {
	ix, a, _ := setup(t)
	ctx := context.Background()
	_ = os.Remove(filepath.Join(a, "Notes", "todo.txt"))
	time.Sleep(10 * time.Millisecond)
	write(t, a, "Notes/recept.md", "Pita od jabuka")
	ix.Touch("home:1", "/Notes")
	ix.flushDirty(ctx)
	ix.extractBatch(ctx, 50)
	if h, _ := ix.Search(ctx, Parse("electricity"), mine, 20); len(h) != 0 {
		t.Fatalf("deleted file still found: %s", names(h))
	}
	if h, _ := ix.Search(ctx, Parse("cevapi"), mine, 20); len(h) != 0 {
		t.Fatalf("old text still found: %s", names(h))
	}
	if h, _ := ix.Search(ctx, Parse("jabuka"), mine, 20); len(h) != 1 {
		t.Fatalf("new text not found: %s", names(h))
	}
	// A whole folder removed.
	_ = os.RemoveAll(filepath.Join(a, "Work"))
	ix.Touch("home:1", "/Work")
	ix.flushDirty(ctx)
	if h, _ := ix.Search(ctx, Parse("report"), mine, 20); len(h) != 0 {
		t.Fatalf("removed folder still found: %s", names(h))
	}
}

func TestParse(t *testing.T) {
	q := Parse(`tax "annual report" -draft ext:pdf,docx type:file`)
	if strings.Join(q.Terms, "|") != "tax" || strings.Join(q.Phrases, "|") != "annual report" || strings.Join(q.Excludes, "|") != "draft" ||
		strings.Join(q.Exts, "|") != "pdf|docx" || q.Type != "file" {
		t.Fatalf("%+v", q)
	}
}
