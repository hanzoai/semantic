package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// chain builds the graph the Python causal tests use: d1 caused d2, which
// caused d3.
func chain(t *testing.T) *Journal {
	t.Helper()
	ctx := context.Background()
	j := &Journal{}
	for i, id := range []string{"d1", "d2", "d3"} {
		d := loan(id, "step "+id, "approved", 0.9-float64(i)*0.1)
		if _, err := j.Write(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	for _, l := range [][2]string{{"d1", "d2"}, {"d2", "d3"}} {
		if err := j.Lead(l[0], l[1], Caused); err != nil {
			t.Fatal(err)
		}
	}
	return j
}

func TestUpAndDown(t *testing.T) {
	c := &Cause{G: chain(t).G}

	up := c.Up("d3", 0)
	if want := []string{"d1", "d2"}; !reflect.DeepEqual(awayIDs(up), want) {
		t.Errorf("what led to d3 is %v, want %v: the farthest cause reads first", awayIDs(up), want)
	}
	if up[0].Hops != 2 || up[1].Hops != 1 {
		t.Errorf("hops are %d and %d, want 2 then 1", up[0].Hops, up[1].Hops)
	}
	down := c.Down("d1", 0)
	if want := []string{"d2", "d3"}; !reflect.DeepEqual(awayIDs(down), want) {
		t.Errorf("what d1 led to is %v, want %v: the nearest consequence reads first", awayIDs(down), want)
	}
	if got := c.Up("d1", 0); got != nil {
		t.Errorf("d1 has causes %v, want none", awayIDs(got))
	}
	if got := c.Down("nobody", 0); got != nil {
		t.Errorf("walking from a decision that is not there gave %v", awayIDs(got))
	}
}

func TestDepthBoundsTheWalk(t *testing.T) {
	c := &Cause{G: chain(t).G}
	if got := c.Up("d3", 1); !reflect.DeepEqual(awayIDs(got), []string{"d2"}) {
		t.Errorf("one step back from d3 reached %v, want d2 alone", awayIDs(got))
	}
	if got := c.Down("d1", 1); !reflect.DeepEqual(awayIDs(got), []string{"d2"}) {
		t.Errorf("one step on from d1 reached %v, want d2 alone", awayIDs(got))
	}
}

func TestCausalityIgnoresEvidence(t *testing.T) {
	ctx := context.Background()
	j := &Journal{}
	j.Write(ctx, loan("d1", "first", "approved", 0.9, "house"))
	j.Write(ctx, loan("d2", "second", "approved", 0.9, "house"))
	c := &Cause{G: j.G}
	if got := c.Up("d2", 0); got != nil {
		t.Errorf("sharing evidence made %v read as a cause; only a recorded link is causal", awayIDs(got))
	}
}

func TestChainIsTheInterfaceOntoUp(t *testing.T) {
	ctx := context.Background()
	c := &Cause{G: chain(t).G}
	got, err := c.Chain(ctx, "d3")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"d1", "d2"}; !reflect.DeepEqual(recordIDs(got), want) {
		t.Errorf("the chain is %v, want %v", recordIDs(got), want)
	}
	if _, err := c.Chain(ctx, "nobody"); !errors.Is(err, ErrNoDecision) {
		t.Errorf("chaining a decision that is not there gave %v, want ErrNoDecision", err)
	}
}

func TestRoots(t *testing.T) {
	ctx := context.Background()
	j := chain(t)
	// A second root, one hop from d3.
	j.Write(ctx, loan("d4", "another beginning", "approved", 0.8))
	j.Lead("d4", "d3", Influenced)

	c := &Cause{G: j.G}
	got := c.Roots("d3", 0)
	if want := []string{"d4", "d1"}; !reflect.DeepEqual(awayIDs(got), want) {
		t.Errorf("the roots of d3 are %v, want %v: nearest first", awayIDs(got), want)
	}
	if got[0].Hops != 1 || got[1].Hops != 2 {
		t.Errorf("roots are %d and %d hops away, want 1 then 2", got[0].Hops, got[1].Hops)
	}
}

// The port of test_causal_loop_detection.
func TestLoops(t *testing.T) {
	j := chain(t)
	if got := (&Cause{G: j.G}).Loops(0); got != nil {
		t.Fatalf("a straight chain has loops %v, want none", got)
	}
	if err := j.Lead("d3", "d1", Influenced); err != nil {
		t.Fatal(err)
	}
	got := (&Cause{G: j.G}).Loops(0)
	if len(got) != 1 {
		t.Fatalf("found %v, want one loop", got)
	}
	if len(got[0]) != 4 {
		t.Errorf("the loop is %v, want three decisions and the return to the first", got[0])
	}
	if got[0][0] != got[0][len(got[0])-1] {
		t.Errorf("the loop %v does not come back to where it started", got[0])
	}
}

// Two cycles can run through the same three decisions in opposite orders, and
// they are two cycles, not one.
func TestLoopsTellsCyclesApart(t *testing.T) {
	ctx := context.Background()
	j := &Journal{}
	for _, id := range []string{"d1", "d2", "d3"} {
		if _, err := j.Write(ctx, loan(id, "step "+id, "approved", 0.9)); err != nil {
			t.Fatal(err)
		}
	}
	for _, l := range [][2]string{
		{"d1", "d2"}, {"d2", "d3"}, {"d3", "d1"}, // round one way
		{"d1", "d3"}, {"d3", "d2"}, {"d2", "d1"}, // and round the other
	} {
		if err := j.Lead(l[0], l[1], Caused); err != nil {
			t.Fatal(err)
		}
	}

	got := (&Cause{G: j.G}).Loops(0)
	long, short := 0, 0
	for _, c := range got {
		switch len(c) {
		case 4:
			long++
		case 3:
			short++
		}
	}
	if long != 2 {
		t.Errorf("found %d loops through all three, want both ways round: %v", long, got)
	}
	if short != 3 {
		t.Errorf("found %d loops between two, want three: %v", short, got)
	}
	if len(got) != 5 {
		t.Errorf("found %d loops in all, want 5: %v", len(got), got)
	}
	if len(got[0]) > len(got[len(got)-1]) {
		t.Errorf("loops are ordered %v, want the shortest first", got)
	}
}

func TestTraceReadsTheDistance(t *testing.T) {
	ctx := context.Background()
	j := &Journal{}
	for _, id := range []string{"d1", "d2", "d3"} {
		j.Write(ctx, loan(id, "step "+id, "approved", 0.9))
	}
	j.G.Join(Edge{From: "d1", To: "d2", Label: Caused, Weight: 0.9})
	j.G.Join(Edge{From: "d2", To: "d3", Label: Influenced, Weight: 0.5})
	c := &Cause{G: j.G}

	tr := c.Trace("d1", "d3")
	if !tr.Found {
		t.Fatal("no causal path from d1 to d3")
	}
	if want := []string{"d1", "d2", "d3"}; !reflect.DeepEqual(tr.Path, want) {
		t.Errorf("path is %v, want %v", tr.Path, want)
	}
	if tr.Hops != 2 || tr.Band != BandNear {
		t.Errorf("%d hops in band %q, want 2 and %q", tr.Hops, tr.Band, BandNear)
	}
	almost(t, "the confidence that survives", tr.Decay, 0.45)
	if want := (Link{From: "d2", To: "d3", Label: Influenced}); tr.Weakest != want {
		t.Errorf("the weakest link is %+v, want %+v", tr.Weakest, want)
	}
	if tr.Weight != 0.5 {
		t.Errorf("the weakest weight is %v, want 0.5", tr.Weight)
	}
	if !reflect.DeepEqual(tr.Between, []string{"d2"}) {
		t.Errorf("the path passed through %v, want d2", tr.Between)
	}
	if !strings.Contains(tr.Gloss, "mediated by 1 decision") || !strings.Contains(tr.Gloss, "moderate") {
		t.Errorf("the gloss reads %q, want it to name the mediation and the strength", tr.Gloss)
	}

	direct := c.Trace("d1", "d2")
	if direct.Band != BandDirect || !strings.HasPrefix(direct.Gloss, "direct cause") {
		t.Errorf("one hop reads as %q in band %q, want a direct cause", direct.Gloss, direct.Band)
	}
	if back := c.Trace("d3", "d1"); back.Found || back.Gloss != "no causal path" {
		t.Errorf("a path was found against the direction of causality: %+v", back)
	}
}

func TestGlossFalls(t *testing.T) {
	for _, c := range []struct {
		hops  int
		decay float64
		want  string
	}{
		{1, 0.9, "direct cause, confidence 0.90"},
		{2, 0.63, "mediated by 1 decision, confidence 0.63, moderate evidence"},
		{3, 0.2, "mediated by 2 decisions, confidence 0.20, weak evidence"},
		{5, 0.1, "distal, 5 causal steps, confidence 0.10, weak evidence"},
	} {
		if got := gloss(c.hops, c.decay, Apart(c.hops)); got != c.want {
			t.Errorf("gloss(%d, %v) = %q, want %q", c.hops, c.decay, got, c.want)
		}
	}
}

// The port of _calculate_influence_strength: strong and close beats weak and
// far, and the number falls with distance.
func TestStrength(t *testing.T) {
	strong := Strength(Caused, 0.9, 1)
	if strong <= 0.8 {
		t.Errorf("a sure cause one step away scored %v, want more than 0.8", strong)
	}
	far := Strength(Influenced, 0.7, 5)
	if far >= 0.7 {
		t.Errorf("a weaker link five steps away scored %v, want less than 0.7", far)
	}
	for _, c := range []struct {
		label string
		want  float64
	}{
		{Caused, 1}, {Influenced, 0.8}, {Precedes, 0.6},
	} {
		if got := Strength(c.label, 1, 0); got != c.want {
			t.Errorf("Strength(%q, 1, 0) = %v, want %v", c.label, got, c.want)
		}
	}
	if a, b := Strength(Caused, 1, 1), Strength(Caused, 1, 2); a <= b {
		t.Errorf("one hop scored %v and two scored %v; distance must cost something", a, b)
	}
}

// The port of _calculate_precedent_strength.
func TestFit(t *testing.T) {
	if got := Fit(0.9, true, true); got <= 0.8 {
		t.Errorf("a close precedent on the same question with the same answer scored %v, want more than 0.8", got)
	}
	if got := Fit(0.3, false, false); got >= 0.5 {
		t.Errorf("a distant precedent on another question scored %v, want less than 0.5", got)
	}
	if got := Fit(1, true, true); got != 1 {
		t.Errorf("Fit(1, true, true) = %v, want 1: a precedent cannot be more than perfect", got)
	}
	almost(t, "same question, different answer", Fit(0.5, true, false), 0.5*1.1*0.8)
}

func TestForce(t *testing.T) {
	ctx := context.Background()
	j := chain(t)
	c := &Cause{G: j.G}

	moved := c.Force("d1")
	if moved <= 0 || moved > 1 {
		t.Errorf("d1 caused two decisions and scored %v, want a number in 0 to 1", moved)
	}
	if got := c.Force("d3"); got != 0 {
		t.Errorf("d3 caused nothing and scored %v, want 0", got)
	}
	// A precedent counts, at half.
	j.Write(ctx, loan("d4", "later", "approved", 0.8))
	j.Lead("d3", "d4", Precedes)
	if cited := c.Force("d4"); cited <= 0 {
		t.Errorf("d4 followed a precedent and scored %v, want more than 0", cited)
	}
	if got := c.Force("nobody"); got != 0 {
		t.Errorf("a decision that is not there scored %v, want 0", got)
	}
}

// The port of _calculate_network_metrics, _calculate_centrality_scores and
// _identify_communities, which the Python analyzer keeps separate and which
// are one reading of one graph.
func TestNet(t *testing.T) {
	ctx := context.Background()
	j := chain(t)
	// A fourth decision, tied to nothing.
	j.Write(ctx, loan("d4", "apart", "approved", 0.5))
	n := (&Cause{G: j.G}).Net(nil)

	if len(n.Decisions) != 4 || n.Edges != 2 {
		t.Fatalf("the network holds %d decisions and %d edges, want 4 and 2", len(n.Decisions), n.Edges)
	}
	almost(t, "density", n.Density, 2.0/12.0)
	almost(t, "the rough mean distance", n.Path, 4)
	if n.Rank["d2"] != 1 {
		t.Errorf("d2 sits between the others and ranks %v, want 1", n.Rank["d2"])
	}
	if n.Rank["d4"] != 0 {
		t.Errorf("d4 touches nothing and ranks %v, want 0", n.Rank["d4"])
	}
	if len(n.Parts) != 2 {
		t.Fatalf("the network splits into %v, want the chain and the one apart", n.Parts)
	}
	sizes := []int{len(n.Parts[0]), len(n.Parts[1])}
	if sizes[0]+sizes[1] != 4 || (sizes[0] != 1 && sizes[1] != 1) {
		t.Errorf("the parts are %v, want three together and one alone", n.Parts)
	}
	if got := (&Cause{}).Net(nil); len(got.Decisions) != 0 || got.Density != 0 {
		t.Errorf("an empty graph gave %+v", got)
	}
}

func awayIDs(as []Away) []string {
	var out []string
	for _, a := range as {
		out = append(out, a.Decision.ID)
	}
	return out
}
