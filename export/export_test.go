package export

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/hanzoai/semantic"
)

// graph is the sample every format test writes. It carries a confidence and a
// source span on one assertion and neither on another, a term with a space in
// it, and terms that are already IRIs, which is the whole range of choices a
// writer has to make.
func graph() []semantic.Triple {
	return []semantic.Triple{
		{
			Subject: "Alice", Predicate: "knows", Object: "Bob", Score: 0.9,
			From: semantic.Chunk{DocID: "d1", Index: 2, Text: "Alice knows Bob.", Start: 40, End: 56},
		},
		{
			Subject: "Bob", Predicate: "works_at", Object: "Acme Inc.",
			From: semantic.Chunk{DocID: "d1", Index: 3, Text: "Bob works at Acme Inc.", Start: 57, End: 79},
		},
		{
			Subject: "Alice", Predicate: "http://schema.org/knows",
			Object: "https://example.org/carol",
		},
	}
}

// write runs one format over the sample graph.
func write(t *testing.T, format string) string {
	t.Helper()
	var b bytes.Buffer
	if err := Default.Write(context.Background(), &b, Triples(graph()), format); err != nil {
		t.Fatalf("write %s: %v", format, err)
	}
	return b.String()
}

// collect walks a source into a slice.
func collect(t *testing.T, src Source) []semantic.Triple {
	t.Helper()
	var out []semantic.Triple
	if err := src(context.Background(), func(tr semantic.Triple) error {
		out = append(out, tr)
		return nil
	}); err != nil {
		t.Fatalf("walk: %v", err)
	}
	return out
}

func TestFormats(t *testing.T) {
	want := []string{
		"csv", "cypher", "dot", "gexf", "graphml", "json", "jsonld",
		"mermaid", "ndjson", "nt", "parquet", "rdfxml", "tsv", "ttl", "yaml",
	}
	if got := Default.Formats(); !slices.Equal(got, want) {
		t.Errorf("Formats() = %v, want %v", got, want)
	}
}

func TestUnknownFormat(t *testing.T) {
	err := Default.Write(context.Background(), io.Discard, Triples(graph()), "avro")
	if !errors.Is(err, ErrFormat) {
		t.Errorf("Write to avro = %v, want ErrFormat", err)
	}
	err = Default.Read(strings.NewReader(""), "avro")(context.Background(), nil)
	if !errors.Is(err, ErrFormat) {
		t.Errorf("Read avro = %v, want ErrFormat", err)
	}
}

func TestWriteOnlyFormatCannotBeRead(t *testing.T) {
	for _, format := range []string{"ttl", "rdfxml", "jsonld", "graphml", "gexf", "dot", "mermaid", "cypher"} {
		err := Default.Read(strings.NewReader(""), format)(context.Background(), nil)
		if !errors.Is(err, ErrParse) {
			t.Errorf("Read %s = %v, want ErrParse", format, err)
		}
	}
}

func TestRegister(t *testing.T) {
	r := &Registry{}
	if _, ok := r.Get("nt"); ok {
		t.Error("the zero registry knows a format")
	}
	r.Set("NT", NT{})
	if _, ok := r.Get("nt"); !ok {
		t.Error("a format registered under NT is not found under nt")
	}
	r.Set("nt", Turtle{})
	if f, _ := r.Get("nt"); !reflect.DeepEqual(f, Turtle{}) {
		t.Errorf("registering over a name left %#v", f)
	}
	r.Drop("nt")
	if got := r.Formats(); len(got) != 0 {
		t.Errorf("after Drop, Formats() = %v", got)
	}
}

// A format that is named but cannot be written says which library is missing,
// rather than reporting a name nobody has heard of.
func TestAbsentFormatSaysWhatIsMissing(t *testing.T) {
	for _, format := range []string{"parquet", "yaml"} {
		err := Default.Write(context.Background(), io.Discard, Triples(graph()), format)
		if !errors.Is(err, ErrLibrary) {
			t.Fatalf("write %s = %v, want ErrLibrary", format, err)
		}
		if !strings.Contains(err.Error(), format) {
			t.Errorf("error %q does not name the format", err)
		}
	}
}

