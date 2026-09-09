// Command export writes one graph in every format this module carries, and
// reads back the ones that can be read back.
//
// Two questions separate the formats. What can one carry? json, ndjson, csv
// and tsv carry every field of an assertion — the span it was read from and
// the confidence it was given included; the RDF and graph formats carry the
// assertion alone, because a triple has nowhere to put the rest. What can one
// be read back from? Those four, and n-triples.
//
// Everything streams. A source hands assertions to a format one at a time and
// the format writes each as it arrives, so the graph never has to fit in
// memory.
//
//	go run ./examples/export
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/hanzoai/semantic"
	"github.com/hanzoai/semantic/export"
	"github.com/hanzoai/semantic/kg"
)

var facts = []semantic.Triple{
	{Subject: "Ada Lovelace", Predicate: "works_for", Object: "Babbage Engines", Score: 0.9,
		From: semantic.Chunk{DocID: "notes.md", Index: 0, Text: "Ada Lovelace works for Babbage Engines."}},
	{Subject: "Babbage Engines", Predicate: "founded_by", Object: "Charles Babbage", Score: 0.8,
		From: semantic.Chunk{DocID: "notes.md", Index: 1, Text: "Babbage Engines was founded by Charles Babbage."}},
	{Subject: "Babbage Engines", Predicate: "located_in", Object: "Montréal", Score: 0.7,
		From: semantic.Chunk{DocID: "notes.md", Index: 2, Text: "It is headquartered in Montréal."}},
}

func main() {
	ctx := context.Background()

	formats(ctx)
	roundTrip(ctx)
	sources(ctx)
	gaps(ctx)
}

// formats is the thirteen this module writes, three lines each. A term that already carries a
// scheme is an IRI and is written as it stands; anything else is a name, and
// the RDF formats mint it into the module's namespace.
func formats(ctx context.Context) {
	fmt.Printf("formats  %v\n", export.Default.Formats())
	for _, name := range export.Default.Formats() {
		f, _ := export.Default.Get(name)
		if _, absent := f.(export.Absent); absent {
			continue
		}
		var out strings.Builder
		if err := export.Default.Write(ctx, &out, export.Triples(facts), name); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("\n%s  %d bytes\n", name, out.Len())
		for _, line := range head(out.String(), 3) {
			fmt.Printf("  %s\n", clip(line, 96))
		}
	}
}

// roundTrip is the other half. What a format writes, the module parses, so
// converting one document to another is one call inside another.
func roundTrip(ctx context.Context) {
	fmt.Println("\nread back")
	for _, name := range []string{"json", "ndjson", "csv", "tsv", "nt"} {
		var out strings.Builder
		if err := export.Default.Write(ctx, &out, export.Triples(facts), name); err != nil {
			log.Fatal(err)
		}
		var back []semantic.Triple
		src := export.Default.Read(strings.NewReader(out.String()), name)
		if err := src(ctx, func(t semantic.Triple) error {
			back = append(back, t)
			return nil
		}); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("  %-7s %d triples back; confidence and span survive: %v\n",
			name, len(back), back[0].Score == facts[0].Score && back[0].From.DocID == facts[0].From.DocID)
	}
	fmt.Println("  nt carries the assertion alone, so its confidence and span come back empty:")
	fmt.Println("  a triple has nowhere to put them, and a graph that has to keep them goes out as ndjson")

	// Converting one format to another is one call inside another.
	var nt strings.Builder
	must(export.Default.Write(ctx, &nt, export.Triples(facts), "nt"))
	var ttl strings.Builder
	must(export.Default.Write(ctx, &ttl, export.Default.Read(strings.NewReader(nt.String()), "nt"), "ttl"))
	fmt.Printf("\n  nt → ttl in one call:\n")
	for _, line := range head(ttl.String(), 4) {
		fmt.Printf("    %s\n", line)
	}

	// A source over a reader is spent by the first walk. GEXF needs two — its
	// grammar puts every node before every edge — so it fails loudly rather
	// than writing a graph with no edges.
	spent := export.Default.Read(strings.NewReader(nt.String()), "nt")
	must(spent(ctx, func(semantic.Triple) error { return nil }))
	if err := spent(ctx, func(semantic.Triple) error { return nil }); errors.Is(err, export.ErrDrained) {
		fmt.Printf("  walking a reader-backed source twice: %v\n", err)
	}
}

