package semantic_test

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

// notes is the corpus every test here reads. Two of its characters are the
// point: the space in "Ada Lovelace" is U+00A0, and the é in "Montréal" is a
// bare e followed by a combining acute. Both look right and neither matches
// the gazetteer, so the run needs normalize to find anything but the middle
// sentence. The negative control at the bottom holds that down.
const notes = "# Field notes\n" +
	"\n" +
	"Ada\u00a0Lovelace works for Babbage Engines.\n" + // non-breaking space +
	"\n" +
	"Babbage Engines was founded by Charles Babbage.\n" +
	"\n" +
	"Babbage Engines is headquartered in Montre\u0301al.\n" // e + combining acute

// gazetteer is what the reader is assumed to know before reading. The names
// are written composed and with ordinary spaces, the way a person would type
// them into a configuration file.
var gazetteer = map[string]string{
	"Ada Lovelace":    "PERSON",
	"Charles Babbage": "PERSON",
	"Babbage Engines": "ORG",
	"Montr\u00e9al":   "LOC",
}

// clean is the normalize stage as a pipeline stage: parse the document, then
// settle its text. normalize is a set of string transforms rather than a
// Parser, so composing it is the caller's line of code, and this is that line.
type clean struct{ parse semantic.Parser }

func (c clean) Parse(ctx context.Context, d semantic.Doc) (semantic.Doc, error) {
	d, err := c.parse.Parse(ctx, d)
	if err != nil {
		return d, err
	}
	d.Text = normalize.Text(d.Text)
	return d, nil
}

