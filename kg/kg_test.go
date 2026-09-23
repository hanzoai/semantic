package kg

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sort"
	"testing"

	"github.com/hanzoai/semantic"
)

// tri is a triple with a source, since provenance is half of what the graph
// is for.
func tri(s, p, o string, score float64, doc string) semantic.Triple {
	return semantic.Triple{
		Subject:   s,
		Predicate: p,
		Object:    o,
		Score:     score,
		From:      semantic.Chunk{DocID: doc},
	}
}

// star is the graph the Python centrality tests use: A joined to four leaves.
func star() *Graph {
	return Build([]semantic.Triple{
		tri("A", "knows", "B", 0, ""),
		tri("A", "knows", "C", 0, ""),
		tri("A", "knows", "D", 0, ""),
		tri("A", "knows", "E", 0, ""),
	})
}

// cliques is the Python community test: two triangles joined by one edge.
func cliques() *Graph {
	return Build([]semantic.Triple{
		tri("1", "r", "2", 0, ""), tri("2", "r", "3", 0, ""), tri("3", "r", "1", 0, ""),
		tri("4", "r", "5", 0, ""), tri("5", "r", "6", 0, ""), tri("6", "r", "4", 0, ""),
		tri("3", "r", "4", 0, ""),
	})
}

// line is A–B–C: the Python path and bridge test.
func line() *Graph {
	return Build([]semantic.Triple{
		tri("A", "r", "B", 0, ""),
		tri("B", "r", "C", 0, ""),
	})
}

