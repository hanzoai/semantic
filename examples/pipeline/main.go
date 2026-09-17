// Command pipeline carries one document through every stage — ingest, parse,
// normalize, split, extract, fold into a graph, write it out — and prints what
// crossed each seam. It is the shortest honest answer to "what does this
// module do".
//
// Nothing here reaches the network and nothing asks a model: the extractor is
// rules over a gazetteer, so the triples are a function of the text.
//
//	go run ./examples/pipeline
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/hanzoai/semantic"
	"github.com/hanzoai/semantic/export"
	"github.com/hanzoai/semantic/extract"
	"github.com/hanzoai/semantic/ingest"
	"github.com/hanzoai/semantic/kg"
	"github.com/hanzoai/semantic/normalize"
	"github.com/hanzoai/semantic/parse"
	"github.com/hanzoai/semantic/split"
	"github.com/hanzoai/semantic/store"
)

// notes is the corpus. Two characters in it are the point: the space in
// "Ada Lovelace" is U+00A0 and the é in "Montréal" is a bare e with a
// combining acute. Both look right on screen and neither matches the
// gazetteer, so without normalize two of the three sentences say nothing.
const notes = "# Field notes\n" +
	"\n" +
	"Ada\u00a0Lovelace works for Babbage Engines.\n" +
	"\n" +
	"Babbage Engines was founded by Charles Babbage.\n" +
	"\n" +
	"Babbage Engines is headquartered in Montre\u0301al.\n"

// gazetteer is what the reader knows before it reads: names written the way
// a person types them, composed and with ordinary spaces.
var gazetteer = map[string]string{
	"Ada Lovelace":    "PERSON",
	"Charles Babbage": "PERSON",
	"Babbage Engines": "ORG",
	"Montr\u00e9al":   "LOC",
}

// clean is parse and normalize in the one slot a Pipeline gives them.
// normalize is a set of string transforms rather than a Parser, so composing
// the two is the caller's line of code and this is that line.
type clean struct{ parse semantic.Parser }

func (c clean) Parse(ctx context.Context, d semantic.Doc) (semantic.Doc, error) {
	d, err := c.parse.Parse(ctx, d)
	if err != nil {
		return d, err
	}
	d.Text = normalize.Text(d.Text)
	return d, nil
}

func main() {
	ctx := context.Background()
	path := write()

	// Ingest. The reference is a path; what comes back is a document that
	// knows where it came from.
	docs, err := ingest.File{}.Ingest(ctx, path)
	if err != nil {
		log.Fatal(err)
	}
	d := docs[0]
	o := ingest.OriginOf(d)
	fmt.Printf("ingest    %s  %d bytes  sha256:%s…\n", o.Type, o.Size, o.Hash[:12])

	// Parse, then normalize. The format is read from the source, the heading
	// becomes the title, and the origin ingest recorded is left alone.
	if d, err = (clean{parse.Any{}}).Parse(ctx, d); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("parse     format=%v title=%q\n", d.Meta["format"], parse.Title(d))
	fmt.Printf("normalize non-breaking space gone: %v, é composed: %v\n",
		!strings.ContainsRune(d.Text, '\u00a0'), strings.Contains(d.Text, "Montr\u00e9al"))

	// Split. One sentence per chunk. The chunks tile the document: joined
	// back up with no overlap they are the text, byte for byte.
	chunks, err := split.Sentences{Max: 1}.Split(ctx, d)
	if err != nil {
		log.Fatal(err)
	}
	var joined strings.Builder
	for _, c := range chunks {
		joined.WriteString(c.Text)
	}
	fmt.Printf("split     %d chunks, tiling the document exactly: %v\n", len(chunks), joined.String() == d.Text)

	// Extract. Deterministic rules over the gazetteer: no model, so the
	// answer can be written down before the program runs.
	rules := extract.Rules{Names: gazetteer}
	var triples []semantic.Triple
	for _, c := range chunks {
		ts, err := rules.Extract(ctx, c)
		if err != nil {
			log.Fatal(err)
		}
		triples = append(triples, ts...)
	}
	fmt.Printf("extract   %d triples\n", len(triples))
	for _, t := range triples {
		fmt.Printf("          %-16s %-12s %-16s  %.2f  from chunk %d\n",
			t.Subject, t.Predicate, t.Object, t.Score, t.From.Index)
	}

	// Fold. The same assertion arriving twice is one edge, not two, so
	// repetition raises confidence rather than duplicating structure.
	g := kg.Build(triples)
	nodes, edges := g.Size()
	g.Add(triples[0])
	again, stillEdges := g.Size()
	st := g.Stat()
	fmt.Printf("kg        %d nodes, %d edges; asserting one of them again: %d nodes, %d edges\n",
		nodes, edges, again, stillEdges)
	fmt.Printf("          density %.2f, %d component, shape %s\n", st.Density, st.Parts, st.Shape)

	// Write it out. The graph is served through store.Triple, so this last
	// stage would read the same against a database.
	var out store.Triple = kg.Keep(g)
	var doc strings.Builder
	if err := export.Default.Write(ctx, &doc, export.Match(out, "", "", ""), "ttl"); err != nil {
		log.Fatal(err)
	}
	fmt.Println("export    turtle")
	for line := range strings.SplitSeq(strings.TrimRight(doc.String(), "\n"), "\n") {
		fmt.Printf("          %s\n", line)
	}
}

// write puts the corpus in a temporary file, since ingest reads sources and
// this example is about the reading.
func write() string {
	dir, err := os.MkdirTemp("", "semantic-pipeline")
	if err != nil {
		log.Fatal(err)
	}
	path := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(path, []byte(notes), 0o600); err != nil {
		log.Fatal(err)
	}
	return path
}
