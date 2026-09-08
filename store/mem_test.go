package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// A small corpus with a known geometry: east and north are orthogonal, and
// tilt is a degree off east, so nearness has one right answer.
var corpus = []Item{
	{ID: "east", Vec: []float32{1, 0}, Meta: map[string]any{"lang": "en", "year": 2020}},
	{ID: "tilt", Vec: []float32{0.9, 0.1}, Meta: map[string]any{"lang": "en", "year": 2021}},
	{ID: "north", Vec: []float32{0, 1}, Meta: map[string]any{"lang": "fr", "year": 2022}},
	{ID: "west", Vec: []float32{-1, 0}, Meta: map[string]any{"lang": "fr", "year": 2023}},
}

func filled(t *testing.T) *Mem {
	t.Helper()
	m := &Mem{}
	for _, it := range corpus {
		if err := m.Put(t.Context(), it.ID, it.Vec, it.Meta); err != nil {
			t.Fatalf("Put %s: %v", it.ID, err)
		}
	}
	return m
}

func TestMemNear(t *testing.T) {
	m := filled(t)
	for _, c := range []struct {
		name string
		q    []float32
		k    int
		want []string
	}{
		{"east", []float32{1, 0}, 2, []string{"east", "tilt"}},
		{"north", []float32{0, 1}, 2, []string{"north", "tilt"}},
		{"west", []float32{-1, 0}, 1, []string{"west"}},
		{"k past the end", []float32{1, 0}, 99, []string{"east", "tilt", "north", "west"}},
		{"k of zero asks for all", []float32{1, 0}, 0, []string{"east", "tilt", "north", "west"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := m.Near(t.Context(), c.q, c.k)
			if err != nil {
				t.Fatal(err)
			}
			equal(t, ids(got), c.want)
		})
	}
}

func TestMemNearScores(t *testing.T) {
	m := filled(t)
	got, err := m.Near(t.Context(), []float32{1, 0}, 4)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []float64{1, Cos([]float32{1, 0}, []float32{0.9, 0.1}), 0, -1} {
		eq(t, got[i].Score, want)
	}
}

func TestMemMetric(t *testing.T) {
	m := filled(t)
	if m.Metric() != "" {
		t.Errorf("a fresh store says its metric is %q, want empty for cosine", m.Metric())
	}

	// Cosine ignores length, so a short vector along east is as near as a
	// long one. Euclid does not.
	if err := m.Put(t.Context(), "far east", []float32{9, 0}, nil); err != nil {
		t.Fatal(err)
	}
	got, err := m.Near(t.Context(), []float32{1, 0}, 2)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, got[0].Score, 1)
	eq(t, got[1].Score, 1)

	m.Rank(Euclid)
	if m.Metric() != Euclid {
		t.Fatalf("Rank did not take: metric is %q", m.Metric())
	}
	got, err = m.Near(t.Context(), []float32{1, 0}, 2)
	if err != nil {
		t.Fatal(err)
	}
	equal(t, ids(got), []string{"east", "tilt"})
	eq(t, got[0].Score, 0)
	eq(t, got[1].Score, -Dist([]float32{1, 0}, []float32{0.9, 0.1}))
}

func TestMemQueryMetricOverridesStore(t *testing.T) {
	m := filled(t)
	m.Rank(Euclid)
	got, err := m.Search(t.Context(), Query{Vec: []float32{2, 0}, K: 1, Metric: Inner})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, got[0].Score, 2) // dot of (2,0) with east
}

func TestMemSearchFilters(t *testing.T) {
	m := filled(t)
	for _, c := range []struct {
		name string
		q    Query
		want []string
	}{
		{"filter narrows the ranking", Query{Vec: []float32{1, 0}, K: 2, Filter: Filter{}.Eq("lang", "fr")}, []string{"north", "west"}},
		{"filter alone keeps insertion order", Query{Filter: Filter{}.Eq("lang", "en")}, []string{"east", "tilt"}},
		{"filter alone with a limit", Query{K: 1, Filter: Filter{}.Eq("lang", "en")}, []string{"east"}},
		{"range", Query{Filter: Filter{}.Ge("year", 2022)}, []string{"north", "west"}},
		{"nothing matches", Query{Vec: []float32{1, 0}, K: 3, Filter: Filter{}.Eq("lang", "de")}, nil},
		{"no query at all is everything", Query{}, []string{"east", "tilt", "north", "west"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := m.Search(t.Context(), c.q)
			if err != nil {
				t.Fatal(err)
			}
			equal(t, ids(got), c.want)
		})
	}
}