func TestFold(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"Apple Inc.", "apple inc."},
		{" Alice ", "alice"},
		{"alice", "alice"},
		{"Ada\tLovelace", "ada lovelace"},
		{"Ada  Lovelace", "ada lovelace"},
		{"   ", ""},
		{"\t", ""},
		{"", ""},
	} {
		if got := Fold(c.in); got != c.want {
			t.Errorf("Fold(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHashIsStableAndFolded(t *testing.T) {
	a := Hash("Ada Lovelace", "WROTE", "Note G")
	if a != Hash(" ada  lovelace ", "wrote", "note g") {
		t.Error("hash must not depend on spelling that folds away")
	}
	if a == Hash("Ada Lovelace", "WROTE", "Note A") {
		t.Error("different objects must hash differently")
	}
	if len(a) != 16 {
		t.Errorf("hash %q is %d chars, want a fixed 16", a, len(a))
	}
	// The separator must keep field boundaries: "ab|c" is not "a|bc".
	if Hash("ab", "c", "d") == Hash("a", "bc", "d") {
		t.Error("hash must not run fields together")
	}
}

func TestBuildFoldsNamesOntoOneNode(t *testing.T) {
	g := Build([]semantic.Triple{
		tri(" Alice ", "knows", "Bob", 0, ""),
		tri("alice", "knows", "Carol", 0, ""),
	})
	nodes, edges := g.Size()
	if nodes != 3 || edges != 2 {
		t.Fatalf("size = %d nodes, %d edges; want 3, 2", nodes, edges)
	}
	n, ok := g.Node("ALICE")
	if !ok {
		t.Fatal("Alice missing")
	}
	if n.ID != "alice" {
		t.Errorf("id = %q, want %q", n.ID, "alice")
	}
	if n.Name != "Alice" {
		t.Errorf("name = %q, want the first surface form %q", n.Name, "Alice")
	}
}

// Fuzzy near-matches are not the same node. This is the Python
// test_exact_resolution_does_not_merge_similar_names case: folding is exact,
// similarity is somebody else's job.
func TestBuildKeepsSimilarNamesApart(t *testing.T) {
	g := Build([]semantic.Triple{
		tri("Alice", "knows", "Bob", 0, ""),
		tri("Alicia", "knows", "Bob", 0, ""),
	})
	if _, ok := g.Node("alice"); !ok {
		t.Error("Alice missing")
	}
	if _, ok := g.Node("alicia"); !ok {
		t.Error("Alicia missing")
	}
	if n, _ := g.Size(); n != 3 {
		t.Errorf("%d nodes, want 3", n)
	}
}

func TestAddDropsIncompleteTriples(t *testing.T) {
	for _, c := range []struct {
		name string
		t    semantic.Triple
	}{
		{"no subject", tri("", "knows", "Bob", 0, "")},
		{"blank subject", tri("   ", "knows", "Bob", 0, "")},
		{"no predicate", tri("Alice", "", "Bob", 0, "")},
		{"blank predicate", tri("Alice", " \t ", "Bob", 0, "")},
		{"no object", tri("Alice", "knows", "", 0, "")},
	} {
		g := Build(nil)
		if g.Add(c.t) {
			t.Errorf("%s: Add reported the triple was taken", c.name)
		}
		if n, e := g.Size(); n != 0 || e != 0 {
			t.Errorf("%s: graph grew to %d nodes, %d edges", c.name, n, e)
		}
	}
}

// The merge contract: the same assertion twice is one edge, its confidence is
// the best evidence seen, and every source that carried it is remembered.
func TestRepeatedAssertionDoesNotDuplicate(t *testing.T) {
	g := Build([]semantic.Triple{
		tri("Ada", "wrote", "Note G", 0.6, "d1"),
		tri("ada", "WROTE", "note g", 0.9, "d2"),
		tri("Ada", "wrote", "Note G", 0.4, "d1"),
	})
	nodes, edges := g.Size()
	if nodes != 2 || edges != 1 {
		t.Fatalf("size = %d nodes, %d edges; want 2, 1", nodes, edges)
	}
	e := g.Edges()[0]
	if e.Count != 3 {
		t.Errorf("count = %d, want 3", e.Count)
	}
	if e.Score != 0.9 {
		t.Errorf("score = %v, want the highest asserted, 0.9", e.Score)
	}
	if !reflect.DeepEqual(e.Docs, []string{"d1", "d2"}) {
		t.Errorf("docs = %v, want [d1 d2] once each in order", e.Docs)
	}
	if e.Label != "wrote" {
		t.Errorf("label = %q, want the first surface form %q", e.Label, "wrote")
	}
	if n, _ := g.Node("ada"); n.Score != 0.9 {
		t.Errorf("node score = %v, want 0.9", n.Score)
	}
}

// A zero Score is an assertion that states no confidence. It leaves the best
// stated confidence where it was, and an edge no assertion scored stays
// unscored, below every edge one did.
func TestAbsentScoreStaysUnscored(t *testing.T) {
	g := Build([]semantic.Triple{
		tri("Apple", "acquired", "Beats", 0.95, ""),
		tri("Apple", "acquired", "Shazam", 0, ""),
		tri("Apple", "makes", "iPhone", 0.6, ""),
		tri("Apple", "makes", "iPhone", 0, ""),
	})
	want := map[string]float64{"beats": 0.95, "shazam": 0, "iphone": 0.6}
	for _, e := range g.Edges() {
		if e.Score != want[e.To] {
			t.Errorf("%s -> %s score = %v, want %v", e.From, e.To, e.Score, want[e.To])
		}
	}
	if n, _ := g.Node("Shazam"); n.Score != 0 {
		t.Errorf("node score = %v, want 0, unscored", n.Score)
	}
}

func TestScoreKeepsTheBestEvidence(t *testing.T) {
	for _, c := range []struct {
		name  string
		order []float64
		want  float64
	}{
		{"rising", []float64{0.2, 0.5, 0.8}, 0.8},
		{"falling", []float64{0.8, 0.5, 0.2}, 0.8},
		{"equal", []float64{0.5, 0.5}, 0.5},
	} {
		g := Build(nil)
		for _, s := range c.order {
			g.Add(tri("A", "r", "B", s, ""))
		}
		if got := g.Edges()[0].Score; got != c.want {
			t.Errorf("%s: score = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestTypePredicateAssignsTypeInsteadOfEdge(t *testing.T) {
	g := Build([]semantic.Triple{
		tri("Ada", "is_a", "Person", 0, ""),
		tri("Ada", "is_a", "Mathematician", 0, ""), // the first type stands
		tri("Ada", "wrote", "Note G", 0, ""),
	})
	nodes, edges := g.Size()
	if nodes != 2 || edges != 1 {
		t.Fatalf("size = %d nodes, %d edges; want 2, 1 — a type is not an edge", nodes, edges)
	}
	n, _ := g.Node("ada")
	if n.Type != "Person" {
		t.Errorf("type = %q, want %q", n.Type, "Person")
	}
	if _, ok := g.Node("person"); ok {
		t.Error("the type became a node of its own")
	}
}

func TestTypePredicatesAreConfigurable(t *testing.T) {
	g := Build([]semantic.Triple{tri("Ada", "kind", "Person", 0, "")}, "kind")
	if n, _ := g.Node("ada"); n.Type != "Person" {
		t.Errorf("type = %q, want %q", n.Type, "Person")
	}
	// With the defaults back, the same predicate is an ordinary edge.
	g = Build([]semantic.Triple{tri("Ada", "kind", "Person", 0, "")})
	if _, edges := g.Size(); edges != 1 {
		t.Errorf("%d edges, want 1", edges)
	}
}

func TestTraversal(t *testing.T) {
	g := Build([]semantic.Triple{
		tri("A", "r", "B", 0, ""),
		tri("A", "r", "C", 0, ""),
		tri("D", "r", "A", 0, ""),
	})
	if got := g.Out("A"); !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Errorf("Out(A) = %v, want [b c]", got)
	}
	if got := g.In("A"); !reflect.DeepEqual(got, []string{"d"}) {
		t.Errorf("In(A) = %v, want [d]", got)
	}
	if got := g.Adj("A"); !reflect.DeepEqual(got, []string{"b", "c", "d"}) {
		t.Errorf("Adj(A) = %v, want [b c d]", got)
	}
	if got := g.Out("nobody"); got != nil {
		t.Errorf("Out of an unknown node = %v, want nil", got)
	}
}

// Out and In name the far end of an assertion and nothing else. From and To
// hand back the assertion itself, which is what a caller that means to cite
// its evidence needs: the label it was made under, and the documents it was
// made in.
func TestFromAndToCarryTheAssertion(t *testing.T) {
	g := Build([]semantic.Triple{
		tri("A", "likes", "B", 0.9, "d1"),
		tri("A", "likes", "C", 0.8, "d2"),
		tri("D", "knows", "A", 0.7, "d3"),
	})
	out := g.From("A")
	if len(out) != 2 {
		t.Fatalf("From(A) = %d assertions, want 2", len(out))
	}
	if out[0].Label != "likes" || out[0].To != "b" || !reflect.DeepEqual(out[0].Docs, []string{"d1"}) {
		t.Errorf("From(A)[0] = %+v, want the likes-B assertion made in d1", out[0])
	}
	in := g.To("A")
	if len(in) != 1 || in[0].From != "d" || in[0].Label != "knows" {
		t.Errorf("To(A) = %+v, want the one assertion made into A", in)
	}
	if got := g.From("nobody"); len(got) != 0 {
		t.Errorf("From of an unknown node = %v, want none", got)
	}
	// A copy, like Nodes and Edges: writing to what a traversal returned must
	// not reach the graph.
	out[0].Label = "loathes"
	if again := g.From("A"); again[0].Label != "likes" {
		t.Error("writing to an assertion From returned reached the graph")
	}
}

// Walk is the edge-wise counterpart of Near: which assertions lie within so
// many hops, each reported once, at the fewest hops any path reached it.
// Direction is ignored, since an assertion relates both its ends however it
// happened to be written.
//
// Over two triangles joined by a bridge, standing on 1: one hop takes the two
// assertions 1 is an end of, the second closes its triangle and crosses the
// bridge, the third reaches two sides of the far triangle, and the fourth its
// last side.
func TestWalk(t *testing.T) {
	g := cliques()
	for _, c := range []struct {
		hops  int
		edges int
	}{{0, 0}, {1, 2}, {2, 4}, {3, 6}, {4, 7}} {
		got := g.Walk("1", c.hops)
		if len(got) != c.edges {
			t.Errorf("Walk(1, %d) reached %d assertions, want %d", c.hops, len(got), c.edges)
		}
		for _, s := range got {
			if s.Hop < 1 || s.Hop > c.hops {
				t.Errorf("Walk(1, %d) reported %+v at hop %d", c.hops, s.Edge, s.Hop)
			}
		}
	}
	if got := g.Walk("nobody", 2); got != nil {
		t.Errorf("Walk of an unknown node = %v, want nil", got)
	}
}

func TestSubIsTheInducedSubgraph(t *testing.T) {
	s := cliques().Sub("1", "2", "3", "4")
	nodes, edges := s.Size()
	if nodes != 4 {
		t.Errorf("%d nodes, want 4", nodes)
	}
	// The triangle's three edges plus the bridge 3–4; 4's own triangle is out.
	if edges != 4 {
		t.Errorf("%d edges, want 4", edges)
	}
	if got := s.Out("4"); got != nil {
		t.Errorf("Out(4) = %v, want nil: 5 and 6 are outside the subgraph", got)
	}
}

func TestSubDoesNotShareStateWithItsSource(t *testing.T) {
	g := line()
	s := g.Sub("A", "B")
	s.Add(tri("A", "r", "Z", 0, ""))
	if _, ok := g.Node("z"); ok {
		t.Error("writing to a subgraph reached the graph it came from")
	}
	if n, _ := s.Size(); n != 3 {
		t.Errorf("subgraph has %d nodes, want 3", n)
	}
}

func TestNear(t *testing.T) {
	g := cliques()
	for _, c := range []struct {
		hops int
		want []string
	}{
		{0, []string{"1"}},
		{1, []string{"1", "2", "3"}},
		{2, []string{"1", "2", "3", "4"}},
		{3, []string{"1", "2", "3", "4", "5", "6"}},
	} {
		got := ids(g.Near("1", c.hops))
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Near(1, %d) = %v, want %v", c.hops, got, c.want)
		}
	}
	if n, e := g.Near("nobody", 2).Size(); n != 0 || e != 0 {
		t.Errorf("Near of an unknown node = %d nodes, %d edges; want empty", n, e)
	}
}

func TestMergeFoldsOneNodeIntoAnother(t *testing.T) {
	g := Build([]semantic.Triple{
		tri("Apple Inc.", "makes", "Mac", 0.9, "d1"),
		tri("Apple", "makes", "Mac", 0.5, "d2"),
		tri("Apple", "founded_by", "Jobs", 0.7, "d2"),
		tri("Woz", "joined", "Apple", 0.6, "d3"),
	})
	if !g.Merge("Apple Inc.", "Apple") {
		t.Fatal("Merge reported nothing moved")
	}
	if _, ok := g.Node("apple"); ok {
		t.Error("the merged-away node is still there")
	}
	n, ok := g.Node("apple inc.")
	if !ok {
		t.Fatal("the surviving node is gone")
	}
	if !reflect.DeepEqual(n.Alias, []string{"apple"}) {
		t.Errorf("alias = %v, want [apple]", n.Alias)
	}
	if !reflect.DeepEqual(n.Docs, []string{"d1", "d2", "d3"}) {
		t.Errorf("docs = %v, want [d1 d2 d3]", n.Docs)
	}
	// makes→Mac was asserted from both names: one edge afterwards, counted
	// twice, holding the better score and both sources.
	var makes Edge
	for _, e := range g.Edges() {
		if e.Label == "makes" {
			makes = e
		}
	}
	if makes.From != "apple inc." || makes.Count != 2 || makes.Score != 0.9 {
		t.Errorf("makes edge = %+v; want from apple inc., count 2, score 0.9", makes)
	}
	if !reflect.DeepEqual(makes.Docs, []string{"d1", "d2"}) {
		t.Errorf("makes docs = %v, want [d1 d2]", makes.Docs)
	}
	if got := g.Out("apple inc."); !reflect.DeepEqual(got, []string{"mac", "jobs"}) {
		t.Errorf("Out = %v, want [mac jobs]", got)
	}
	if got := g.In("apple inc."); !reflect.DeepEqual(got, []string{"woz"}) {
		t.Errorf("In = %v, want [woz]", got)
	}
	if nodes, edges := g.Size(); nodes != 4 || edges != 3 {
		t.Errorf("size = %d nodes, %d edges; want 4, 3", nodes, edges)
	}
}

func TestMergeRefuses(t *testing.T) {
	g := line()
	for _, c := range []struct{ into, from string }{
		{"A", "A"},
		{"A", "nobody"},
		{"nobody", "A"},
	} {
		if g.Merge(c.into, c.from) {
			t.Errorf("Merge(%q, %q) reported a move", c.into, c.from)
		}
	}
	if n, e := g.Size(); n != 3 || e != 2 {
		t.Errorf("a refused merge changed the graph: %d nodes, %d edges", n, e)
	}
}

func TestMergeKeepsTypeAndTakesTheBetterScore(t *testing.T) {
	g := Build([]semantic.Triple{
		tri("Apple", "makes", "Mac", 0.4, ""),
		tri("Apple Inc.", "is_a", "Company", 0.9, ""),
	})
	if !g.Merge("Apple", "Apple Inc.") {
		t.Fatal("Merge reported nothing moved")
	}
	n, _ := g.Node("apple")
	if n.Type != "Company" {
		t.Errorf("type = %q, want the merged-in node's %q", n.Type, "Company")
	}
	if n.Score != 0.9 {
		t.Errorf("score = %v, want 0.9", n.Score)
	}
}

func TestNodesAndEdgesAreCopies(t *testing.T) {
	g := Build([]semantic.Triple{tri("A", "r", "B", 0.5, "d1")})
	g.Set("A", map[string]any{"k": "v"})

	n, _ := g.Node("A")
	n.Name, n.Score = "clobbered", 99
	n.Docs[0] = "clobbered"
	n.Props["k"] = "clobbered"
	e := g.Edges()[0]
	e.Docs[0] = "clobbered"

	again, _ := g.Node("A")
	if again.Name != "A" || again.Score != 0.5 || again.Docs[0] != "d1" || again.Props["k"] != "v" {
		t.Errorf("a returned node aliases the graph: %+v", again)
	}
	if g.Edges()[0].Docs[0] != "d1" {
		t.Error("a returned edge aliases the graph")
	}
}

func TestSetAddsAnIsolatedNode(t *testing.T) {
	g := line()
	if !g.Set("C", map[string]any{"kind": "loner"}) {
		t.Fatal("Set refused a usable id")
	}
	if g.Set("  ", nil) {
		t.Error("Set accepted a blank id")
	}
	// C is already in the line graph; a node nothing asserts must still enter.
	if !g.Set("Z", nil) {
		t.Fatal("Set refused Z")
	}
	n, ok := g.Node("Z")
	if !ok || n.ID != "z" {
		t.Fatalf("Z missing: %+v", n)
	}
	if got := g.Adj("Z"); got != nil {
		t.Errorf("Adj(Z) = %v, want nil", got)
	}
	// The first writer of a property wins.
	g.Set("C", map[string]any{"kind": "other"})
	if n, _ := g.Node("C"); n.Props["kind"] != "loner" {
		t.Errorf("kind = %v, want the first value written", n.Props["kind"])
	}
}

func TestEdgeIDNamesTheAssertion(t *testing.T) {
	g := Build([]semantic.Triple{tri("Ada", "wrote", "Note G", 0, "")})
	e := g.Edges()[0]
	if got, want := e.ID(), Hash("ada", "wrote", "note g"); got != want {
		t.Errorf("edge id = %q, want %q", got, want)
	}
}

// --- shape ---------------------------------------------------------------

func TestDegree(t *testing.T) {
	d := star().Degree()
	if !close(d["a"], 1.0) {
		t.Errorf("A = %v, want 1.0: joined to all four others", d["a"])
	}
	if !close(d["b"], 0.25) {
		t.Errorf("B = %v, want 0.25", d["b"])
	}
}

func TestDegreeCountsIsolatedNodes(t *testing.T) {
	g := Build([]semantic.Triple{tri("A", "r", "B", 0, "")})
	g.Set("C", nil)
	d := g.Degree()
	if len(d) != 3 {
		t.Fatalf("%d scores, want 3", len(d))
	}
	if d["c"] != 0 {
		t.Errorf("C = %v, want 0", d["c"])
	}
}

func TestBetween(t *testing.T) {
	b := star().Between()
	if !close(b["a"], 1.0) {
		t.Errorf("A = %v, want 1.0: every path between leaves runs through it", b["a"])
	}
	for _, leaf := range []string{"b", "c", "d", "e"} {
		if !close(b[leaf], 0) {
			t.Errorf("%s = %v, want 0", leaf, b[leaf])
		}
	}
	if b["a"] <= b["b"] {
		t.Error("the centre must score above a leaf")
	}
}

// A path graph: the middle node of A–B–C–D–E carries the most traffic, and
// betweenness ranks the nodes a graph would come apart without.
func TestBetweenOnAPath(t *testing.T) {
	g := Build([]semantic.Triple{
		tri("A", "r", "B", 0, ""), tri("B", "r", "C", 0, ""),
		tri("C", "r", "D", 0, ""), tri("D", "r", "E", 0, ""),
	})
	b := g.Between()
	// C carries A–D, A–E, B–D and B–E: four of the (4*3)/2 = 6 pairs it
	// could sit between. B carries A–C, A–D and A–E, so three of the six.
	if !close(b["c"], 4.0/6) {
		t.Errorf("C = %v, want 2/3", b["c"])
	}
	if !close(b["b"], 3.0/6) {
		t.Errorf("B = %v, want 1/2", b["b"])
	}
	if !close(b["a"], 0) {
		t.Errorf("A = %v, want 0", b["a"])
	}
}

func TestClose(t *testing.T) {
	c := star().Close()
	if !close(c["a"], 1.0) {
		t.Errorf("A = %v, want 1.0: one step from everything", c["a"])
	}
	if !close(c["b"], 4.0/7) {
		t.Errorf("B = %v, want 4/7", c["b"])
	}
}

func TestCloseOfAnUnreachedNodeIsZero(t *testing.T) {
	g := line()
	g.Set("Z", nil)
	if got := g.Close()["z"]; got != 0 {
		t.Errorf("Z = %v, want 0", got)
	}
}

func TestEigen(t *testing.T) {
	e := star().Eigen()
	top := Top(e, 1)
	if len(top) == 0 || top[0].ID != "a" {
		t.Errorf("top = %v, want a", top)
	}
	if !close(e["a"], 1.0) {
		t.Errorf("A = %v, want the scale's top, 1.0", e["a"])
	}
	for _, leaf := range []string{"b", "c", "d", "e"} {
		if e[leaf] >= e["a"] {
			t.Errorf("%s = %v, must be under the centre", leaf, e[leaf])
		}
	}
}

func TestRankSumsToOne(t *testing.T) {
	g := cliques()
	r := g.Rank()
	var sum float64
	for _, v := range r {
		sum += v
	}
	if !close(sum, 1.0) {
		t.Errorf("scores sum to %v, want 1", sum)
	}
	if len(r) != 6 {
		t.Errorf("%d scores, want 6", len(r))
	}
}

// A node everything points at and that points nowhere still gets its share,
// and the share it cannot pass on is redistributed rather than lost.
func TestRankHandlesANodeWithNoWayOut(t *testing.T) {
	g := Build([]semantic.Triple{
		tri("A", "r", "Z", 0, ""),
		tri("B", "r", "Z", 0, ""),
		tri("C", "r", "Z", 0, ""),
	})
	r := g.Rank()
	var sum float64
	for _, v := range r {
		sum += v
	}
	if !close(sum, 1.0) {
		t.Errorf("scores sum to %v, want 1", sum)
	}
	if top := Top(r, 1); top[0].ID != "z" {
		t.Errorf("top = %v, want z", top)
	}
}

func TestParts(t *testing.T) {
	g := Build([]semantic.Triple{
		tri("A", "r", "B", 0, ""),
		tri("C", "r", "D", 0, ""),
	})
	parts := g.Parts()
	if len(parts) != 2 {
		t.Fatalf("%d components, want 2", len(parts))
	}
	for _, p := range parts {
		if len(p) != 2 {
			t.Errorf("component %v has %d nodes, want 2", p, len(p))
		}
	}
}

func TestPartsKeepsAnIsolatedNodeAsItsOwn(t *testing.T) {
	g := Build([]semantic.Triple{tri("A", "r", "B", 0, "")})
	g.Set("C", nil)
	parts := g.Parts()
	if len(parts) != 2 {
		t.Fatalf("%d components, want 2", len(parts))
	}
	if !reflect.DeepEqual(parts[0], []string{"a", "b"}) {
		t.Errorf("largest = %v, want [a b]", parts[0])
	}
	if !reflect.DeepEqual(parts[1], []string{"c"}) {
		t.Errorf("second = %v, want [c]", parts[1])
	}
}

func TestPath(t *testing.T) {
	g := line()
	got := g.Path("A", "C")
	if !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("Path(A, C) = %v, want [a b c]", got)
	}
	if len(got)-1 != 2 {
		t.Errorf("%d steps, want 2", len(got)-1)
	}
	if got := g.Path("A", "A"); !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("Path(A, A) = %v, want [a]", got)
	}
	g.Set("Z", nil)
	if got := g.Path("A", "Z"); got != nil {
		t.Errorf("Path to an unreachable node = %v, want nil", got)
	}
	if got := g.Path("A", "nobody"); got != nil {
		t.Errorf("Path to an unknown node = %v, want nil", got)
	}
}

// Direction is what Out and In are for; a route is a route either way.
func TestPathIgnoresDirection(t *testing.T) {
	g := Build([]semantic.Triple{
		tri("A", "r", "B", 0, ""),
		tri("C", "r", "B", 0, ""),
	})
	if got := g.Path("A", "C"); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("Path(A, C) = %v, want [a b c]", got)
	}
}

