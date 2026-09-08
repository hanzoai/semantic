package agent

import (
	"reflect"
	"testing"
	"time"
)

func at(day int) time.Time {
	return time.Date(2026, 3, day, 12, 0, 0, 0, time.UTC)
}

func TestSpanHolds(t *testing.T) {
	for _, c := range []struct {
		name string
		span Span
		at   time.Time
		want bool
	}{
		{"open span holds always", Span{}, at(1), true},
		{"before it starts", Span{From: at(5)}, at(4), false},
		{"on the day it starts", Span{From: at(5)}, at(5), true},
		{"after it starts", Span{From: at(5)}, at(6), true},
		{"before it ends", Span{Until: at(5)}, at(4), true},
		{"after it ends", Span{Until: at(5)}, at(6), false},
		{"inside both bounds", Span{From: at(1), Until: at(5)}, at(3), true},
		{"outside both bounds", Span{From: at(1), Until: at(5)}, at(9), false},
	} {
		if got := c.span.Holds(c.at); got != c.want {
			t.Errorf("%s: Holds = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSpanCloseNeverWidens(t *testing.T) {
	open := Span{}.Close(at(5))
	if !open.Until.Equal(at(5)) {
		t.Errorf("closing an open span set Until to %v, want %v", open.Until, at(5))
	}
	early := Span{Until: at(2)}.Close(at(9))
	if !early.Until.Equal(at(2)) {
		t.Errorf("closing later moved Until to %v; a close may only shorten", early.Until)
	}
	later := Span{Until: at(9)}.Close(at(2))
	if !later.Until.Equal(at(2)) {
		t.Errorf("closing earlier left Until at %v, want %v", later.Until, at(2))
	}
}

func TestAddIsUpsert(t *testing.T) {
	var g Graph
	if !g.Add(Node{ID: "a", Kind: "entity", Text: "Ada"}) {
		t.Fatal("first Add reported the node was not new")
	}
	if g.Add(Node{ID: "a", Kind: "person", Text: "Ada Lovelace"}) {
		t.Error("second Add reported a new node; the id already existed")
	}
	n, _ := g.Node("a")
	if n.Text != "Ada Lovelace" || n.Kind != "person" {
		t.Errorf("node is %+v, want the second write's kind and text", n)
	}
	if got := g.Nodes("entity"); len(got) != 0 {
		t.Errorf("node still listed under its old kind: %+v", got)
	}
	if got := g.Nodes("person"); len(got) != 1 {
		t.Errorf("Nodes(person) = %d nodes, want 1", len(got))
	}
	if g.Add(Node{Kind: "entity"}) {
		t.Error("a node with no id was accepted")
	}
}

func TestJoinMakesEndsAndMerges(t *testing.T) {
	var g Graph
	if !g.Join(Edge{Link: Link{From: "a", To: "b", Label: "knows"}, Weight: 0.5}) {
		t.Fatal("first Join reported the edge was not new")
	}
	for _, id := range []string{"a", "b"} {
		n, ok := g.Node(id)
		if !ok {
			t.Fatalf("Join did not create the %s end", id)
		}
		if n.Kind != "entity" {
			t.Errorf("created end %s has kind %q, want entity", id, n.Kind)
		}
	}
	if g.Join(Edge{Link: Link{From: "a", To: "b", Label: "knows"}, Weight: 0.9}) {
		t.Error("the same assertion twice reported a new edge")
	}
	e, _ := g.Edge(Link{From: "a", To: "b", Label: "knows"})
	if e.Weight != 0.9 {
		t.Errorf("weight is %v, want the later assertion's 0.9", e.Weight)
	}
	if n := len(g.Edges("")); n != 1 {
		t.Errorf("graph holds %d edges, want 1", n)
	}
	if e := (Edge{Link: Link{From: "a", To: "c"}}); !g.Join(e) {
		t.Error("an edge with no label was refused")
	}
	if e, _ := g.Edge(Link{From: "a", To: "c", Label: About}); e.Weight != 1 {
		t.Errorf("default weight is %v, want 1", e.Weight)
	}
}

// At is the port of ContextGraph.state_at: only what was true then, and no
// edge to something that was not.
func TestAtIsTheGraphAsItStood(t *testing.T) {
	var g Graph
	g.Add(Node{ID: "a", Text: "always"})
	g.Add(Node{ID: "b", Text: "later", Span: Span{From: at(5)}})
	g.Add(Node{ID: "c", Text: "gone", Span: Span{Until: at(2)}})
	g.Join(Edge{Link: Link{From: "a", To: "b", Label: "knows"}})
	g.Join(Edge{Link: Link{From: "a", To: "c", Label: "knew"}})

	nodes, edges := g.At(at(3))
	if got := ids(nodes); !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("nodes at day 3 are %v, want [a]: b is not yet true and c is over", got)
	}
	if len(edges) != 0 {
		t.Errorf("edges at day 3 are %v, want none: both ends must be live", edges)
	}
	nodes, edges = g.At(at(6))
	if got := ids(nodes); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("nodes at day 6 are %v, want [a b]", got)
	}
	if len(edges) != 1 || edges[0].To != "b" {
		t.Errorf("edges at day 6 are %v, want the one to b", edges)
	}
}

func TestRetractClosesAndCascades(t *testing.T) {
	var g Graph
	g.Add(Node{ID: "a", Text: "account"})
	g.Add(Node{ID: "b", Text: "owner"})
	g.Join(Edge{Link: Link{From: "a", To: "b", Label: "held_by"}})

	if !g.Retract("a", "closed", at(5)) {
		t.Fatal("Retract reported nothing to retract")
	}
	if g.Retract("a", "again", at(6)) {
		t.Error("retracting twice reported a second retraction")
	}
	if _, ok := g.Node("a"); !ok {
		t.Error("retraction removed the node; it must stay readable")
	}
	nodes, edges := g.At(at(4))
	if len(nodes) != 2 || len(edges) != 1 {
		t.Errorf("before the retraction the graph is %d nodes and %d edges, want 2 and 1", len(nodes), len(edges))
	}
	nodes, _ = g.At(at(6))
	if got := ids(nodes); !reflect.DeepEqual(got, []string{"b"}) {
		t.Errorf("after the retraction the nodes are %v, want [b]", got)
	}
	e, _ := g.Edge(Link{From: "a", To: "b", Label: "held_by"})
	if !e.Span.Until.Equal(at(5)) {
		t.Errorf("the edge was left open at %v; a retraction cascades", e.Span.Until)
	}
	loss, ok := g.Lost("a")
	if !ok || loss.Why != "closed" || loss.Purged {
		t.Errorf("loss is %+v, want a retraction with the reason kept", loss)
	}
	if n := len(g.Losses()); n != 2 {
		t.Errorf("%d losses recorded, want 2: the node and the edge it reached", n)
	}
}

func TestRetractKeepsAnEarlierEnd(t *testing.T) {
	var g Graph
	g.Add(Node{ID: "a", Span: Span{Until: at(2)}})
	g.Retract("a", "late", at(9))
	n, _ := g.Node("a")
	if !n.Span.Until.Equal(at(2)) {
		t.Errorf("Until moved to %v; retraction may only shorten a window", n.Span.Until)
	}
}

func TestPurgeLeavesOnlyTheNote(t *testing.T) {
	var g Graph
	g.Add(Node{ID: "a", Text: "personal"})
	g.Join(Edge{Link: Link{From: "a", To: "b", Label: "about"}})
	if !g.Purge("a", "erasure", at(5)) {
		t.Fatal("Purge reported nothing to purge")
	}
	if _, ok := g.Node("a"); ok {
		t.Error("the purged node is still readable")
	}
	if n := len(g.Edges("")); n != 0 {
		t.Errorf("%d edges survived the purge, want 0", n)
	}
	if nodes, _ := g.At(at(1)); len(nodes) != 1 || nodes[0].ID != "b" {
		t.Errorf("the purged node is still in the past: %v", ids(nodes))
	}
	loss, ok := g.Lost("a")
	if !ok || !loss.Purged || loss.Why != "erasure" {
		t.Errorf("loss is %+v, want a purge with its reason", loss)
	}
}

func TestCutAndDropOneEdge(t *testing.T) {
	var g Graph
	l := Link{From: "a", To: "b", Label: "knows"}
	g.Join(Edge{Link: l})
	if !g.Cut(l, "wrong", at(5)) {
		t.Fatal("Cut reported nothing to cut")
	}
	if g.Cut(l, "wrong", at(6)) {
		t.Error("cutting twice reported a second cut")
	}
	e, _ := g.Edge(l)
	if !e.Span.Until.Equal(at(5)) {
		t.Errorf("Until is %v, want the moment of the cut", e.Span.Until)
	}
	if _, ok := g.Node("a"); !ok {
		t.Error("cutting an edge removed an end")
	}
	if !g.Drop(l, "gone", at(6)) {
		t.Fatal("Drop reported nothing to drop")
	}
	if _, ok := g.Edge(l); ok {
		t.Error("the dropped edge is still readable")
	}
	if g.Drop(l, "gone", at(7)) {
		t.Error("dropping twice reported a second drop")
	}
}

func TestStats(t *testing.T) {
	var g Graph
	g.Add(Node{ID: "a", Kind: "person"})
	g.Add(Node{ID: "b", Kind: "person"})
	g.Add(Node{ID: "c", Kind: "place"})
	g.Join(Edge{Link: Link{From: "a", To: "b", Label: "knows"}})
	g.Join(Edge{Link: Link{From: "a", To: "c", Label: "lives"}})

	s := g.Stats()
	if s.Nodes != 3 || s.Edges != 2 {
		t.Errorf("counted %d nodes and %d edges, want 3 and 2", s.Nodes, s.Edges)
	}
	if s.Kinds["person"] != 2 || s.Kinds["place"] != 1 {
		t.Errorf("kinds are %v, want two people and one place", s.Kinds)
	}
	if want := 2.0 / 6.0; s.Density != want {
		t.Errorf("density is %v, want %v (edges over ordered pairs)", s.Density, want)
	}
	if (&Graph{}).Stats().Density != 0 {
		t.Error("an empty graph has a density other than 0")
	}
}

func TestFindScoresByShareOfTheQuestion(t *testing.T) {
	var g Graph
	g.Add(Node{ID: "a", Text: "mortgage approval for a first time buyer"})
	g.Add(Node{ID: "b", Text: "mortgage rates"})
	g.Add(Node{ID: "c", Text: "unrelated"})

	hits := g.Find("mortgage approval")
	if len(hits) != 2 {
		t.Fatalf("found %d nodes, want the two that carry a query word", len(hits))
	}
	if hits[0].Node.ID != "a" || hits[0].Score != 1 {
		t.Errorf("best hit is %s at %v, want a at 1", hits[0].Node.ID, hits[0].Score)
	}
	if hits[1].Node.ID != "b" || hits[1].Score != 0.5 {
		t.Errorf("second hit is %s at %v, want b at 0.5", hits[1].Node.ID, hits[1].Score)
	}
	if got := g.Find("   "); got != nil {
		t.Errorf("an empty question found %v, want nothing", got)
	}
}

func TestWatchSeesEveryChange(t *testing.T) {
	var g Graph
	var seen []Change
	g.Watch = func(c Change) { seen = append(seen, c) }

	g.Add(Node{ID: "a"})
	g.Join(Edge{Link: Link{From: "a", To: "b", Label: "knows"}})
	g.Retract("a", "", at(5))
	g.Purge("b", "", at(6))

	knows := Link{From: "a", To: "b", Label: "knows"}
	want := []Change{
		{Op: OpAdd, Node: "a"},
		{Op: OpAdd, Node: "b"}, // the join creates the end it needs
		{Op: OpAdd, Edge: knows},
		{Op: OpClose, Node: "a"},
		{Op: OpClose, Edge: knows}, // retracting a node cascades to its edges
		{Op: OpPurge, Node: "b"},
		{Op: OpPurge, Edge: knows}, // a closed edge is still a record to remove
	}
	if !reflect.DeepEqual(seen, want) {
		t.Errorf("watched\n%+v\nwant\n%+v", seen, want)
	}
}

func ids(nodes []Node) []string {
	var out []string
	for _, n := range nodes {
		out = append(out, n.ID)
	}
	return out
}
