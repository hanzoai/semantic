package store

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestDriversOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb")
	for _, c := range []struct {
		name string
		open func() (any, error)
	}{
		{"vector mem", func() (any, error) { return Vectors.Open("mem", "") }},
		{"graph mem", func() (any, error) { return Graphs.Open("mem", "") }},
		{"triple mem", func() (any, error) { return Triples.Open("mem", "") }},
		{"vector file", func() (any, error) { return Vectors.Open("file", path) }},
		{"graph file", func() (any, error) { return Graphs.Open("file", path+"g") }},
		{"triple file", func() (any, error) { return Triples.Open("file", path+"t") }},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, err := c.open()
			if err != nil {
				t.Fatal(err)
			}
			if f, ok := s.(*File); ok {
				defer f.Close()
			}
			if s == nil {
				t.Fatal("opened nothing")
			}
		})
	}
}

func TestDriversOpenedStoreWorks(t *testing.T) {
	v, err := Vectors.Open("mem", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if err := v.Put(ctx, "a", []float32{1, 0}, nil); err != nil {
		t.Fatal(err)
	}
	got, err := v.Near(ctx, []float32{1, 0}, 1)
	if err != nil {
		t.Fatal(err)
	}
	equal(t, ids(got), []string{"a"})
}

func TestDriversNamesTheOnesItCannotServe(t *testing.T) {
	for _, c := range []struct {
		kind string
		name string
		has  func(string) bool
		open func(string) error
	}{
		{"vector", "faiss", Vectors.Has, func(n string) error { _, err := Vectors.Open(n, ""); return err }},
		{"vector", "weaviate", Vectors.Has, func(n string) error { _, err := Vectors.Open(n, ""); return err }},
		{"vector", "milvus", Vectors.Has, func(n string) error { _, err := Vectors.Open(n, ""); return err }},
		{"vector", "pgvector", Vectors.Has, func(n string) error { _, err := Vectors.Open(n, ""); return err }},
		{"vector", "pinecone", Vectors.Has, func(n string) error { _, err := Vectors.Open(n, ""); return err }},
		{"vector", "sqlite", Vectors.Has, func(n string) error { _, err := Vectors.Open(n, ""); return err }},
		{"graph", "neo4j", Graphs.Has, func(n string) error { _, err := Graphs.Open(n, ""); return err }},
		{"graph", "falkordb", Graphs.Has, func(n string) error { _, err := Graphs.Open(n, ""); return err }},
		{"graph", "neptune", Graphs.Has, func(n string) error { _, err := Graphs.Open(n, ""); return err }},
		{"graph", "age", Graphs.Has, func(n string) error { _, err := Graphs.Open(n, ""); return err }},
		{"triple", "oxigraph", Triples.Has, func(n string) error { _, err := Triples.Open(n, ""); return err }},
		{"triple", "anzo", Triples.Has, func(n string) error { _, err := Triples.Open(n, ""); return err }},
	} {
		t.Run(c.kind+" "+c.name, func(t *testing.T) {
			if !c.has(c.name) {
				t.Fatalf("%q is not even a name", c.name)
			}
			err := c.open(c.name)
			if !errors.Is(err, ErrDriver) {
				t.Fatalf("opening %q gave %v, want ErrDriver", c.name, err)
			}
			if !strings.Contains(err.Error(), c.name) {
				t.Errorf("%q does not say which driver it means", err)
			}
		})
	}
}

func TestDriversRefusesANameNobodyRegistered(t *testing.T) {
	if Vectors.Has("nonsense") {
		t.Fatal("a name nobody registered is registered")
	}
	_, err := Vectors.Open("nonsense", "")
	if !errors.Is(err, ErrDriver) {
		t.Errorf("gave %v, want ErrDriver", err)
	}
}

func TestDriversNames(t *testing.T) {
	for _, c := range []struct {
		kind string
		got  []string
		want []string
	}{
		{"vector", Vectors.Names(), []string{"faiss", "file", "mem", "milvus", "pgvector", "pinecone", "sqlite", "weaviate"}},
		{"graph", Graphs.Names(), []string{"age", "falkordb", "file", "mem", "neo4j", "neptune"}},
		{"triple", Triples.Names(), []string{"anzo", "file", "mem", "oxigraph"}},
	} {
		t.Run(c.kind, func(t *testing.T) {
			for _, want := range c.want {
				if !slices.Contains(c.got, want) {
					t.Errorf("%v does not name %q", c.got, want)
				}
			}
			if !slices.IsSorted(c.got) {
				t.Errorf("%v is not in order", c.got)
			}
		})
	}
}

func TestDriversAddReplaces(t *testing.T) {
	// A fresh set, so the test does not change what the package registered.
	var d Drivers[Vector]
	if d.Has("mine") {
		t.Fatal("the zero value knows a driver")
	}
	if got := d.Names(); len(got) != 0 {
		t.Fatalf("the zero value names %v", got)
	}
	d.Add("mine", func(string) (Vector, error) { return &Mem{}, nil })
	if _, err := d.Open("mine", ""); err != nil {
		t.Fatal(err)
	}
	d.Add("mine", nil)
	if _, err := d.Open("mine", ""); !errors.Is(err, ErrDriver) {
		t.Errorf("after registering the name alone, Open gave %v, want ErrDriver", err)
	}
	if !d.Has("mine") {
		t.Error("the name went away with its implementation")
	}
}

func TestDriversPassesTheSourceName(t *testing.T) {
	var d Drivers[Vector]
	var got string
	d.Add("mine", func(dsn string) (Vector, error) {
		got = dsn
		return &Mem{}, nil
	})
	if _, err := d.Open("mine", "over there"); err != nil {
		t.Fatal(err)
	}
	if got != "over there" {
		t.Errorf("the driver was told %q", got)
	}
}