func TestBridges(t *testing.T) {
	for _, c := range []struct {
		name string
		g    *Graph
		want []string // labelled by "from->to"
	}{
		{"every edge of a line", line(), []string{"a->b", "b->c"}},
		{"the join between two triangles", cliques(), []string{"3->4"}},
		{"a star's spokes", star(), []string{"a->b", "a->c", "a->d", "a->e"}},
	} {
		var got []string
		for _, e := range c.g.Bridges() {
			got = append(got, e.From+"->"+e.To)
		}
		sort.Strings(got)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: bridges = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestGroups(t *testing.T) {
	groups := cliques().Groups()
	if len(groups) != 2 {
		t.Fatalf("%d groups, want 2: %v", len(groups), groups)
	}
	of := map[string]int{}
	for i, grp := range groups {
		for _, id := range grp {
			of[id] = i
		}
	}
	for _, same := range [][2]string{{"1", "2"}, {"2", "3"}, {"4", "5"}, {"5", "6"}} {
		if of[same[0]] != of[same[1]] {
			t.Errorf("%s and %s landed in different groups: %v", same[0], same[1], groups)
		}
	}
	if of["1"] == of["4"] {
		t.Errorf("the two triangles were not told apart: %v", groups)
	}
}

func TestGroupsKeepsIsolatedNodesAlone(t *testing.T) {
	g := Build([]semantic.Triple{tri("A", "r", "B", 0, "")})
	g.Set("C", nil)
	groups := g.Groups()
	if len(groups) != 2 {
		t.Fatalf("%d groups, want 2: %v", len(groups), groups)
	}
	if !reflect.DeepEqual(groups[1], []string{"c"}) {
		t.Errorf("second group = %v, want [c]", groups[1])
	}
}

func TestGroupsOfAnEdgelessGraphAreSingletons(t *testing.T) {
	g := Build(nil)
	g.Set("A", nil)
	g.Set("B", nil)
	groups := g.Groups()
	if !reflect.DeepEqual(groups, [][]string{{"a"}, {"b"}}) {
		t.Errorf("groups = %v, want [[a] [b]]", groups)
	}
}

func TestModularity(t *testing.T) {
	g := cliques()
	split := g.Groups()
	one := [][]string{ids(g)}
	if q := g.Modularity(split); q < 0.3 {
		t.Errorf("the two triangles score %v, want community structure above 0.3", q)
	}
	if q := g.Modularity(one); !close(q, 0) {
		t.Errorf("one group holding everything scores %v, want 0", q)
	}
	if g.Modularity(split) <= g.Modularity(one) {
		t.Error("the split must score above the lump")
	}
	if q := Build(nil).Modularity(nil); q != 0 {
		t.Errorf("empty graph scores %v, want 0", q)
	}
}

func TestStat(t *testing.T) {
	g := cliques()
	s := g.Stat()
	if s.Nodes != 6 || s.Edges != 7 || s.Links != 7 {
		t.Errorf("counts = %d nodes, %d edges, %d links; want 6, 7, 7", s.Nodes, s.Edges, s.Links)
	}
	if !close(s.Density, 7.0/15) {
		t.Errorf("density = %v, want 7/15", s.Density)
	}
	if s.Deg.Min != 2 || s.Deg.Max != 3 {
		t.Errorf("degree span = %d..%d, want 2..3", s.Deg.Min, s.Deg.Max)
	}
	if !close(s.Deg.Avg, 14.0/6) {
		t.Errorf("mean degree = %v, want 14/6", s.Deg.Avg)
	}
	if s.Parts != 1 || s.Biggest != 6 || !s.Whole {
		t.Errorf("connectivity = %d parts, biggest %d, whole %v; want 1, 6, true", s.Parts, s.Biggest, s.Whole)
	}
	if s.Shape != "moderate" {
		t.Errorf("shape = %q, want moderate", s.Shape)
	}
	if s.Path <= 1 || s.Path >= 3 {
		t.Errorf("mean path = %v, want between 1 and 3", s.Path)
	}
}

func TestStatShape(t *testing.T) {
	split := Build([]semantic.Triple{tri("A", "r", "B", 0, ""), tri("C", "r", "D", 0, "")})
	if got := split.Stat().Shape; got != "disconnected" {
		t.Errorf("two components: %q, want disconnected", got)
	}
	if got := line().Stat().Shape; got != "dense" {
		t.Errorf("A-B-C has density 2/3: %q, want dense", got)
	}
	// A long thin chain: few of the possible links are present.
	chain := Build(nil)
	for i := range 30 {
		chain.Add(tri(string(rune('a'+i)), "r", string(rune('b'+i)), 0, ""))
	}
	if got := chain.Stat().Shape; got != "sparse" {
		t.Errorf("a 31-node chain: %q, want sparse", got)
	}
	if got := Build(nil).Stat(); got.Nodes != 0 || got.Deg.Min != 0 || got.Shape != "sparse" {
		t.Errorf("empty graph = %+v", got)
	}
}

func TestStatPathIsTheMeanOverReachablePairs(t *testing.T) {
	// A–B–C: four pairs at one step, two at two steps.
	if got := line().Stat().Path; !close(got, (1+1+1+1+2+2)/6.0) {
		t.Errorf("mean path = %v, want 8/6", got)
	}
	// An unreachable node is left out of the mean rather than counted as far.
	g := line()
	g.Set("Z", nil)
	if got := g.Stat().Path; !close(got, (1+1+1+1+2+2)/6.0) {
		t.Errorf("mean path with an isolated node = %v, want 8/6", got)
	}
}

func TestTop(t *testing.T) {
	score := map[string]float64{"c": 0.5, "a": 0.9, "b": 0.5, "d": 0.1}
	want := []Pair{{"a", 0.9}, {"b", 0.5}, {"c", 0.5}, {"d", 0.1}}
	if got := Top(score, 0); !reflect.DeepEqual(got, want) {
		t.Errorf("Top all = %v, want %v", got, want)
	}
	if got := Top(score, 2); !reflect.DeepEqual(got, want[:2]) {
		t.Errorf("Top 2 = %v, want %v", got, want[:2])
	}
	if got := Top(nil, 3); len(got) != 0 {
		t.Errorf("Top of nothing = %v, want empty", got)
	}
}

// --- store ---------------------------------------------------------------

func TestMemServesTheStoreInterfaces(t *testing.T) {
	ctx := context.Background()
	var m Mem

	if err := m.Node(ctx, "Ada", map[string]any{"born": 1815}); err != nil {
		t.Fatalf("Node: %v", err)
	}
	if err := m.Edge(ctx, "Ada", "Note G", "wrote"); err != nil {
		t.Fatalf("Edge: %v", err)
	}
	if err := m.Assert(ctx, "Ada", "wrote", "Note G"); err != nil {
		t.Fatalf("Assert: %v", err)
	}

	out, err := m.Out(ctx, "Ada")
	if err != nil {
		t.Fatalf("Out: %v", err)
	}
	if !reflect.DeepEqual(out, []string{"note g"}) {
		t.Errorf("Out = %v, want [note g]", out)
	}

	// The store writes go through the same fold: two ways of saying one
	// thing is one edge.
	if nodes, edges := m.Graph().Size(); nodes != 2 || edges != 1 {
		t.Errorf("size = %d nodes, %d edges; want 2, 1", nodes, edges)
	}
	if n, _ := m.Graph().Node("Ada"); n.Props["born"] != 1815 {
		t.Errorf("props = %v, want born 1815", n.Props)
	}
}

func TestMemMatch(t *testing.T) {
	ctx := context.Background()
	m := Keep(Build([]semantic.Triple{
		tri("Ada", "wrote", "Note G", 0, ""),
		tri("Ada", "knew", "Babbage", 0, ""),
		tri("Babbage", "built", "Engine", 0, ""),
	}))
	for _, c := range []struct {
		s, p, o string
		want    [][3]string
	}{
		{"", "", "", [][3]string{
			{"ada", "wrote", "note g"},
			{"ada", "knew", "babbage"},
			{"babbage", "built", "engine"},
		}},
		{"Ada", "", "", [][3]string{{"ada", "wrote", "note g"}, {"ada", "knew", "babbage"}}},
		{"", "built", "", [][3]string{{"babbage", "built", "engine"}}},
		{"", "", "Babbage", [][3]string{{"ada", "knew", "babbage"}}},
		{"ada", "wrote", "note g", [][3]string{{"ada", "wrote", "note g"}}},
		{"nobody", "", "", nil},
	} {
		got, err := m.Match(ctx, c.s, c.p, c.o)
		if err != nil {
			t.Fatalf("Match(%q,%q,%q): %v", c.s, c.p, c.o, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Match(%q,%q,%q) = %v, want %v", c.s, c.p, c.o, got, c.want)
		}
	}
}

func TestMemRejectsWhatItCannotStore(t *testing.T) {
	ctx := context.Background()
	var m Mem
	if err := m.Node(ctx, "  ", nil); err == nil {
		t.Error("a blank node id was accepted")
	}
	if err := m.Assert(ctx, "Ada", "", "Note G"); err == nil {
		t.Error("a triple with no predicate was accepted")
	}
	if nodes, _ := m.Graph().Size(); nodes != 0 {
		t.Errorf("%d nodes, want none", nodes)
	}
}

func TestMemHonoursACancelledContext(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	stop()
	m := Keep(line())
	if err := m.Node(ctx, "A", nil); !errors.Is(err, context.Canceled) {
		t.Errorf("Node: %v, want context.Canceled", err)
	}
	if err := m.Edge(ctx, "A", "B", "r"); !errors.Is(err, context.Canceled) {
		t.Errorf("Edge: %v, want context.Canceled", err)
	}
	if err := m.Assert(ctx, "A", "r", "B"); !errors.Is(err, context.Canceled) {
		t.Errorf("Assert: %v, want context.Canceled", err)
	}
	if _, err := m.Out(ctx, "A"); !errors.Is(err, context.Canceled) {
		t.Errorf("Out: %v, want context.Canceled", err)
	}
	if _, err := m.Match(ctx, "", "", ""); !errors.Is(err, context.Canceled) {
		t.Errorf("Match: %v, want context.Canceled", err)
	}
}

func TestMemIsSafeForConcurrentUse(t *testing.T) {
	ctx := context.Background()
	var m Mem
	done := make(chan struct{})
	for i := range 8 {
		go func(i int) {
			defer func() { done <- struct{}{} }()
			for j := range 50 {
				m.Assert(ctx, "A", "r", string(rune('a'+j%26)))
				m.Out(ctx, "A")
				m.Match(ctx, "a", "", "")
			}
		}(i)
	}
	for range 8 {
		<-done
	}
	// Twenty-six objects, and the subject "A" folds onto the first of them,
	// so there are 26 nodes and 26 edges — one of which is the self-loop
	// a–r–a. Asserting that a thing relates to itself is still an assertion.
	if nodes, edges := m.Graph().Size(); nodes != 26 || edges != 26 {
		t.Errorf("size = %d nodes, %d edges; want 26, 26", nodes, edges)
	}
}

// The pipeline's output is the graph's input: what Run returns, Build takes.
func TestBuildTakesWhatAPipelineReturns(t *testing.T) {
	p := semantic.Pipeline{Ingest: source{}, Split: sentences{}, Extract: naive{}}
	ts, err := p.Run(context.Background(), "ref")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	g := Build(ts)
	if nodes, edges := g.Size(); nodes != 3 || edges != 2 {
		t.Fatalf("size = %d nodes, %d edges; want 3, 2", nodes, edges)
	}
	if n, _ := g.Node("ada"); !reflect.DeepEqual(n.Docs, []string{"d"}) {
		t.Errorf("docs = %v, want [d]: provenance must survive the fold", n.Docs)
	}
}

type source struct{}

func (source) Ingest(context.Context, string) ([]semantic.Doc, error) {
	return []semantic.Doc{{ID: "d", Text: "ada wrote note g. ada knew babbage."}}, nil
}

type sentences struct{}

func (sentences) Split(_ context.Context, d semantic.Doc) ([]semantic.Chunk, error) {
	return []semantic.Chunk{
		{DocID: d.ID, Index: 0, Text: "ada wrote note g"},
		{DocID: d.ID, Index: 1, Text: "ada knew babbage"},
	}, nil
}

// naive reads "subject predicate object" and nothing else.
type naive struct{}

func (naive) Extract(_ context.Context, c semantic.Chunk) ([]semantic.Triple, error) {
	switch c.Index {
	case 0:
		return []semantic.Triple{tri("ada", "wrote", "note g", 0.9, c.DocID)}, nil
	default:
		return []semantic.Triple{tri("ada", "knew", "babbage", 0.8, c.DocID)}, nil
	}
}

// --- helpers -------------------------------------------------------------

func ids(g *Graph) []string {
	var out []string
	for _, n := range g.Nodes() {
		out = append(out, n.ID)
	}
	return out
}

func close(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
