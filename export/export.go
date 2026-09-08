// Package export writes a graph out in the formats other tools read.
//
// Everything here streams. A [Source] hands assertions to a [Format] one at a
// time and the format writes each one as it arrives, so the graph never has to
// fit in memory. The formats that name nodes — graphml, gexf, dot, mermaid,
// cypher — hold the set of nodes they have already declared, which is the
// graph's nodes and not its edges; the rest hold nothing at all.
//
// Two questions separate the formats. What can one carry? json, ndjson, csv
// and tsv carry every field of an assertion, the span it was read from and the
// confidence it was given included; the RDF and graph formats carry the
// assertion alone, because a triple has nowhere to put the rest. What can one
// be read back from? The four that carry everything, and nt. [Registry.Read]
// parses them into the same Source a writer takes, so converting one to
// another is one call inside another:
//
//	err := export.Default.Write(ctx, w, export.Default.Read(f, "nt"), "ttl")
//
// A term that already carries a scheme is an IRI and is written as it stands.
// Anything else is a name, and the RDF formats mint it into [Base].
//
// A format is named by its file extension, so the name of a document is the
// name of the format that writes it:
//
//	json ndjson jsonld csv tsv nt ttl rdfxml graphml gexf dot mermaid cypher
//
// parquet and yaml resolve too, and report [ErrLibrary]: both need an encoder
// outside the standard library, and this module takes no dependencies. See
// [Absent].
package export

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
)

// What this package returns when it cannot write what it was given.
var (
	// ErrFormat says nothing is registered under that name.
	ErrFormat = errors.New("unknown format")
	// ErrTerm says an assertion is missing a subject, predicate or object.
	// A format that identifies nodes has nothing to call the node, so it
	// stops rather than write a document with a hole in it.
	ErrTerm = errors.New("assertion has an empty term")
	// ErrName says a predicate cannot be written as an XML qualified name,
	// which is the one thing rdfxml needs that the other formats do not.
	ErrName = errors.New("predicate has no XML name")
	// ErrLibrary says the format needs code this module does not carry.
	// Register a writer for it to fill the gap.
	ErrLibrary = errors.New("format needs a library this module does not carry")
	// ErrParse says the format is written here but not read back.
	ErrParse = errors.New("format cannot be parsed")
	// ErrDrained says a source over a reader has already been walked and its
	// bytes are gone.
	ErrDrained = errors.New("source is already read")
)

// Base is the namespace a term without a scheme is minted into. It is
// Semantica's own vocabulary, so a graph written here and one written by the
// Python this is ported from name the same things.
const Base = "https://semantica.dev/ns#"

// Format writes a stream of assertions. A format is a value with no state of
// its own, so one may be shared and used concurrently.
type Format interface {
	Write(ctx context.Context, w io.Writer, src Source) error
}

// Reader is the other half of a format that can parse its own output. json,
// ndjson, csv, tsv and nt implement it; the rest are written and not read.
type Reader interface {
	Read(r io.Reader) Source
}

// Registry resolves a name to the format written under it, and is the seam
// for formats this package does not implement: register a writer and it is
// reached the same way as the rest.
//
//	export.Default.Set("parquet", myParquetWriter)
//	err := export.Default.Write(ctx, f, src, "parquet")
//
// The zero Registry is empty and usable; Default holds the formats here.
type Registry struct {
	mu sync.RWMutex
	by map[string]Format
}

// Default resolves the formats this package implements.
var Default = &Registry{by: map[string]Format{
	"json":    JSON{},
	"ndjson":  NDJSON{},
	"jsonld":  JSONLD{},
	"csv":     CSV{},
	"tsv":     CSV{Sep: '\t'},
	"nt":      NT{},
	"ttl":     Turtle{},
	"rdfxml":  RDFXML{},
	"graphml": GraphML{},
	"gexf":    GEXF{},
	"dot":     DOT{},
	"mermaid": Mermaid{},
	"cypher":  Cypher{},
	"parquet": Absent{"parquet"},
	"yaml":    Absent{"yaml"},
}}

// Set registers f under name, replacing whatever was there.
func (r *Registry) Set(name string, f Format) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.by == nil {
		r.by = map[string]Format{}
	}
	r.by[strings.ToLower(name)] = f
}

// Get returns the format registered under name.
func (r *Registry) Get(name string) (Format, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.by[strings.ToLower(name)]
	return f, ok
}

// Drop removes the format registered under name.
func (r *Registry) Drop(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.by, strings.ToLower(name))
}

// Formats lists what is registered, in order.
func (r *Registry) Formats() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.by))
	for name := range r.by {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// Write serializes src to w in the named format.
func (r *Registry) Write(ctx context.Context, w io.Writer, src Source, format string) error {
	f, ok := r.Get(format)
	if !ok {
		return fmt.Errorf("%w %q", ErrFormat, format)
	}
	return f.Write(ctx, w, src)
}

// Read is the source that parses rd as the named format. A format that does
// not carry every field of an assertion cannot be read back, and the walk
// reports ErrParse.
//
// The reader is spent by the first walk; a second returns [ErrDrained] rather
// than an empty graph, so a format that needs two walks fails loudly instead
// of writing a graph with no edges.
func (r *Registry) Read(rd io.Reader, format string) Source {
	f, ok := r.Get(format)
	if !ok {
		return fail(fmt.Errorf("%w %q", ErrFormat, format))
	}
	p, ok := f.(Reader)
	if !ok {
		return fail(fmt.Errorf("%w: %s", ErrParse, format))
	}
	return p.Read(rd)
}

// pen writes a document and keeps the first error, so a serializer writes
// plainly and checks once. It buffers, because a format that writes a graph a
// field at a time would otherwise make a syscall of each one.
type pen struct {
	to  *bufio.Writer
	err error
}

func ink(w io.Writer) *pen { return &pen{to: bufio.NewWriter(w)} }

// put writes its arguments in order, doing nothing once an error has landed.
func (p *pen) put(v ...string) {
	for _, s := range v {
		if p.err != nil {
			return
		}
		_, p.err = p.to.WriteString(s)
	}
}

// done flushes and reports the first error the document met.
func (p *pen) done() error {
	if p.err != nil {
		return p.err
	}
	return p.to.Flush()
}

// seen holds the nodes a format has already declared, mapping each term to
// the identifier that format calls it by. It is what makes the node formats
// cost the graph's nodes and not its edges.
type seen map[string]string

// id returns the identifier for term, minting one with mint if the term is
// new, and reports whether it was new. A nil mint names a node by its term.
func (s seen) id(term string, mint func(n int) string) (string, bool) {
	if v, ok := s[term]; ok {
		return v, false
	}
	v := term
	if mint != nil {
		v = mint(len(s))
	}
	s[term] = v
	return v, true
}
