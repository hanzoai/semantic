package store

import (
	"fmt"
	"sort"
	"sync"
)

// Drivers names the implementations of one kind of store, so a program can
// be told at run time which one to use. A driver in another package adds
// itself in an init, and a caller reaches it by importing that package for
// its effect:
//
//	import _ "github.com/hanzoai/semantic/store/qdrant"
//
//	v, err := store.Vectors.Open("qdrant", "http://localhost:6333/docs")
//
// The zero value is an empty set of drivers, ready to use.
type Drivers[T any] struct {
	mu sync.RWMutex
	by map[string]func(string) (T, error)
}

// Vectors, Graphs and Triples hold the drivers of each kind. A store that
// answers more than one question registers under each of them.
var (
	Vectors = &Drivers[Vector]{}
	Graphs  = &Drivers[Graph]{}
	Triples = &Drivers[Triple]{}
)

// Add registers a driver under a name, replacing whatever was there. A nil
// open registers the name alone: Open then reports ErrDriver, so a caller
// can tell a store this build cannot serve from one nobody has heard of.
func (d *Drivers[T]) Add(name string, open func(dsn string) (T, error)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.by == nil {
		d.by = map[string]func(string) (T, error){}
	}
	d.by[name] = open
}

// Open builds the store a driver names from a data source name, whose shape
// the driver decides: a path, a URL, or nothing at all.
func (d *Drivers[T]) Open(name, dsn string) (T, error) {
	d.mu.RLock()
	open, ok := d.by[name]
	d.mu.RUnlock()
	var zero T
	if !ok {
		return zero, fmt.Errorf("%w: no driver %q", ErrDriver, name)
	}
	if open == nil {
		return zero, fmt.Errorf("%w: %q is not built in", ErrDriver, name)
	}
	return open(dsn)
}

// Has reports whether a name is registered, whether or not it can be opened.
func (d *Drivers[T]) Has(name string) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	_, ok := d.by[name]
	return ok
}

// Names lists every registered driver, in order.
func (d *Drivers[T]) Names() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]string, 0, len(d.by))
	for n := range d.by {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// The stores this package implements, and the names of the ones it does
// not. The names are here so the set of stores semantica knows about is one
// list a program can read, rather than something a caller learns by an
// import failing. Each unnamed one is a driver in its own package, waiting
// to be written against the same three interfaces.
func init() {
	mem := func(string) (*Mem, error) { return &Mem{}, nil }
	Vectors.Add("mem", func(dsn string) (Vector, error) { return mem(dsn) })
	Graphs.Add("mem", func(dsn string) (Graph, error) { return mem(dsn) })
	Triples.Add("mem", func(dsn string) (Triple, error) { return mem(dsn) })

	Vectors.Add("file", func(dsn string) (Vector, error) { return Open(dsn) })
	Graphs.Add("file", func(dsn string) (Graph, error) { return Open(dsn) })
	Triples.Add("file", func(dsn string) (Triple, error) { return Open(dsn) })

	for _, n := range []string{"faiss", "milvus", "pgvector", "pinecone", "sqlite", "weaviate"} {
		Vectors.Add(n, nil)
	}
	for _, n := range []string{"age", "falkordb", "neo4j", "neptune"} {
		Graphs.Add(n, nil)
	}
	for _, n := range []string{"anzo", "oxigraph"} {
		Triples.Add(n, nil)
	}
}
