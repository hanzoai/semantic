package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hanzoai/semantic"
)

// Every reader is an Ingester, so any of them can be a pipeline's first stage.
var (
	_ semantic.Ingester = File{}
	_ semantic.Ingester = Dir{}
	_ semantic.Ingester = Glob{}
	_ semantic.Ingester = Web{}
	_ semantic.Ingester = Reader{}
	_ semantic.Ingester = Text{}
	_ semantic.Ingester = Func(nil)
	_ semantic.Ingester = (*Registry)(nil)
)

// tree lays down the fixture the Python file-ingestor tests use: three files
// at the top level, one in a subdirectory, one of them not valid UTF-8.
func tree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "test.txt"), []byte("Hello World"))
	write(t, filepath.Join(root, "test.pdf"), []byte("%PDF-1.4 content"))
	write(t, filepath.Join(root, "latin.txt"), []byte("Caf\xe9"))
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "sub", "sub.log"), []byte("Log content"))
	return root
}

func write(t *testing.T, p string, b []byte) {
	t.Helper()
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func names(docs []semantic.Doc) []string {
	out := make([]string, len(docs))
	for i, d := range docs {
		out[i] = filepath.Base(d.Source)
	}
	return out
}

func has(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// TestText ports the FileObject.text decoding cases: UTF-8 as itself,
// Latin-1 bytes as their characters, nothing as nothing.
func TestText(t *testing.T) {
	for _, c := range []struct {
		name string
		in   []byte
		want string
	}{
		{"utf8", []byte("Hello"), "Hello"},
		{"latin1", []byte("Caf\xe9"), "Café"},
		{"utf8 already", []byte("Café"), "Café"},
		{"empty", nil, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := text(c.in); got != c.want {
				t.Errorf("text(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestKind ports the FileTypeDetector cases: extension first, leading bytes
// when there is no extension, "unknown" when neither says anything.
func TestKind(t *testing.T) {
	for _, c := range []struct {
		name string
		body []byte
		want string
	}{
		{"file.tar.gz", nil, "gz"},
		{"movie.mp4", nil, "mp4"},
		{"REPORT.PDF", nil, "pdf"},
		{"sig", []byte("\x89PNG\r\n\x1a\n"), "png"},
		{"sig", []byte("%PDF-1.7"), "pdf"},
		{"sig", []byte("PAR1____"), "parquet"},
		{"unknown", nil, "unknown"},
		{"unknown", []byte("ab"), "unknown"},
	} {
		t.Run(c.name+"/"+c.want, func(t *testing.T) {
			if got := kind(c.name, c.body); got != c.want {
				t.Errorf("kind(%q, %q) = %q, want %q", c.name, c.body, got, c.want)
			}
		})
	}
}

func TestFile(t *testing.T) {
	root := tree(t)
	ctx := context.Background()

	docs, err := File{}.Ingest(ctx, filepath.Join(root, "test.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("got %d docs, want 1", len(docs))
	}
	d := docs[0]
	if d.Text != "Hello World" {
		t.Errorf("text = %q", d.Text)
	}
	if d.ID != d.Source {
		t.Errorf("id %q should be the source %q for a whole file", d.ID, d.Source)
	}
	if !filepath.IsAbs(d.Source) {
		t.Errorf("source %q should be absolute", d.Source)
	}
	o := OriginOf(d)
	if o.Size != 11 {
		t.Errorf("size = %d, want 11", o.Size)
	}
	if o.Type != "txt" {
		t.Errorf("type = %q, want txt", o.Type)
	}
	if !strings.HasPrefix(o.Mime, "text/plain") {
		t.Errorf("mime = %q, want text/plain", o.Mime)
	}
	if o.Mod.IsZero() || o.At.IsZero() {
		t.Errorf("mod %v and at %v should both be set", o.Mod, o.At)
	}
	// The hash is the content's, not the path's: the same bytes hash alike.
	same := t.TempDir()
	write(t, filepath.Join(same, "copy.txt"), []byte("Hello World"))
	twin, err := File{}.Ingest(ctx, filepath.Join(same, "copy.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if h := OriginOf(twin[0]).Hash; h != o.Hash {
		t.Errorf("hash %s != %s for identical content", h, o.Hash)
	}

	// Latin-1 content comes back as text, per the Python fallback.
	docs, err = File{}.Ingest(ctx, filepath.Join(root, "latin.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if docs[0].Text != "Café" {
		t.Errorf("latin.txt = %q, want Café", docs[0].Text)
	}
}

func TestFileRefused(t *testing.T) {
	root := tree(t)
	ctx := context.Background()

	if _, err := (File{}).Ingest(ctx, ""); !errors.Is(err, ErrEmpty) {
		t.Errorf("empty ref: %v, want ErrEmpty", err)
	}
	if _, err := (File{}).Ingest(ctx, filepath.Join(root, "ghost")); err == nil {
		t.Error("missing file: want an error")
	}
	if _, err := (File{}).Ingest(ctx, root); err == nil {
		t.Error("directory: want an error")
	}
	_, err := File{Max: 4}.Ingest(ctx, filepath.Join(root, "test.txt"))
	if !errors.Is(err, ErrSize) {
		t.Errorf("over Max: %v, want ErrSize", err)
	}
	if _, err := (File{Max: 4}).Ingest(ctx, filepath.Join(root, "latin.txt")); err != nil {
		t.Errorf("at Max: %v", err)
	}
	stop, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := (File{}).Ingest(stop, filepath.Join(root, "test.txt")); err == nil {
		t.Error("cancelled context: want an error")
	}
}

// TestFileURL checks the file:// spellings resolve to the same path.
func TestFileURL(t *testing.T) {
	root := tree(t)
	p := filepath.Join(root, "test.txt")
	for _, ref := range []string{p, "file://" + p, "file:" + p} {
		docs, err := File{}.Ingest(context.Background(), ref)
		if err != nil {
			t.Fatalf("%s: %v", ref, err)
		}
		if docs[0].Text != "Hello World" {
			t.Errorf("%s: text = %q", ref, docs[0].Text)
		}
	}
}

func TestDir(t *testing.T) {
	root := tree(t)
	ctx := context.Background()

	for _, c := range []struct {
		name string
		dir  Dir
		want []string
	}{
		{"whole tree", Dir{}, []string{"latin.txt", "sub.log", "test.pdf", "test.txt"}},
		{"top level", Dir{Depth: 1}, []string{"latin.txt", "test.pdf", "test.txt"}},
		{"match by extension", Dir{Match: []string{"*.txt"}}, []string{"latin.txt", "test.txt"}},
		{"match by path", Dir{Match: []string{"sub/*"}}, []string{"sub.log"}},
		{"skip", Dir{Skip: []string{"*.pdf", "*.log"}}, []string{"latin.txt", "test.txt"}},
		{"match then skip", Dir{Match: []string{"*.txt"}, Skip: []string{"latin*"}}, []string{"test.txt"}},
		{"max size", Dir{Max: 5}, []string{"latin.txt"}},
		{"min size", Dir{Min: 12}, []string{"test.pdf"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			docs, err := c.dir.Ingest(ctx, root)
			if err != nil {
				t.Fatal(err)
			}
			got := names(docs)
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for _, w := range c.want {
				if !has(got, w) {
					t.Errorf("got %v, missing %s", got, w)
				}
			}
		})
	}
}

func TestDirHidden(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "seen.txt"), []byte("a"))
	write(t, filepath.Join(root, ".hidden"), []byte("b"))
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, ".git", "config"), []byte("c"))

	docs, err := Dir{}.Ingest(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(docs); len(got) != 1 || got[0] != "seen.txt" {
		t.Errorf("got %v, want [seen.txt]", got)
	}

	docs, err = Dir{Hidden: true}.Ingest(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(docs); len(got) != 3 {
		t.Errorf("got %v, want three files", got)
	}
}

// TestDirKeepsWhatItRead checks that one unreadable file does not discard the
// documents the walk already collected.
func TestDirKeepsWhatItRead(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "ok.txt"), []byte("fine"))
	bad := filepath.Join(root, "bad.txt")
	write(t, bad, []byte("locked"))
	if err := os.Chmod(bad, 0o000); err != nil {
		t.Skip("cannot make a file unreadable here")
	}
	t.Cleanup(func() { os.Chmod(bad, 0o644) })
	if _, err := os.ReadFile(bad); err == nil {
		t.Skip("running as a user that ignores file permissions")
	}

	docs, err := Dir{}.Ingest(context.Background(), root)
	if err == nil {
		t.Fatal("want an error for the unreadable file")
	}
	if got := names(docs); len(got) != 1 || got[0] != "ok.txt" {
		t.Errorf("got %v, want the readable file back anyway", got)
	}
}

func TestDirRefused(t *testing.T) {
	root := tree(t)
	if _, err := (Dir{}).Ingest(context.Background(), filepath.Join(root, "test.txt")); err == nil {
		t.Error("a file is not a directory: want an error")
	}
	if _, err := (Dir{}).Ingest(context.Background(), ""); !errors.Is(err, ErrEmpty) {
		t.Error("want ErrEmpty")
	}
}

func TestGlob(t *testing.T) {
	root := tree(t)
	docs, err := Glob{}.Ingest(context.Background(), filepath.Join(root, "*.txt"))
	if err != nil {
		t.Fatal(err)
	}
	got := names(docs)
	if len(got) != 2 || !has(got, "latin.txt") || !has(got, "test.txt") {
		t.Errorf("got %v, want the two .txt files", got)
	}

	docs, err = Glob{}.Ingest(context.Background(), filepath.Join(root, "*.none"))
	if err != nil || len(docs) != 0 {
		t.Errorf("no matches: got %d docs, %v", len(docs), err)
	}
}

func TestScheme(t *testing.T) {
	root := tree(t)
	for _, c := range []struct {
		ref, want string
	}{
		{"", ""},
		{"-", "stdin"},
		{"https://example.com/a", "https"},
		{"HTTP://example.com", "http"},
		{"s3://bucket/key", "s3"},
		{"postgresql://host/db", "postgresql"},
		{filepath.Join(root, "*.txt"), "glob"},
		{root, "dir"},
		{filepath.Join(root, "test.txt"), "file"},
		{filepath.Join(root, "ghost.txt"), "file"},
		{"file://" + root, "file"},
		{"C:/notes/a.txt", "file"},
	} {
		if got := Scheme(c.ref); got != c.want {
			t.Errorf("Scheme(%q) = %q, want %q", c.ref, got, c.want)
		}
	}
}

func TestRegistry(t *testing.T) {
	root := tree(t)
	ctx := context.Background()

	// A reference resolves by shape: file, directory, pattern.
	docs, err := Default.Ingest(ctx, filepath.Join(root, "test.txt"))
	if err != nil || len(docs) != 1 {
		t.Fatalf("file: %d docs, %v", len(docs), err)
	}
	docs, err = Default.Ingest(ctx, root)
	if err != nil || len(docs) != 4 {
		t.Fatalf("dir: %d docs, %v", len(docs), err)
	}
	docs, err = Default.Ingest(ctx, filepath.Join(root, "*.txt"))
	if err != nil || len(docs) != 2 {
		t.Fatalf("glob: %d docs, %v", len(docs), err)
	}
	if _, err := Default.Ingest(ctx, " "); !errors.Is(err, ErrEmpty) {
		t.Errorf("blank ref: %v, want ErrEmpty", err)
	}
	if _, err := Default.Ingest(ctx, "s3://bucket/key"); !errors.Is(err, ErrScheme) {
		t.Errorf("unregistered scheme: %v, want ErrScheme", err)
	}
	if got := Default.Schemes(); len(got) != 6 {
		t.Errorf("Schemes() = %v, want the six built in", got)
	}
}

// TestDriver checks the seam a source this package does not implement uses:
// register under a scheme, or under a file extension, and be reached by the
// same call.
func TestDriver(t *testing.T) {
	ctx := context.Background()
	var r Registry // the zero registry is usable

	seen := ""
	r.Set("s3", Func(func(_ context.Context, ref string) ([]semantic.Doc, error) {
		seen = ref
		return []semantic.Doc{{ID: ref, Source: ref, Text: "from the bucket"}}, nil
	}))
	docs, err := r.Ingest(ctx, "s3://bucket/key")
	if err != nil {
		t.Fatal(err)
	}
	if seen != "s3://bucket/key" || docs[0].Text != "from the bucket" {
		t.Errorf("driver got %q, returned %+v", seen, docs)
	}

	// An extension claims the local paths that name it, ahead of File.
	root := t.TempDir()
	p := filepath.Join(root, "table.parquet")
	write(t, p, []byte("PAR1"))
	r.Set("file", File{})
	r.Set("parquet", Func(func(_ context.Context, ref string) ([]semantic.Doc, error) {
		return []semantic.Doc{{ID: ref, Text: "columns"}}, nil
	}))
	docs, err = r.Ingest(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if docs[0].Text != "columns" {
		t.Errorf("extension driver: got %q", docs[0].Text)
	}
	r.Drop("parquet")
	docs, err = r.Ingest(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if docs[0].Text != "PAR1" {
		t.Errorf("after Drop: got %q, want the file read as bytes", docs[0].Text)
	}
}

func TestJSON(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	p := filepath.Join(root, "people.json")
	write(t, p, []byte(`[{"name":"Ada","note":"first"},{"name":"Alan","note":"second"}]`))
	docs, err := File{}.Ingest(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("got %d docs, want one per element", len(docs))
	}
	if got := docs[1].ID; !strings.HasSuffix(got, "#1") {
		t.Errorf("id = %q, want the record's position", got)
	}
	if r := Record(docs[0]); r["name"] != "Ada" {
		t.Errorf("record = %v", r)
	}
	if !strings.Contains(docs[0].Text, "name: Ada") {
		t.Errorf("text = %q, want the record rendered", docs[0].Text)
	}

	// Field picks the text and leaves the rest of the record in place.
	docs, err = File{Decode: JSON{Field: "note"}}.Ingest(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if docs[0].Text != "first" || docs[1].Text != "second" {
		t.Errorf("texts = %q, %q", docs[0].Text, docs[1].Text)
	}
	if Record(docs[1])["name"] != "Alan" {
		t.Error("the record should survive alongside the chosen field")
	}

	// A lone object is one document; a lone array of scalars is one each.
	one := filepath.Join(root, "one.json")
	write(t, one, []byte(`{"name":"Grace"}`))
	docs, err = File{}.Ingest(ctx, one)
	if err != nil || len(docs) != 1 {
		t.Fatalf("object: %d docs, %v", len(docs), err)
	}
	if docs[0].ID != docs[0].Source {
		t.Errorf("a whole-source document keeps the plain id, got %q", docs[0].ID)
	}
	list := filepath.Join(root, "list.json")
	write(t, list, []byte(`["alpha","beta"]`))
	docs, err = File{}.Ingest(ctx, list)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 || docs[0].Text != "alpha" || docs[1].Text != "beta" {
		t.Errorf("scalars: %+v", docs)
	}

	bad := filepath.Join(root, "bad.json")
	write(t, bad, []byte(`{"name":`))
	if _, err := (File{}).Ingest(ctx, bad); err == nil {
		t.Error("malformed JSON: want an error")
	}
}

func TestLines(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "log.jsonl")
	body := "{\"n\":1}\n\n{\"n\":2}\n"
	write(t, p, []byte(body))

	docs, err := File{}.Ingest(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("got %d docs, want 2 (the blank line is not a record)", len(docs))
	}
	// Offsets point at the bytes each record came from.
	if got := OriginOf(docs[0]).Offset; got != 0 {
		t.Errorf("first offset = %d, want 0", got)
	}
	if got := OriginOf(docs[1]).Offset; got != 9 {
		t.Errorf("second offset = %d, want 9", got)
	}
	if body[9:16] != `{"n":2}` {
		t.Fatalf("fixture drifted: %q", body[9:16])
	}
	if Record(docs[1])["n"] != float64(2) {
		t.Errorf("record = %v", Record(docs[1]))
	}

	torn := filepath.Join(root, "torn.jsonl")
	write(t, torn, []byte("{\"n\":1}\n{oops}\n"))
	if _, err := (File{}).Ingest(context.Background(), torn); err == nil {
		t.Error("malformed line: want an error naming the offset")
	} else if !strings.Contains(err.Error(), "offset 8") {
		t.Errorf("error should say where it broke: %v", err)
	}
}

// TestRows ports the delimiter, quoting and header cases from the Python CSV
// tests.
func TestRows(t *testing.T) {
	for _, c := range []struct {
		name string
		body string
		rows Rows
		want []map[string]string
	}{
		{
			name: "comma",
			body: "a,b\n1,2\n",
			want: []map[string]string{{"a": "1", "b": "2"}},
		},
		{
			name: "semicolon",
			body: "a;b\n1;2\n",
			want: []map[string]string{{"a": "1", "b": "2"}},
		},
		{
			name: "pipe",
			body: "a|b\n1|2\n",
			want: []map[string]string{{"a": "1", "b": "2"}},
		},
		{
			name: "tab",
			body: "user_id\trole\n1\tadmin\n2\tuser\n",
			want: []map[string]string{
				{"user_id": "1", "role": "admin"},
				{"user_id": "2", "role": "user"},
			},
		},
		{
			name: "quoted commas",
			body: "company,revenue\n\"Acme, Inc.\",100\n\"Widgets, LLC\",200\n",
			want: []map[string]string{
				{"company": "Acme, Inc.", "revenue": "100"},
				{"company": "Widgets, LLC", "revenue": "200"},
			},
		},
		{
			name: "newline inside a quoted field",
			body: "id,notes\n1,\"line1\nline2\"\n2,\"alpha\nbeta\"\n",
			want: []map[string]string{
				{"id": "1", "notes": "line1\nline2"},
				{"id": "2", "notes": "alpha\nbeta"},
			},
		},
		{
			name: "named columns make the first row data",
			body: "colA,colB\nx,1\ny,2\n",
			rows: Rows{Cols: []string{"0", "1"}},
			want: []map[string]string{
				{"0": "colA", "1": "colB"},
				{"0": "x", "1": "1"},
				{"0": "y", "1": "2"},
			},
		},
		{
			name: "empty cells stay empty",
			body: "name,score\nalice,\nbob,10\n",
			want: []map[string]string{
				{"name": "alice", "score": ""},
				{"name": "bob", "score": "10"},
			},
		},
		{
			name: "ragged rows are data",
			body: "a,b,c\n1,2\n3,4,5,6\n",
			want: []map[string]string{
				{"a": "1", "b": "2"},
				{"a": "3", "b": "4", "c": "5", "3": "6"},
			},
		},
		{
			name: "latin-1 source",
			body: "name,city\nJos\xe9,S\xe3o Paulo\n",
			want: []map[string]string{{"name": "José", "city": "São Paulo"}},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			docs, err := c.rows.Decode([]byte(c.body), Origin{Ref: "t.csv"})
			if err != nil {
				t.Fatal(err)
			}
			if len(docs) != len(c.want) {
				t.Fatalf("got %d rows, want %d", len(docs), len(c.want))
			}
			for i, want := range c.want {
				rec := Record(docs[i])
				if len(rec) != len(want) {
					t.Errorf("row %d = %v, want %v", i, rec, want)
					continue
				}
				for k, v := range want {
					if rec[k] != v {
						t.Errorf("row %d [%s] = %v, want %q", i, k, rec[k], v)
					}
				}
			}
		})
	}
}

func TestRowsText(t *testing.T) {
	body := []byte("name,city\nAda,London\n")

	docs, err := Rows{}.Decode(body, Origin{Ref: "t.csv"})
	if err != nil {
		t.Fatal(err)
	}
	if docs[0].Text != "city: London\nname: Ada" {
		t.Errorf("text = %q, want every column in a stable order", docs[0].Text)
	}
	if got := OriginOf(docs[0]).Offset; got != 10 {
		t.Errorf("offset = %d, want the row's byte offset 10", got)
	}

	docs, err = Rows{Field: "city"}.Decode(body, Origin{Ref: "t.csv"})
	if err != nil {
		t.Fatal(err)
	}
	if docs[0].Text != "London" {
		t.Errorf("Field text = %q, want London", docs[0].Text)
	}

	docs, err = Rows{}.Decode(nil, Origin{Ref: "t.csv"})
	if err != nil || len(docs) != 0 {
		t.Errorf("empty source: %d docs, %v", len(docs), err)
	}
}

// TestTSV checks that the file extension alone picks the tab reader, without
// sniffing.
func TestTSV(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "t.tsv")
	write(t, p, []byte("a\tb\n1\t2\n"))
	docs, err := File{}.Ingest(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || Record(docs[0])["a"] != "1" {
		t.Errorf("got %+v", docs)
	}
}

func TestHTML(t *testing.T) {
	page := `<html><head><title>  Ada &amp; Alan </title>
	<style>body{color:red}</style></head>
	<body><!-- hidden > note --><h1>Heads</h1>
	<p>Text with an entity: &lt;tag&gt;</p>
	<script>var x = "not text";</script>
	</body></html>`

	docs, err := HTML{}.Decode([]byte(page), Origin{Ref: "p.html"})
	if err != nil {
		t.Fatal(err)
	}
	body := docs[0].Text
	for _, gone := range []string{"color:red", "not text", "hidden", "<h1>", "&amp;"} {
		if strings.Contains(body, gone) {
			t.Errorf("text still holds %q: %q", gone, body)
		}
	}
	for _, kept := range []string{"Heads", "Text with an entity: <tag>"} {
		if !strings.Contains(body, kept) {
			t.Errorf("text lost %q: %q", kept, body)
		}
	}
	if got := Title(docs[0]); got != "Ada & Alan" {
		t.Errorf("title = %q, want Ada & Alan", got)
	}
}

func TestReader(t *testing.T) {
	ctx := context.Background()

	docs, err := Reader{R: strings.NewReader("plain bytes"), Source: "buf"}.Ingest(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if docs[0].Text != "plain bytes" || docs[0].Source != "buf" {
		t.Errorf("got %+v", docs[0])
	}
	if got := OriginOf(docs[0]).Size; got != 11 {
		t.Errorf("size = %d, want 11", got)
	}

	docs, err = Reader{
		R:    strings.NewReader(`{"a":1}` + "\n" + `{"a":2}`),
		Type: "jsonl",
	}.Ingest(ctx, "events")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 || docs[0].Source != "events" {
		t.Errorf("got %d docs from %q", len(docs), docs[0].Source)
	}

	docs, err = Text{}.Ingest(ctx, "just words")
	if err != nil {
		t.Fatal(err)
	}
	if docs[0].Text != "just words" || docs[0].Source != "text" {
		t.Errorf("got %+v", docs[0])
	}

	docs, err = Text{Type: "csv", Source: "inline"}.Ingest(ctx, "a,b\n1,2\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || Record(docs[0])["b"] != "2" {
		t.Errorf("got %+v", docs)
	}
}

// TestStdin checks that the zero Reader reads standard input, which is what
// the "stdin" scheme resolves to.
func TestStdin(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old })
	go func() {
		fmt.Fprint(w, "piped in")
		w.Close()
	}()

	docs, err := Default.Ingest(context.Background(), "-")
	if err != nil {
		t.Fatal(err)
	}
	if docs[0].Text != "piped in" || docs[0].Source != "stdin" {
		t.Errorf("got %+v", docs[0])
	}
}

func TestWeb(t *testing.T) {
	mod := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/people.json":
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Last-Modified", mod.Format(http.TimeFormat))
			fmt.Fprint(w, `[{"name":"Ada"},{"name":"Alan"}]`)
		case "/page":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<html><title>Page</title><body>Hello</body></html>")
		case "/moved":
			http.Redirect(w, r, "/page", http.StatusFound)
		case "/agent":
			fmt.Fprint(w, r.UserAgent()+" "+r.Header.Get("X-Token"))
		case "/big":
			fmt.Fprint(w, strings.Repeat("x", 100))
		default:
			http.Error(w, "no", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	web := Web{Private: true} // the test server listens on loopback

	docs, err := web.Ingest(ctx, srv.URL+"/people.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("got %d docs, want one per element", len(docs))
	}
	o := OriginOf(docs[0])
	if o.Status != 200 {
		t.Errorf("status = %d", o.Status)
	}
	if o.Type != "json" {
		t.Errorf("type = %q, want json from the content type", o.Type)
	}
	if o.Mime != "application/json" {
		t.Errorf("mime = %q", o.Mime)
	}
	if !o.Mod.Equal(mod) {
		t.Errorf("mod = %v, want %v from Last-Modified", o.Mod, mod)
	}
	if Record(docs[1])["name"] != "Alan" {
		t.Errorf("record = %v", Record(docs[1]))
	}

	docs, err = web.Ingest(ctx, srv.URL+"/page")
	if err != nil {
		t.Fatal(err)
	}
	if docs[0].Text != "Hello" || Title(docs[0]) != "Page" {
		t.Errorf("html: text %q title %q", docs[0].Text, Title(docs[0]))
	}

	docs, err = web.Ingest(ctx, srv.URL+"/moved")
	if err != nil {
		t.Fatal(err)
	}
	if got := docs[0].Source; !strings.HasSuffix(got, "/page") {
		t.Errorf("source = %q, want the URL the redirect landed on", got)
	}

	docs, err = Web{Private: true, Agent: "reader/1", Header: http.Header{
		"X-Token": []string{"abc"},
	}}.Ingest(ctx, srv.URL+"/agent")
	if err != nil {
		t.Fatal(err)
	}
	if docs[0].Text != "reader/1 abc" {
		t.Errorf("headers not sent: %q", docs[0].Text)
	}

	if _, err := web.Ingest(ctx, srv.URL+"/missing"); err == nil {
		t.Error("404: want an error")
	}
	_, err = Web{Private: true, Max: 10}.Ingest(ctx, srv.URL+"/big")
	if !errors.Is(err, ErrSize) {
		t.Errorf("over Max: %v, want ErrSize", err)
	}
	if _, err := web.Ingest(ctx, ""); !errors.Is(err, ErrEmpty) {
		t.Errorf("empty ref: %v", err)
	}
}

// TestWebBlocked checks the address rules: without Private, a fetch aimed
// inside the network is refused before it is sent.
func TestWebBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "secret")
	}))
	defer srv.Close()

	if _, err := (Web{}).Ingest(context.Background(), srv.URL); !errors.Is(err, ErrBlocked) {
		t.Errorf("loopback server: %v, want ErrBlocked", err)
	}

	for _, ref := range []string{
		"http://127.0.0.1/x",
		"http://localhost:8080/x",
		"http://api.localhost/x",
		"http://10.0.0.1/x",
		"http://192.168.1.1/x",
		"http://172.16.0.1/x",
		"http://169.254.169.254/latest/meta-data/",
		"http://100.64.0.1/x",
		"http://[::1]/x",
		"http://[fd00::1]/x",
		"http://0.0.0.0/x",
		"file:///etc/passwd",
		"gopher://example.com/x",
		"http:///nohost",
	} {
		if _, err := (Web{}).Ingest(context.Background(), ref); !errors.Is(err, ErrBlocked) {
			t.Errorf("%s: %v, want ErrBlocked", ref, err)
		}
	}

	// Public addresses pass the rules; they are not fetched here.
	for _, ip := range []string{"8.8.8.8", "1.1.1.1", "2606:4700::1111"} {
		if internal(parse(t, ip)) {
			t.Errorf("%s should be reachable", ip)
		}
	}
}

func parse(t *testing.T, s string) net.IP {
	t.Helper()
	ip := net.ParseIP(s)
	if ip == nil {
		t.Fatalf("bad fixture address %q", s)
	}
	return ip
}

func TestOriginRoundTrip(t *testing.T) {
	o := Origin{
		Ref:    "notes.txt",
		Offset: 12,
		Size:   345,
		Mod:    time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Hash:   "abc123",
		Type:   "txt",
		Mime:   "text/plain",
		Status: 200,
		At:     time.Date(2026, 1, 2, 3, 4, 6, 0, time.UTC),
	}
	d := semantic.Doc{ID: "notes.txt", Source: o.Ref, Meta: o.Meta()}
	if got := OriginOf(d); got != o {
		t.Errorf("OriginOf = %+v, want %+v", got, o)
	}

	// A document that has been through JSON still reports where it came from.
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var back semantic.Doc
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	got := OriginOf(back)
	if !got.Mod.Equal(o.Mod) || got.Size != o.Size || got.Offset != o.Offset ||
		got.Hash != o.Hash || got.Status != o.Status {
		t.Errorf("after JSON: %+v, want %+v", got, o)
	}
}

// TestPipeline runs the registry as a pipeline's first stage, which is the
// reason every reader here returns semantic.Docs.
func TestPipeline(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a.txt"), []byte("Ada wrote the first program"))

	p := semantic.Pipeline{
		Ingest:  Default,
		Extract: words{},
	}
	out, err := p.Run(context.Background(), filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Subject != "Ada" {
		t.Fatalf("got %+v", out)
	}
	if out[0].From.Text == "" {
		t.Error("the triple lost the chunk it came from")
	}
}

// words is the smallest extractor that proves the wiring: the first word is
// the subject of one triple.
type words struct{}

func (words) Extract(_ context.Context, c semantic.Chunk) ([]semantic.Triple, error) {
	f := strings.Fields(c.Text)
	if len(f) == 0 {
		return nil, nil
	}
	return []semantic.Triple{{Subject: f[0], Predicate: "said", Object: c.Text, From: c}}, nil
}