// Every format writes the whole sample without error, and none of them writes
// nothing.
func TestEveryFormatWritesTheGraph(t *testing.T) {
	for _, format := range Default.Formats() {
		if _, absent := mustGet(t, format).(Absent); absent {
			continue
		}
		out := write(t, format)
		for _, want := range []string{"Alice", "Bob"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s output does not mention %s:\n%s", format, want, out)
			}
		}
	}
}

// An assertion missing a term names nothing, so the formats that identify
// nodes refuse it. The formats that carry fields write what they were given.
func TestEmptyTerm(t *testing.T) {
	src := Triples([]semantic.Triple{{Subject: "Alice", Predicate: "knows"}})
	for _, format := range []string{"nt", "ttl", "rdfxml", "jsonld", "graphml", "gexf", "dot", "mermaid", "cypher"} {
		err := Default.Write(context.Background(), io.Discard, src, format)
		if !errors.Is(err, ErrTerm) {
			t.Errorf("%s with an empty object = %v, want ErrTerm", format, err)
		}
	}
	for _, format := range []string{"json", "ndjson", "csv", "tsv"} {
		if err := Default.Write(context.Background(), io.Discard, src, format); err != nil {
			t.Errorf("%s with an empty object = %v, want it written", format, err)
		}
	}
}

// A failing writer is reported by every format, whichever of them is holding
// the error at the time.
func TestWriteError(t *testing.T) {
	want := errors.New("disk full")
	for _, format := range Default.Formats() {
		if _, absent := mustGet(t, format).(Absent); absent {
			continue
		}
		err := Default.Write(context.Background(), broken{want}, Triples(graph()), format)
		if !errors.Is(err, want) {
			t.Errorf("%s: error = %v, want %v", format, err, want)
		}
	}
}

// A write that fails ends the walk. The buffer swallows the first few
// assertions, so the count is not one; what matters is that a graph of ten
// thousand does not go on being read into a writer that stopped taking it.
func TestWriteErrorStopsTheWalk(t *testing.T) {
	want := errors.New("disk full")
	const held = 10000
	for _, format := range Default.Formats() {
		if _, absent := mustGet(t, format).(Absent); absent {
			continue
		}
		seen := 0
		src := func(ctx context.Context, yield func(semantic.Triple) error) error {
			for i := range held {
				seen++
				tr := semantic.Triple{
					Subject:   fmt.Sprintf("subject %d %s", i, strings.Repeat("x", 60)),
					Predicate: "knows",
					Object:    fmt.Sprintf("object %d %s", i, strings.Repeat("y", 60)),
					From:      semantic.Chunk{DocID: "d", Index: i, Text: strings.Repeat("z", 200)},
				}
				if err := yield(tr); err != nil {
					return err
				}
			}
			return nil
		}
		if err := Default.Write(context.Background(), broken{want}, src, format); !errors.Is(err, want) {
			t.Errorf("%s: error = %v, want %v", format, err, want)
		}
		if seen > held/10 {
			t.Errorf("%s: walked %d of %d assertions into a writer that had failed", format, seen, held)
		}
	}
}

// mustGet resolves a format the registry is meant to hold.
func mustGet(t *testing.T, format string) Format {
	t.Helper()
	f, ok := Default.Get(format)
	if !ok {
		t.Fatalf("%s is not registered", format)
	}
	return f
}

// broken fails every write, as a full disk does.
type broken struct{ err error }

func (b broken) Write([]byte) (int, error) { return 0, b.err }

func TestCancel(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	stop()
	err := Default.Write(ctx, io.Discard, Triples(graph()), "ndjson")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("write with a cancelled context = %v, want context.Canceled", err)
	}
}

func TestTriplesSource(t *testing.T) {
	got := collect(t, Triples(graph()))
	if !reflect.DeepEqual(got, graph()) {
		t.Errorf("Triples yielded %v", got)
	}
	if got := collect(t, Triples(nil)); got != nil {
		t.Errorf("Triples(nil) yielded %v", got)
	}
	// The source replays, which is what the two-pass formats need.
	src := Triples(graph())
	if a, b := collect(t, src), collect(t, src); !reflect.DeepEqual(a, b) {
		t.Error("walking a slice source twice gave different graphs")
	}
}

// bag is a triple store holding what it was given, standing in for a database
// in the source tests.
type bag struct {
	held [][3]string
	err  error
}

func (s *bag) Assert(_ context.Context, a, b, c string) error {
	s.held = append(s.held, [3]string{a, b, c})
	return nil
}

