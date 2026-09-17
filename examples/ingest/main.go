// Command ingest turns references into documents. A path, a directory, a
// glob, a URL, a stream, a string already in hand: each is read by one reader,
// and every document that comes back carries the origin it was read from —
// reference, byte offset, size, modification time, content hash, type.
//
// The HTTP part runs against a server this program starts on loopback, so the
// example needs no network. It also shows the refusal that guards a fetch of
// an attacker-supplied URL: an internal address is blocked unless the caller
// says otherwise.
//
//	go run ./examples/ingest
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/hanzoai/semantic"
	"github.com/hanzoai/semantic/ingest"
)

func main() {
	ctx := context.Background()
	dir := corpus()

	// One file, one document, and the provenance a claim read out of it will
	// need later.
	docs, err := ingest.File{}.Ingest(ctx, filepath.Join(dir, "brief.md"))
	if err != nil {
		log.Fatal(err)
	}
	o := ingest.OriginOf(docs[0])
	fmt.Println("file")
	fmt.Printf("  %s  %s  %d bytes  sha256:%s…  modified %s\n",
		filepath.Base(o.Ref), o.Type, o.Size, o.Hash[:12], o.Mod.Format("15:04:05"))

	// A JSON array is one document per element, not one document. Field says
	// which member holds the text; the whole record stays reachable.
	docs, err = ingest.File{Decode: ingest.JSON{Field: "body"}}.Ingest(ctx, filepath.Join(dir, "notes.json"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\njson  %d documents from one file\n", len(docs))
	for _, d := range docs {
		fmt.Printf("  %-16s %q  record id %v\n", short(d.ID, dir), d.Text, ingest.Record(d)["id"])
	}

	// JSON Lines the same way, and each document's offset is the byte offset
	// of its own line, so a claim traces back to the exact bytes.
	docs, err = ingest.File{Decode: ingest.Lines{Field: "body"}}.Ingest(ctx, filepath.Join(dir, "notes.jsonl"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\njsonl %d documents, each at its own offset\n", len(docs))
	for _, d := range docs {
		fmt.Printf("  offset %3d  %q\n", ingest.OriginOf(d).Offset, d.Text)
	}

	// Delimited text: one document per row, the separator sniffed from the
	// header, every column rendered as a line so an extractor has something
	// to read.
	docs, err = ingest.File{}.Ingest(ctx, filepath.Join(dir, "roster.csv"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\ncsv   %d documents, separator sniffed from the header\n", len(docs))
	fmt.Printf("  %s\n", strings.ReplaceAll(docs[0].Text, "\n", " · "))

	// A directory walk, filtered. Depth 1 is the directory itself.
	docs, err = ingest.Dir{Match: []string{"*.md"}, Depth: 1}.Ingest(ctx, dir)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\ndir   *.md at depth 1: %d of the %d files in the tree\n", len(docs), count(dir))

	// A glob, in sorted order.
	docs, err = ingest.Glob{}.Ingest(ctx, filepath.Join(dir, "*.jsonl"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("glob  *.jsonl: %d documents\n", len(docs))

	// A stream. The zero Reader reads standard input; give it any io.Reader
	// and the same code reads a pipe, a socket or a decompressor.
	docs, err = ingest.Reader{R: strings.NewReader("Ada Lovelace works for Babbage Engines."), Source: "notes"}.Ingest(ctx, "-")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\nread  %q from %s\n", docs[0].Text, docs[0].Source)

	// Text already in hand. There is no location, so the reference is the
	// body itself.
	docs, err = ingest.Text{Source: "clipboard"}.Ingest(ctx, "Babbage Engines is headquartered in Montréal.")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("text  %q from %s\n", docs[0].Text, docs[0].Source)

	web(ctx)
	routing(ctx, dir)
}

// web fetches over real HTTP from a server on loopback, and shows the guard
// that stands between a fetch and the network the fetching machine can see.
func web(ctx context.Context) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><title>Engines</title></head>`+
			`<body><script>ignored()</script><p>Charles Babbage founded Babbage Engines.</p></body></html>`)
	}))
	defer srv.Close()

	fmt.Println("\nhttp")

	// The default refuses an internal address: a URL is usually
	// attacker-influenced, and the fetching machine sees more of the network
	// than the person asking.
	if _, err := (ingest.Web{}).Ingest(ctx, srv.URL); errors.Is(err, ingest.ErrBlocked) {
		fmt.Printf("  %s refused: %v\n", srv.URL, err)
	}

	// A trusted internal deployment says so, once, in the reader.
	docs, err := ingest.Web{Private: true}.Ingest(ctx, srv.URL)
	if err != nil {
		log.Fatal(err)
	}
	o := ingest.OriginOf(docs[0])
	fmt.Printf("  status %d  %s  title %q\n", o.Status, o.Mime, ingest.Title(docs[0]))
	fmt.Printf("  text   %q\n", strings.TrimSpace(docs[0].Text))
}

// routing shows the registry: one ingester that reads whatever it is pointed
// at, and the seam a source this package does not implement goes through.
func routing(ctx context.Context, dir string) {
	fmt.Println("\nregistry")
	for _, ref := range []string{
		filepath.Join(dir, "brief.md"),
		filepath.Join(dir, "*.json"),
		dir,
		"https://example.com/page",
		"-",
	} {
		fmt.Printf("  %-24s → %s\n", short(ref, dir), ingest.Scheme(ref))
	}

	// A driver for a source this package does not read registers under its
	// scheme and is reached by the same call as the rest.
	var reg ingest.Registry
	reg.Set("file", ingest.File{})
	reg.Set("memo", ingest.Func(func(_ context.Context, ref string) ([]semantic.Doc, error) {
		id := strings.TrimPrefix(ref, "memo://")
		return []semantic.Doc{{ID: id, Source: ref, Text: "the memo about " + id}}, nil
	}))
	docs, err := reg.Ingest(ctx, "memo://q3")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  schemes %v\n", reg.Schemes())
	fmt.Printf("  memo://q3 → %q\n", docs[0].Text)

	// And a scheme nobody registered says so, rather than reading nothing.
	if _, err := reg.Ingest(ctx, "s3://bucket/key"); errors.Is(err, ingest.ErrScheme) {
		fmt.Printf("  s3://bucket/key → %v\n", err)
	}
}

// corpus writes the files the readers above read.
func corpus() string {
	dir, err := os.MkdirTemp("", "semantic-ingest")
	if err != nil {
		log.Fatal(err)
	}
	files := map[string]string{
		"brief.md":    "# Brief\n\nAda Lovelace works for Babbage Engines.\n",
		"notes.json":  `[{"id":"n1","body":"Babbage Engines was founded by Charles Babbage."},` + "\n" + ` {"id":"n2","body":"It is headquartered in Montréal."}]`,
		"notes.jsonl": `{"id":"n1","body":"Ada Lovelace joined in 1843."}` + "\n" + `{"id":"n2","body":"She works for Babbage Engines."}` + "\n",
		"roster.csv":  "name;role;city\nAda Lovelace;analyst;Montréal\nCharles Babbage;founder;London\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			log.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "archive"), 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "archive", "old.md"), []byte("# Old\n\nSuperseded.\n"), 0o600); err != nil {
		log.Fatal(err)
	}
	return dir
}

// count is how many files the tree holds, so the filtered walk above has
// something to be measured against.
func count(dir string) int {
	n := 0
	filepath.WalkDir(dir, func(_ string, e os.DirEntry, err error) error {
		if err == nil && !e.IsDir() {
			n++
		}
		return nil
	})
	return n
}

// short trims the temporary directory off a path so the output is stable.
func short(ref, dir string) string {
	if s, ok := strings.CutPrefix(ref, dir); ok {
		return "." + s
	}
	return ref
}