func TestMemSpaceIsAFilter(t *testing.T) {
	m := &Mem{}
	ctx := t.Context()
	for _, c := range []struct{ id, space string }{
		{"a", "docs"}, {"b", "notes"}, {"c", "docs"},
	} {
		if err := m.Put(ctx, c.id, []float32{1, 0}, map[string]any{Space: c.space}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := m.Search(ctx, Query{Vec: []float32{1, 0}, Filter: Filter{}.Eq(Space, "docs")})
	if err != nil {
		t.Fatal(err)
	}
	equal(t, ids(got), []string{"a", "c"})
}

func TestMemPut(t *testing.T) {
	m := &Mem{}
	ctx := t.Context()

	if err := m.Put(ctx, "", []float32{1, 0}, nil); !errors.Is(err, ErrID) {
		t.Errorf("Put with no id gave %v, want ErrID", err)
	}
	if err := m.Put(ctx, "a", []float32{1, 0}, nil); err != nil {
		t.Fatal(err)
	}
	if err := m.Put(ctx, "b", []float32{1, 0, 0}, nil); !errors.Is(err, ErrDim) {
		t.Errorf("Put of a longer vector gave %v, want ErrDim", err)
	}
	if _, err := m.Near(ctx, []float32{1, 0, 0}, 1); !errors.Is(err, ErrDim) {
		t.Errorf("search with a longer vector gave %v, want ErrDim", err)
	}

	// A second Put under the same id replaces, it does not add.
	if err := m.Put(ctx, "a", []float32{0, 1}, map[string]any{"lang": "fr"}); err != nil {
		t.Fatal(err)
	}
	if got := m.Stat().Vectors; got != 1 {
		t.Errorf("store holds %d vectors, want 1", got)
	}
	v, meta, ok := m.Get("a")
	if !ok {
		t.Fatal("Get lost the vector it was just given")
	}
	if v[0] != 0 || v[1] != 1 || meta["lang"] != "fr" {
		t.Errorf("Get returned %v %v, want the second Put's values", v, meta)
	}
	if _, _, ok := m.Get("nobody"); ok {
		t.Error("Get found an id nobody wrote")
	}
}

func TestMemCopiesWhatItIsGiven(t *testing.T) {
	m := &Mem{}
	v := []float32{1, 0}
	meta := map[string]any{"lang": "en"}
	if err := m.Put(t.Context(), "a", v, meta); err != nil {
		t.Fatal(err)
	}
	v[0], meta["lang"] = 9, "fr"

	got, gotMeta, _ := m.Get("a")
	if got[0] != 1 {
		t.Errorf("changing the caller's slice changed the store: %v", got)
	}
	if gotMeta["lang"] != "en" {
		t.Errorf("changing the caller's map changed the store: %v", gotMeta)
	}
	gotMeta["lang"] = "de"
	if _, again, _ := m.Get("a"); again["lang"] != "en" {
		t.Errorf("changing what Get returned changed the store: %v", again)
	}
}

func TestMemDrop(t *testing.T) {
	m := filled(t)
	ctx := t.Context()
	if err := m.Drop(ctx, "east", "nobody"); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := m.Get("east"); ok {
		t.Error("Drop left the vector behind")
	}
	if got := m.Stat().Vectors; got != 3 {
		t.Errorf("store holds %d vectors, want 3", got)
	}
	got, err := m.Near(ctx, []float32{1, 0}, 4)
	if err != nil {
		t.Fatal(err)
	}
	equal(t, ids(got), []string{"tilt", "north", "west"})

	if err := m.Drop(ctx, "tilt", "north", "west"); err != nil {
		t.Fatal(err)
	}
	// An empty store forgets its dimension, so it can be refilled.
	if err := m.Put(ctx, "wide", []float32{1, 2, 3}, nil); err != nil {
		t.Errorf("refilling an emptied store: %v", err)
	}
}

func TestMemScan(t *testing.T) {
	m := filled(t)
	for _, c := range []struct {
		name          string
		offset, limit int
		want          []string
	}{
		{"from the start", 0, 2, []string{"east", "tilt"}},
		{"from the middle", 2, 2, []string{"north", "west"}},
		{"no limit", 0, 0, []string{"east", "tilt", "north", "west"}},
		{"past the end", 9, 2, nil},
		{"limit past the end", 3, 9, []string{"west"}},
		{"negative offset reads as zero", -1, 1, []string{"east"}},
	} {
		t.Run(c.name, func(t *testing.T) { equal(t, ids(m.Scan(c.offset, c.limit)), c.want) })
	}
}

func TestMemSearchesEmptyStore(t *testing.T) {
	m := &Mem{}
	got, err := m.Near(t.Context(), []float32{1, 0}, 5)
	if err != nil {
		t.Fatalf("searching an empty store: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("an empty store answered %v", got)
	}
}

// A small graph: a → b → c, and d on its own.
func linked(t *testing.T) *Mem {
	t.Helper()
	m := &Mem{}
	ctx := t.Context()
	if err := m.Edge(ctx, "a", "b", "knows"); err != nil {
		t.Fatal(err)
	}
	if err := m.Edge(ctx, "b", "c", "knows"); err != nil {
		t.Fatal(err)
	}
	if err := m.Edge(ctx, "a", "c", "wrote"); err != nil {
		t.Fatal(err)
	}
	if err := m.Node(ctx, "d", map[string]any{"kind": "island"}); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMemNodeProps(t *testing.T) {
	m := &Mem{}
	ctx := t.Context()
	if err := m.Node(ctx, "", nil); !errors.Is(err, ErrID) {
		t.Errorf("Node with no id gave %v, want ErrID", err)
	}
	if err := m.Node(ctx, "a", map[string]any{"name": "Ada", "born": 1815}); err != nil {
		t.Fatal(err)
	}
	// A second write sets what it names and leaves the rest.
	if err := m.Node(ctx, "a", map[string]any{"born": 1816}); err != nil {
		t.Fatal(err)
	}
	p, ok := m.Props("a")
	if !ok {
		t.Fatal("Props lost the node")
	}
	if p["name"] != "Ada" || p["born"] != 1816 {
		t.Errorf("properties are %v, want name kept and born replaced", p)
	}
	if _, ok := m.Props("nobody"); ok {
		t.Error("Props found a node nobody wrote")
	}
}

func TestMemEdge(t *testing.T) {
	m := linked(t)
	ctx := t.Context()

	if err := m.Edge(ctx, "", "b", "knows"); !errors.Is(err, ErrID) {
		t.Errorf("Edge with no subject gave %v, want ErrID", err)
	}
	// An edge creates the nodes at both ends.
	equal(t, m.Nodes(), []string{"a", "b", "c", "d"})

	out, err := m.Out(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	equal(t, out, []string{"b", "c"})

	in, err := m.In(ctx, "c")
	if err != nil {
		t.Fatal(err)
	}
	equal(t, in, []string{"b", "a"})

	// The same edge twice is one edge.
	if err := m.Edge(ctx, "a", "b", "knows"); err != nil {
		t.Fatal(err)
	}
	if got := m.Stat().Edges; got != 3 {
		t.Errorf("store holds %d edges, want 3", got)
	}
	// A second label between the same nodes is a second edge.
	if err := m.Edge(ctx, "a", "b", "wrote"); err != nil {
		t.Fatal(err)
	}
	if got := m.Stat().Edges; got != 4 {
		t.Errorf("store holds %d edges, want 4", got)
	}
}

func TestMemEdges(t *testing.T) {
	m := linked(t)
	for _, c := range []struct {
		name            string
		from, to, label string
		want            []Link
	}{
		{"everything", "", "", "", []Link{{"a", "b", "knows"}, {"b", "c", "knows"}, {"a", "c", "wrote"}}},
		{"by subject", "a", "", "", []Link{{"a", "b", "knows"}, {"a", "c", "wrote"}}},
		{"by object", "", "c", "", []Link{{"b", "c", "knows"}, {"a", "c", "wrote"}}},
		{"by label", "", "", "knows", []Link{{"a", "b", "knows"}, {"b", "c", "knows"}}},
		{"by all three", "a", "c", "wrote", []Link{{"a", "c", "wrote"}}},
		{"no match", "c", "a", "", nil},
	} {
		t.Run(c.name, func(t *testing.T) { equal(t, m.Edges(c.from, c.to, c.label), c.want) })
	}
}

func TestMemCut(t *testing.T) {
	m := linked(t)
	ctx := t.Context()
	if err := m.Cut(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Props("b"); ok {
		t.Error("Cut left the node behind")
	}
	equal(t, m.Edges("", "", ""), []Link{{"a", "c", "wrote"}})
	out, _ := m.Out(ctx, "a")
	equal(t, out, []string{"c"})

	if err := m.Cut(ctx, "nobody"); err != nil {
		t.Errorf("cutting a node nobody wrote: %v", err)
	}
}

func TestMemUnlink(t *testing.T) {
	for _, c := range []struct {
		name            string
		from, to, label string
		want            []Link
	}{
		{"one edge", "a", "b", "knows", []Link{{"b", "c", "knows"}, {"a", "c", "wrote"}}},
		{"everything leaving a", "a", "", "", []Link{{"b", "c", "knows"}}},
		{"every knows", "", "", "knows", []Link{{"a", "c", "wrote"}}},
		{"everything", "", "", "", nil},
		{"no match", "c", "a", "", []Link{{"a", "b", "knows"}, {"b", "c", "knows"}, {"a", "c", "wrote"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := linked(t)
			if err := m.Unlink(t.Context(), c.from, c.to, c.label); err != nil {
				t.Fatal(err)
			}
			equal(t, m.Edges("", "", ""), c.want)
			// Adjacency has to be rebuilt, not just the edge list.
			out, _ := m.Out(t.Context(), "a")
			var want []string
			for _, l := range c.want {
				if l.From == "a" {
					want = append(want, l.To)
				}
			}
			equal(t, out, want)
		})
	}
}

func TestMemPath(t *testing.T) {
	m := linked(t)
	ctx := t.Context()
	for _, c := range []struct {
		name     string
		from, to string
		want     []string
	}{
		{"one hop", "a", "b", []string{"a", "b"}},
		{"shortest of two", "a", "c", []string{"a", "c"}},
		{"two hops", "b", "c", []string{"b", "c"}},
		{"a node to itself", "a", "a", []string{"a"}},
		{"against the arrows", "c", "a", nil},
		{"no such start", "nobody", "a", nil},
		{"no such end", "a", "nobody", nil},
		{"unreachable", "a", "d", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := m.Path(ctx, c.from, c.to)
			if err != nil {
				t.Fatal(err)
			}
			equal(t, got, c.want)
		})
	}
}

func TestMemPathTakesTheShortWay(t *testing.T) {
	m := &Mem{}
	ctx := t.Context()
	for _, l := range []Link{{"a", "b", ""}, {"b", "c", ""}, {"c", "z", ""}, {"a", "z", ""}} {
		if err := m.Edge(ctx, l.From, l.To, l.Label); err != nil {
			t.Fatal(err)
		}
	}
	got, err := m.Path(ctx, "a", "z")
	if err != nil {
		t.Fatal(err)
	}
	equal(t, got, []string{"a", "z"})
}

func TestMemReach(t *testing.T) {
	m := linked(t)
	ctx := t.Context()
	for _, c := range []struct {
		name string
		id   string
		hops int
		want []string
	}{
		{"one hop", "a", 1, []string{"b", "c"}},
		{"two hops reaches no further here", "a", 2, []string{"b", "c"}},
		{"either direction", "c", 1, []string{"b", "a"}},
		{"two hops backwards", "c", 2, []string{"b", "a"}},
		{"no hops", "a", 0, nil},
		{"an island", "d", 3, nil},
		{"no such node", "nobody", 1, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := m.Reach(ctx, c.id, c.hops)
			if err != nil {
				t.Fatal(err)
			}
			equal(t, got, c.want)
		})
	}
}

func TestMemDegree(t *testing.T) {
	m := linked(t)
	for _, c := range []struct {
		id      string
		in, out int
	}{
		{"a", 0, 2},
		{"b", 1, 1},
		{"c", 2, 0},
		{"d", 0, 0},
		{"nobody", 0, 0},
	} {
		if in, out := m.Degree(c.id); in != c.in || out != c.out {
			t.Errorf("%s has degree in %d out %d, want in %d out %d", c.id, in, out, c.in, c.out)
		}
	}
}

func TestMemComponents(t *testing.T) {
	m := linked(t)
	got := m.Components()
	if len(got) != 2 {
		t.Fatalf("found %d components, want 2: %v", len(got), got)
	}
	equal(t, got[0], []string{"a", "b", "c"})
	equal(t, got[1], []string{"d"})

	if len((&Mem{}).Components()) != 0 {
		t.Error("an empty graph has no components")
	}
}

func TestMemAssertAndMatch(t *testing.T) {
	m := &Mem{}
	ctx := t.Context()
	facts := [][3]string{
		{"ada", "wrote", "notes"},
		{"ada", "knows", "charles"},
		{"charles", "wrote", "engine"},
	}
	for _, f := range facts {
		if err := m.Assert(ctx, f[0], f[1], f[2]); err != nil {
			t.Fatal(err)
		}
	}
	// The same claim twice is one fact.
	if err := m.Assert(ctx, "ada", "wrote", "notes"); err != nil {
		t.Fatal(err)
	}
	if got := m.Stat().Facts; got != 3 {
		t.Errorf("store holds %d facts, want 3", got)
	}

	for _, c := range []struct {
		name    string
		s, p, o string
		want    [][3]string
	}{
		{"everything", "", "", "", facts},
		{"by subject", "ada", "", "", facts[:2]},
		{"by predicate", "", "wrote", "", [][3]string{facts[0], facts[2]}},
		{"by object", "", "", "engine", [][3]string{facts[2]}},
		{"fully bound", "ada", "knows", "charles", [][3]string{facts[1]}},
		{"no match", "ada", "knows", "engine", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := m.Match(ctx, c.s, c.p, c.o)
			if err != nil {
				t.Fatal(err)
			}
			equal(t, got, c.want)
		})
	}
}

func TestMemAssertRefusesEmptyTerms(t *testing.T) {
	m := &Mem{}
	for _, f := range [][3]string{{"", "p", "o"}, {"s", "", "o"}, {"s", "p", ""}} {
		if err := m.Assert(t.Context(), f[0], f[1], f[2]); !errors.Is(err, ErrTerm) {
			t.Errorf("Assert%v gave %v, want ErrTerm", f, err)
		}
	}
}

func TestMemRetract(t *testing.T) {
	for _, c := range []struct {
		name    string
		s, p, o string
		want    int
	}{
		{"one fact", "ada", "wrote", "notes", 2},
		{"by subject", "ada", "", "", 1},
		{"by predicate", "", "wrote", "", 1},
		{"everything", "", "", "", 0},
		{"no match", "nobody", "", "", 3},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := &Mem{}
			ctx := t.Context()
			for _, f := range [][3]string{
				{"ada", "wrote", "notes"},
				{"ada", "knows", "charles"},
				{"charles", "wrote", "engine"},
			} {
				if err := m.Assert(ctx, f[0], f[1], f[2]); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.Retract(ctx, c.s, c.p, c.o); err != nil {
				t.Fatal(err)
			}
			if got := m.Stat().Facts; got != c.want {
				t.Errorf("%d facts left, want %d", got, c.want)
			}
			// A retracted fact can be asserted again.
			if err := m.Assert(ctx, "ada", "wrote", "notes"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMemStat(t *testing.T) {
	m := filled(t)
	ctx := t.Context()
	if err := m.Edge(ctx, "a", "b", "knows"); err != nil {
		t.Fatal(err)
	}
	if err := m.Assert(ctx, "a", "knows", "b"); err != nil {
		t.Fatal(err)
	}
	want := Stat{Vectors: 4, Nodes: 2, Edges: 1, Facts: 1}
	if got := m.Stat(); got != want {
		t.Errorf("Stat = %+v, want %+v", got, want)
	}
}

func TestMemDumpAndLoad(t *testing.T) {
	m := filled(t)
	ctx := t.Context()
	m.Rank(Euclid)
	if err := m.Edge(ctx, "a", "b", "knows"); err != nil {
		t.Fatal(err)
	}
	if err := m.Node(ctx, "a", map[string]any{"name": "Ada"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Assert(ctx, "ada", "wrote", "notes"); err != nil {
		t.Fatal(err)
	}

	other := &Mem{}
	if err := other.Load(ctx, m.Dump()); err != nil {
		t.Fatal(err)
	}
	if got, want := other.Stat(), m.Stat(); got != want {
		t.Errorf("loaded store holds %+v, want %+v", got, want)
	}
	if other.Metric() != Euclid {
		t.Errorf("loaded store ranks by %q, want %q", other.Metric(), Euclid)
	}
	p, _ := other.Props("a")
	if p["name"] != "Ada" {
		t.Errorf("loaded node has %v, want its name", p)
	}
	got, err := other.Match(ctx, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	equal(t, got, [][3]string{{"ada", "wrote", "notes"}})

	// Loading replaces; it does not merge.
	if err := other.Load(ctx, Snap{}); err != nil {
		t.Fatal(err)
	}
	if got := (other.Stat()); got != (Stat{}) {
		t.Errorf("loading an empty snapshot left %+v", got)
	}
}

func TestMemHonoursContext(t *testing.T) {
	m := filled(t)
	ctx, stop := context.WithCancel(context.Background())
	stop()
	for _, c := range []struct {
		name string
		call func() error
	}{
		{"Put", func() error { return m.Put(ctx, "a", []float32{1, 0}, nil) }},
		{"Near", func() error { _, err := m.Near(ctx, []float32{1, 0}, 1); return err }},
		{"Drop", func() error { return m.Drop(ctx, "a") }},
		{"Node", func() error { return m.Node(ctx, "a", nil) }},
		{"Edge", func() error { return m.Edge(ctx, "a", "b", "c") }},
		{"Out", func() error { _, err := m.Out(ctx, "a"); return err }},
		{"In", func() error { _, err := m.In(ctx, "a"); return err }},
		{"Cut", func() error { return m.Cut(ctx, "a") }},
		{"Unlink", func() error { return m.Unlink(ctx, "a", "", "") }},
		{"Path", func() error { _, err := m.Path(ctx, "a", "b"); return err }},
		{"Reach", func() error { _, err := m.Reach(ctx, "a", 1); return err }},
		{"Assert", func() error { return m.Assert(ctx, "a", "b", "c") }},
		{"Match", func() error { _, err := m.Match(ctx, "", "", ""); return err }},
		{"Retract", func() error { return m.Retract(ctx, "", "", "") }},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := c.call(); !errors.Is(err, context.Canceled) {
				t.Errorf("gave %v, want context.Canceled", err)
			}
		})
	}
}

func TestMemIsSafeForConcurrentUse(t *testing.T) {
	m := &Mem{}
	ctx := t.Context()
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := string(rune('a' + i))
			for j := 0; j < 50; j++ {
				m.Put(ctx, id, []float32{float32(i), float32(j)}, map[string]any{"n": j})
				m.Edge(ctx, id, "hub", "knows")
				m.Assert(ctx, id, "knows", "hub")
				m.Near(ctx, []float32{1, 0}, 3)
				m.Match(ctx, "", "knows", "")
				m.Stat()
			}
		}()
	}
	wg.Wait()
	if got := m.Stat(); got.Vectors != 8 || got.Facts != 8 {
		t.Errorf("after the storm: %+v, want 8 vectors and 8 facts", got)
	}
}

func TestMemZeroValueWorks(t *testing.T) {
	var m Mem
	ctx := t.Context()
	if err := m.Put(ctx, "a", []float32{1}, nil); err != nil {
		t.Fatal(err)
	}
	if err := m.Edge(ctx, "a", "b", "knows"); err != nil {
		t.Fatal(err)
	}
	if err := m.Assert(ctx, "a", "knows", "b"); err != nil {
		t.Fatal(err)
	}
	if got := m.Stat(); got.Vectors != 1 || got.Nodes != 2 || got.Edges != 1 || got.Facts != 1 {
		t.Errorf("the zero store holds %+v", got)
	}
}

func TestMemErrorsName(t *testing.T) {
	for _, err := range []error{ErrID, ErrTerm, ErrDim, ErrDriver} {
		if !strings.HasPrefix(err.Error(), "store: ") {
			t.Errorf("%q does not say which package it came from", err)
		}
	}
}