func (s *bag) Match(_ context.Context, a, b, c string) ([][3]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	var out [][3]string
	for _, t := range s.held {
		if (a == "" || a == t[0]) && (b == "" || b == t[1]) && (c == "" || c == t[2]) {
			out = append(out, t)
		}
	}
	return out, nil
}

func TestMatchSource(t *testing.T) {
	st := &bag{held: [][3]string{
		{"Alice", "knows", "Bob"},
		{"Bob", "knows", "Carol"},
		{"Alice", "works_at", "Acme"},
	}}
	got := collect(t, Match(st, "", "knows", ""))
	want := []semantic.Triple{
		{Subject: "Alice", Predicate: "knows", Object: "Bob"},
		{Subject: "Bob", Predicate: "knows", Object: "Carol"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Match on the predicate gave %v, want %v", got, want)
	}
	if got := collect(t, Match(st, "", "", "")); len(got) != 3 {
		t.Errorf("the empty pattern gave %d assertions, want 3", len(got))
	}
	fail := errors.New("store down")
	err := Match(&bag{err: fail}, "", "", "")(context.Background(), nil)
	if !errors.Is(err, fail) {
		t.Errorf("a failing store gave %v, want %v", err, fail)
	}
}

// edges is a graph store, standing in for a database in the walk tests.
type edges struct {
	out map[string][]string
	err error
}

func (e edges) Node(context.Context, string, map[string]any) error { return nil }
func (e edges) Edge(context.Context, string, string, string) error { return nil }
func (e edges) Out(_ context.Context, id string) ([]string, error) {
	if e.err != nil {
		return nil, e.err
	}
	return e.out[id], nil
}

func TestWalkSource(t *testing.T) {
	// A cycle: Alice -> Bob -> Carol -> Alice, plus a leaf off Bob.
	g := edges{out: map[string][]string{
		"Alice": {"Bob"},
		"Bob":   {"Carol", "Dave"},
		"Carol": {"Alice"},
	}}
	got := collect(t, Walk(g, "linked", "Alice"))
	want := []semantic.Triple{
		{Subject: "Alice", Predicate: "linked", Object: "Bob"},
		{Subject: "Bob", Predicate: "linked", Object: "Carol"},
		{Subject: "Bob", Predicate: "linked", Object: "Dave"},
		{Subject: "Carol", Predicate: "linked", Object: "Alice"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Walk gave %v,\nwant %v", got, want)
	}
	if got := collect(t, Walk(g, "linked")); got != nil {
		t.Errorf("Walk with no roots gave %v", got)
	}
	fail := errors.New("graph down")
	err := Walk(edges{err: fail}, "linked", "Alice")(context.Background(), nil)
	if !errors.Is(err, fail) {
		t.Errorf("a failing graph gave %v, want %v", err, fail)
	}
}

// A source over a reader is spent once read. Saying so is what keeps gexf,
// which walks twice, from writing a graph with no edges in it.
func TestReadSourceIsSpent(t *testing.T) {
	src := Default.Read(strings.NewReader("[]"), "json")
	if got := collect(t, src); got != nil {
		t.Fatalf("the first walk gave %v", got)
	}
	if err := src(context.Background(), nil); !errors.Is(err, ErrDrained) {
		t.Errorf("the second walk gave %v, want ErrDrained", err)
	}
	var b bytes.Buffer
	err := Default.Write(context.Background(), &b, Default.Read(strings.NewReader(""), "ndjson"), "gexf")
	if !errors.Is(err, ErrDrained) {
		t.Errorf("gexf over a reader gave %v, want ErrDrained", err)
	}
}

// Reading one format and writing another is one call inside another, which is
// the point of a source that both sides speak.
func TestConvert(t *testing.T) {
	var lines, table bytes.Buffer
	if err := Default.Write(context.Background(), &lines, Triples(graph()), "ndjson"); err != nil {
		t.Fatal(err)
	}
	if err := Default.Write(context.Background(), &table, Default.Read(&lines, "ndjson"), "csv"); err != nil {
		t.Fatal(err)
	}
	got := collect(t, Default.Read(&table, "csv"))
	if !reflect.DeepEqual(got, graph()) {
		t.Errorf("ndjson through csv gave %v,\nwant %v", got, graph())
	}
}