// sources are the three ways a graph is handed to a writer: a slice already in
// memory, a triple store queried by pattern, and a graph store walked from a
// set of roots.
func sources(ctx context.Context) {
	g := kg.Build(facts)
	mem := kg.Keep(g)

	fmt.Println("\nsources")
	fmt.Printf("  %-38s %d\n", `Triples(slice)`, count(ctx, export.Triples(facts)))
	fmt.Printf("  %-38s %d\n", `Match(store, "", "", "")`, count(ctx, export.Match(mem, "", "", "")))
	fmt.Printf("  %-38s %d\n", `Match(store, "", "located_in", "")`, count(ctx, export.Match(mem, "", "located_in", "")))

	// A graph store reports a node's neighbours without saying which relation
	// leads to them, so a walk cannot recover the predicate and records the
	// label it was given in its place. Where the predicates matter, export
	// from the triple store.
	var out strings.Builder
	must(export.Default.Write(ctx, &out, export.Walk(mem, "linked", "ada lovelace"), "nt"))
	fmt.Printf("  %-38s %d, every predicate the label given:\n",
		`Walk(store, "linked", "ada lovelace")`, strings.Count(out.String(), "\n"))
	for _, line := range head(out.String(), 2) {
		fmt.Printf("    %s\n", line)
	}
}

// gaps are the two things this package will not do quietly. A format it knows
// of and cannot write says so; a predicate that cannot be an XML name stops
// the one format that needs one.
func gaps(ctx context.Context) {
	fmt.Println("\nwhat it refuses")
	for _, name := range []string{"parquet", "yaml"} {
		err := export.Default.Write(ctx, io.Discard, export.Triples(facts), name)
		if errors.Is(err, export.ErrLibrary) {
			fmt.Printf("  %-8s %v\n", name, err)
		}
	}
	if err := export.Default.Write(ctx, io.Discard, export.Triples(facts), "avro"); errors.Is(err, export.ErrFormat) {
		fmt.Printf("  %-8s %v\n", "avro", err)
	}

	// Registering a writer under the name fills the gap, and it is reached
	// the same way as the rest.
	export.Default.Set("parquet", lines{})
	var out strings.Builder
	must(export.Default.Write(ctx, &out, export.Triples(facts), "parquet"))
	fmt.Printf("  after Set(\"parquet\", …): %q\n", strings.TrimSpace(out.String()))

	// RDF/XML is the one format here that cannot write every predicate: a
	// property element needs an XML name.
	odd := []semantic.Triple{{Subject: "a", Predicate: "https://example.org/1", Object: "b"}}
	if err := export.Default.Write(ctx, io.Discard, export.Triples(odd), "rdfxml"); errors.Is(err, export.ErrName) {
		fmt.Printf("  rdfxml   %v\n", err)
	}

	// And an assertion with a hole in it stops a format that has to name the
	// node, rather than writing a document with a hole in it.
	hole := []semantic.Triple{{Subject: "a", Predicate: "", Object: "b"}}
	if err := export.Default.Write(ctx, io.Discard, export.Triples(hole), "graphml"); errors.Is(err, export.ErrTerm) {
		fmt.Printf("  graphml  %v\n", err)
	}
}

// lines is a format of the caller's own: one line per assertion. A real
// parquet writer is the same shape.
type lines struct{}

func (lines) Write(ctx context.Context, w io.Writer, src export.Source) error {
	return src(ctx, func(t semantic.Triple) error {
		_, err := fmt.Fprintf(w, "%s|%s|%s\n", t.Subject, t.Predicate, t.Object)
		return err
	})
}

func count(ctx context.Context, src export.Source) int {
	n := 0
	must(src(ctx, func(semantic.Triple) error { n++; return nil }))
	return n
}

func head(s string, n int) []string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		return append(lines[:n:n], "…")
	}
	return lines
}

func clip(s string, n int) string {
	if len([]rune(s)) > n {
		return string([]rune(s)[:n-1]) + "…"
	}
	return s
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