func write(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(p, []byte(notes), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// want is what the corpus says, in the order the sentences say it.
var want = []semantic.Triple{
	{Subject: "Ada Lovelace", Predicate: "works_for", Object: "Babbage Engines", Score: 0.7},
	{Subject: "Babbage Engines", Predicate: "founded_by", Object: "Charles Babbage", Score: 0.7},
	{Subject: "Babbage Engines", Predicate: "located_in", Object: "Montr\u00e9al", Score: 0.7},
}

// TestPipeline runs one file through every stage by hand and checks what each
// one handed the next. Composing them is the module's whole claim, so the
// checks are on the values that cross the seams, not on the absence of errors.
func TestPipeline(t *testing.T) {
	ctx := context.Background()
	path := write(t)

	// Ingest. The reference is a path; what comes back is a document that
	// knows its own provenance.
	docs, err := ingest.File{}.Ingest(ctx, path)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("ingest returned %d documents, want 1", len(docs))
	}
	d := docs[0]
	if d.ID != path || d.Source != path {
		t.Errorf("id %q source %q, want both %q", d.ID, d.Source, path)
	}
	if o := ingest.OriginOf(d); o.Type != "md" || o.Size != int64(len(notes)) || len(o.Hash) != 64 {
		t.Errorf("origin = %+v, want type md, size %d, a sha-256", o, len(notes))
	}

	// Parse. ingest files the source's kind under Meta["type"] as an
	// extension ("md"); parse files the format it read under Meta["format"]
	// as a format name ("markdown"). Two vocabularies, and no handoff needed
	// between them: parse recovers the format from Doc.Source, which is where
	// ingest's extension came from in the first place.
	d, err = parse.Any{}.Parse(ctx, d)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := d.Meta["format"]; got != "markdown" {
		t.Errorf("format = %v, want markdown", got)
	}
	if got := d.Meta["type"]; got != "md" {
		t.Errorf("type = %v, want md: parse must not overwrite the origin", got)
	}
	if got := d.Meta["title"]; got != "Field notes" {
		t.Errorf("title = %v, want Field notes", got)
	}

	// Normalize. The non-breaking space becomes a space and the combining
	// acute is composed, so the names in the text are the names in the
	// gazetteer.
	d.Text = normalize.Text(d.Text)
	if strings.ContainsRune(d.Text, '\u00a0') {
		t.Error("a non-breaking space survived normalize")
	}
	for _, name := range []string{"Ada Lovelace", "Montr\u00e9al"} {
		if !strings.Contains(d.Text, name) {
			t.Errorf("normalized text does not contain %q", name)
		}
	}

	// Split. One sentence per chunk, each carrying the document it came from
	// and its place in the sequence. The chunks are a partition: joined back
	// up they are the document.
	chunks, err := split.Sentences{Max: 1}.Split(ctx, d)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if len(chunks) != 3 {
		t.Fatalf("%d chunks, want 3: %q", len(chunks), chunks)
	}
	var joined strings.Builder
	for i, c := range chunks {
		if c.DocID != d.ID {
			t.Errorf("chunk %d came from %q, want %q", i, c.DocID, d.ID)
		}
		if c.Index != i {
			t.Errorf("chunk %d is indexed %d", i, c.Index)
		}
		joined.WriteString(c.Text)
	}
	if joined.String() != d.Text {
		t.Errorf("chunks joined = %q, want the document back", joined.String())
	}

	// Extract. Deterministic rules over a gazetteer: no model, so the answer
	// is a function of the text and can be written down.
	rules := extract.Rules{Names: gazetteer}
	var got []semantic.Triple
	for _, c := range chunks {
		ts, err := rules.Extract(ctx, c)
		if err != nil {
			t.Fatalf("extract chunk %d: %v", c.Index, err)
		}
		got = append(got, ts...)
	}
	if len(got) != 3 {
		t.Fatalf("%d triples, want 3: %v", len(got), got)
	}
	for i, tr := range got {
		w := want[i]
		if tr.Subject != w.Subject || tr.Predicate != w.Predicate || tr.Object != w.Object {
			t.Errorf("triple %d = %q %q %q, want %q %q %q",
				i, tr.Subject, tr.Predicate, tr.Object, w.Subject, w.Predicate, w.Object)
		}
		if tr.Score != w.Score {
			t.Errorf("triple %d scored %v, want %v", i, tr.Score, w.Score)
		}
		// Every assertion says which sentence it was read from, which is
		// what makes it answerable later.
		if tr.From.Index != i || tr.From.DocID != d.ID {
			t.Errorf("triple %d came from chunk %d of %q, want chunk %d of %q",
				i, tr.From.Index, tr.From.DocID, i, d.ID)
		}
	}

	// Store. kg.Mem is the graph kept in memory and served through the store
	// interfaces, so the rest of this runs against store.Triple and would run
	// the same against a database.
	var mem kg.Mem
	var st store.Triple = &mem
	for _, tr := range got {
		if err := st.Assert(ctx, tr.Subject, tr.Predicate, tr.Object); err != nil {
			t.Fatalf("assert %v: %v", tr, err)
		}
	}
	if n, e := mem.Graph().Size(); n != 4 || e != 3 {
		t.Errorf("graph = %d nodes, %d edges; want 4, 3", n, e)
	}
	// Names fold to node ids, so the graph answers to the spelling it was
	// given and to the one it keeps.
	for _, id := range []string{"Babbage Engines", "babbage engines"} {
		if _, ok := mem.Graph().Node(id); !ok {
			t.Errorf("graph has no node %q", id)
		}
	}
	// Saying the same thing twice does not make a second edge.
	if err := st.Assert(ctx, "Ada Lovelace", "works_for", "Babbage Engines"); err != nil {
		t.Fatal(err)
	}
	if n, e := mem.Graph().Size(); n != 4 || e != 3 {
		t.Errorf("after repeating an assertion: %d nodes, %d edges; want 4, 3", n, e)
	}

	// Export. N-Triples, read out of the store and minted into the module's
	// namespace. A name is a name in any store, so the document is exactly
	// this and a diff against it is a real regression.
	var out strings.Builder
	if err := export.Default.Write(ctx, &out, export.Match(st, "", "", ""), "nt"); err != nil {
		t.Fatalf("export: %v", err)
	}
	const ns = export.Base
	wantNT := "<" + ns + "ada%20lovelace> <" + ns + "works_for> <" + ns + "babbage%20engines> .\n" +
		"<" + ns + "babbage%20engines> <" + ns + "founded_by> <" + ns + "charles%20babbage> .\n" +
		"<" + ns + "babbage%20engines> <" + ns + "located_in> <" + ns + "montr%C3%A9al> .\n"
	if out.String() != wantNT {
		t.Errorf("n-triples =\n%s\nwant\n%s", out.String(), wantNT)
	}

	// And it reads back: what the module writes, the module parses.
	var back []semantic.Triple
	err = export.Default.Read(strings.NewReader(out.String()), "nt")(ctx, func(tr semantic.Triple) error {
		back = append(back, tr)
		return nil
	})
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(back) != 3 {
		t.Fatalf("read back %d triples, want 3", len(back))
	}
	for i, tr := range back {
		w := want[i]
		got := name(t, tr.Subject, ns) + " " + name(t, tr.Predicate, ns) + " " + name(t, tr.Object, ns)
		// The store folded the names on the way in, so what comes back is
		// the node id, not the surface form the extractor read.
		if wanted := kg.Fold(w.Subject) + " " + w.Predicate + " " + kg.Fold(w.Object); got != wanted {
			t.Errorf("read back %d = %q, want %q", i, got, wanted)
		}
	}
}

// name is the term a writer minted, read back: strip the namespace and undo
// the percent-escaping the RDF formats apply to anything not already an IRI.
func name(t *testing.T, iri, ns string) string {
	t.Helper()
	s, err := url.PathUnescape(strings.TrimPrefix(iri, ns))
	if err != nil {
		t.Fatalf("unescape %q: %v", iri, err)
	}
	return s
}

// TestPipelineRunsAsOnePipeline checks that the same stages, handed to the
// root Pipeline, produce the same assertions. The hand-run above is the
// specification; this is the composition that saves a caller from writing it.
func TestPipelineRunsAsOnePipeline(t *testing.T) {
	p := semantic.Pipeline{
		Ingest:  ingest.File{},
		Parse:   clean{parse.Any{}},
		Split:   split.Sentences{Max: 1},
		Extract: extract.Rules{Names: gazetteer},
	}
	got, err := p.Run(context.Background(), write(t))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("%d triples, want %d: %v", len(got), len(want), got)
	}
	for i, tr := range got {
		w := want[i]
		if tr.Subject != w.Subject || tr.Predicate != w.Predicate || tr.Object != w.Object || tr.Score != w.Score {
			t.Errorf("triple %d = %v, want %v", i, tr, w)
		}
	}
}

// TestPipelineNeedsNormalize is the negative control on the stage that is
// easiest to leave out, because leaving it out does not fail: it quietly finds
// less. Only the sentence with no typography in it survives.
func TestPipelineNeedsNormalize(t *testing.T) {
	p := semantic.Pipeline{
		Ingest:  ingest.File{},
		Parse:   parse.Any{},
		Split:   split.Sentences{Max: 1},
		Extract: extract.Rules{Names: gazetteer},
	}
	got, err := p.Run(context.Background(), write(t))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("%d triples without normalize, want 1: %v", len(got), got)
	}
	if got[0].Subject != "Babbage Engines" || got[0].Predicate != "founded_by" {
		t.Errorf("survivor = %v, want the founded_by assertion", got[0])
	}
}

// The stages of this module are the interfaces of the root package, so a
// caller may swap any one of them for its own.
var (
	_ semantic.Ingester  = ingest.File{}
	_ semantic.Parser    = parse.Any{}
	_ semantic.Parser    = clean{}
	_ semantic.Splitter  = split.Sentences{}
	_ semantic.Extractor = extract.Rules{}
	_ store.Triple       = (*kg.Mem)(nil)
	_ store.Graph        = (*kg.Mem)(nil)
)
