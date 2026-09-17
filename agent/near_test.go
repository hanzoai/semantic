package agent

import (
	"math"
	"reflect"
	"testing"
)

func almost(t *testing.T, what string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s is %v, want %v", what, got, want)
	}
}

func TestApart(t *testing.T) {
	for _, c := range []struct {
		hops int
		want Band
	}{
		{0, BandDirect}, {1, BandDirect},
		{2, BandNear}, {3, BandNear},
		{4, BandMid}, {6, BandMid},
		{7, BandFar}, {20, BandFar},
	} {
		if got := Apart(c.hops); got != c.want {
			t.Errorf("Apart(%d) = %q, want %q", c.hops, got, c.want)
		}
	}
}

// The port of test_get_neighbor_distances_tracks_path_decay_and_band: a walk
// reports how far it went, what survived the trip, and the way it came.
func TestNearTracksDecayAndBand(t *testing.T) {
	var g Graph
	g.Add(Node{ID: "A", Kind: "entity", Text: "anchor"})
	g.Add(Node{ID: "B", Kind: "entity", Text: "bridge"})
	g.Add(Node{ID: "C", Kind: "decision", Text: "decision"})
	g.Join(Edge{From: "A", To: "B", Label: Influenced, Weight: 0.9})
	g.Join(Edge{From: "B", To: "C", Label: Influenced, Weight: 0.7})

	steps := g.Near("A", Reach{Hops: 2, Floor: 0.5})
	if len(steps) != 2 {
		t.Fatalf("reached %d nodes, want B and C", len(steps))
	}
	if steps[0].Node.ID != "B" || steps[0].Hop != 1 {
		t.Errorf("first step is %s at hop %d, want B at 1", steps[0].Node.ID, steps[0].Hop)
	}
	c := steps[1]
	if c.Node.ID != "C" || c.Hop != 2 {
		t.Fatalf("second step is %s at hop %d, want C at 2", c.Node.ID, c.Hop)
	}
	almost(t, "decay to C", c.Decay, 0.9*0.7)
	if c.Band != BandNear {
		t.Errorf("band is %q, want %q", c.Band, BandNear)
	}
	if want := []string{"A", "B", "C"}; !reflect.DeepEqual(c.Path, want) {
		t.Errorf("path is %v, want %v", c.Path, want)
	}
	if c.Label != Influenced || c.Weight != 0.7 {
		t.Errorf("arrived by %q at weight %v, want %q at 0.7", c.Label, c.Weight, Influenced)
	}
}

func TestNearFloorAndDepth(t *testing.T) {
	var g Graph
	g.Join(Edge{From: "A", To: "B", Label: "x", Weight: 0.5})
	g.Join(Edge{From: "B", To: "C", Label: "x", Weight: 0.5})

	if got := g.Near("A", Reach{}); len(got) != 1 || got[0].Node.ID != "B" {
		t.Errorf("the zero Reach walked to %v, want one hop", steps(got))
	}
	if got := g.Near("A", Reach{Hops: 2}); len(got) != 2 {
		t.Errorf("two hops reached %v, want B and C", steps(got))
	}
	if got := g.Near("A", Reach{Hops: 2, Floor: 0.3}); len(got) != 1 {
		t.Errorf("a floor of 0.3 kept %v, want only B at 0.5", steps(got))
	}
	if got := g.Near("nobody", Reach{}); got != nil {
		t.Errorf("walking from a node that is not there gave %v", steps(got))
	}
}

func TestNearFollowsOnlyWhatItIsAsked(t *testing.T) {
	var g Graph
	g.Join(Edge{From: "A", To: "B", Label: Caused, Weight: 1})
	g.Join(Edge{From: "A", To: "C", Label: About, Weight: 1})
	g.Join(Edge{From: "A", To: "D", Label: Caused, Weight: 0.2})

	got := g.Near("A", Reach{Labels: []string{Caused}})
	if !reflect.DeepEqual(steps(got), []string{"B", "D"}) {
		t.Errorf("following %q reached %v, want B and D", Caused, steps(got))
	}
	got = g.Near("A", Reach{Labels: []string{Caused}, Min: 0.5})
	if !reflect.DeepEqual(steps(got), []string{"B"}) {
		t.Errorf("a minimum weight of 0.5 reached %v, want B: the edge to D is weaker", steps(got))
	}
}

func TestNearRespectsTime(t *testing.T) {
	var g Graph
	g.Add(Node{ID: "A"})
	g.Add(Node{ID: "B"})
	g.Add(Node{ID: "C", Span: Span{From: at(9)}})
	g.Join(Edge{From: "A", To: "B", Label: "x", Span: Span{Until: at(2)}})
	g.Join(Edge{From: "A", To: "C", Label: "x"})

	if got := g.Near("A", Reach{At: at(1)}); !reflect.DeepEqual(steps(got), []string{"B"}) {
		t.Errorf("at day 1 the walk reached %v, want B: C is not true yet", steps(got))
	}
	if got := g.Near("A", Reach{At: at(10)}); !reflect.DeepEqual(steps(got), []string{"C"}) {
		t.Errorf("at day 10 the walk reached %v, want C: the edge to B has closed", steps(got))
	}
}

func TestPath(t *testing.T) {
	var g Graph
	g.Join(Edge{From: "A", To: "B", Label: "x", Weight: 0.5})
	g.Join(Edge{From: "B", To: "C", Label: "x", Weight: 0.5})

	r := g.Path("A", "C", Reach{})
	if !r.Found {
		t.Fatal("no path from A to C")
	}
	if want := []string{"A", "B", "C"}; !reflect.DeepEqual(r.IDs(), want) {
		t.Errorf("path is %v, want %v", r.IDs(), want)
	}
	if r.Hops != 2 || r.Band != BandNear {
		t.Errorf("path is %d hops in band %q, want 2 and %q", r.Hops, r.Band, BandNear)
	}
	almost(t, "decay", r.Decay, 0.25)
	if r.Doors != 0 {
		t.Errorf("a path inside one graph used %d doors", r.Doors)
	}
	if g.Path("C", "A", Reach{}).Found {
		t.Error("found a path against the direction of every edge")
	}
	if g.Path("A", "nobody", Reach{}).Found {
		t.Error("found a path to a node that is not there")
	}
}

// The port of test_cross_graph_path_traverses_link_boundary: two graphs stay
// separate and are still reachable from each other.
func TestCrossGoesThroughTheDoor(t *testing.T) {
	left, right := &Graph{}, &Graph{}
	left.Add(Node{ID: "A", Text: "left"})
	right.Add(Node{ID: "B", Text: "right"})
	left.Door("A", right, "B")

	r := left.Cross("A", right, "B", Reach{})
	if !r.Found {
		t.Fatal("the door did not lead anywhere")
	}
	if r.Hops != 1 || r.Doors != 1 || r.Band != BandDirect {
		t.Errorf("crossed in %d hops through %d doors in band %q, want 1, 1, %q", r.Hops, r.Doors, r.Band, BandDirect)
	}
	want := []Where{{Graph: left, ID: "A"}, {Graph: right, ID: "B"}}
	if !reflect.DeepEqual(r.Path, want) {
		t.Errorf("path is %v, want A here then B there", r.IDs())
	}
	if n := len(right.Nodes("")); n != 1 {
		t.Errorf("the other graph now holds %d nodes; a door must not merge them", n)
	}
}

func steps(ss []Step) []string {
	var out []string
	for _, s := range ss {
		out = append(out, s.Node.ID)
	}
	return out
}
