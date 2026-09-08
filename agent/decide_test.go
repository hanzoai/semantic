package agent

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/hanzoai/semantic/decision"
)

// The interfaces the root module keeps, and this package fills in.
var (
	_ decision.Recorder = (*Journal)(nil)
	_ decision.Cause    = (*Cause)(nil)
	_ decision.Policy   = (*Rules)(nil)
)

func loan(id, scenario, outcome string, sure float64, about ...string) Decision {
	d := Decision{
		Topic:      "loan",
		Case:       scenario,
		Reason:     "the file supports it",
		Confidence: sure,
	}
	d.ID, d.Choice, d.Agent, d.At, d.Because = id, outcome, "officer", at(1), about
	return d
}

func TestWriteWantsTheEssentials(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name string
		in   Decision
	}{
		{"no topic", Decision{Case: "c", Record: decision.Record{Choice: "approved"}}},
		{"no case", Decision{Topic: "loan", Record: decision.Record{Choice: "approved"}}},
		{"no choice", Decision{Topic: "loan", Case: "c"}},
		{"confidence above one", Decision{Topic: "loan", Case: "c", Confidence: 1.5, Record: decision.Record{Choice: "approved"}}},
		{"confidence below zero", Decision{Topic: "loan", Case: "c", Confidence: -0.1, Record: decision.Record{Choice: "approved"}}},
	} {
		if _, err := (&Journal{}).Write(ctx, c.in); err == nil {
			t.Errorf("%s: Write accepted it", c.name)
		}
	}
	if _, err := (&Journal{}).Write(ctx, loan("", "a case", "approved", 1)); err != nil {
		t.Errorf("a decision with everything it needs was refused: %v", err)
	}
}

func TestWriteAndReadBack(t *testing.T) {
	j := &Journal{}
	in := loan("d1", "mortgage for a first time buyer", "approved", 0.95, "buyer", "house")
	in.Props = map[string]any{"branch": "north"}

	out, err := j.Write(context.Background(), in)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if out.ID != "d1" || out.Topic != "loan" || out.Case != in.Case || out.Choice != "approved" {
		t.Errorf("read back %+v, want what was written", out)
	}
	if out.Confidence != 0.95 || out.Agent != "officer" || !out.At.Equal(at(1)) {
		t.Errorf("read back %+v, want the confidence, agent and time written", out)
	}
	if !reflect.DeepEqual(out.Because, []string{"buyer", "house"}) {
		t.Errorf("evidence is %v, want what it rested on, in order", out.Because)
	}
	if out.Props["branch"] != "north" {
		t.Errorf("properties are %v, want the caller's branch kept", out.Props)
	}
	if _, ok := out.Props[keyTopic]; ok {
		t.Errorf("properties are %v; what the fields already carry must not be repeated there", out.Props)
	}
	// The evidence is edges, not a list beside the graph.
	for _, e := range []string{"buyer", "house"} {
		if _, ok := j.G.Edge(Link{From: "d1", To: e, Label: About}); !ok {
			t.Errorf("no edge from the decision to %s", e)
		}
		if n, _ := j.G.Node(e); n.Kind != kindEntity {
			t.Errorf("%s was written as a %q, want an entity", e, n.Kind)
		}
	}
	if _, ok := j.Read("nobody"); ok {
		t.Error("read a decision that was never written")
	}
}

func TestWriteFillsAnIDAndTime(t *testing.T) {
	j := &Journal{}
	first, err := j.Write(context.Background(), loan("", "a case", "approved", 0.5))
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == "" {
		t.Fatal("no id was given to the decision")
	}
	second, _ := j.Write(context.Background(), loan("", "another", "refused", 0.5))
	if second.ID == first.ID {
		t.Error("two decisions were given the same id")
	}
	bare := Decision{Topic: "loan", Case: "c"}
	bare.Choice = "approved"
	out, _ := j.Write(context.Background(), bare)
	if out.At.IsZero() {
		t.Error("a decision was written with no time")
	}
}

func TestByOnAndAbout(t *testing.T) {
	ctx := context.Background()
	j := &Journal{}
	a := loan("d1", "first", "approved", 0.9, "ada")
	b := loan("d2", "second", "refused", 0.4, "ada", "bob")
	c := loan("d3", "third", "approved", 0.7, "bob")
	c.Agent = "manager"
	c.Topic = "credit"
	for _, d := range []Decision{a, b, c} {
		if _, err := j.Write(ctx, d); err != nil {
			t.Fatal(err)
		}
	}

	got, err := j.By(ctx, "officer")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"d1", "d2"}; !reflect.DeepEqual(recordIDs(got), want) {
		t.Errorf("the officer decided %v, want %v", recordIDs(got), want)
	}
	if got := decisionIDs(j.On("loan")); !reflect.DeepEqual(got, []string{"d1", "d2"}) {
		t.Errorf("on loan: %v, want d1 and d2", got)
	}
	if got := decisionIDs(j.About("ada")); !reflect.DeepEqual(got, []string{"d1", "d2"}) {
		t.Errorf("about ada: %v, want d1 and d2", got)
	}
	if got := decisionIDs(j.About("bob")); !reflect.DeepEqual(got, []string{"d2", "d3"}) {
		t.Errorf("about bob: %v, want d2 and d3", got)
	}
	if n := len(j.All()); n != 3 {
		t.Errorf("%d decisions in all, want 3", n)
	}
}

// Record is the interface's way in and must land in the same place as Write.
func TestRecordIsWriteWithLessSaid(t *testing.T) {
	ctx := context.Background()
	j := &Journal{}
	r := decision.Record{ID: "d1", Agent: "ada", Choice: "hold", Because: []string{"e1"}, Rule: "p1", At: at(2)}
	if err := j.Record(ctx, r); err != nil {
		t.Fatalf("Record: %v", err)
	}
	back, ok := j.Read("d1")
	if !ok {
		t.Fatal("the record was not written")
	}
	if !reflect.DeepEqual(back.Record, r) {
		t.Errorf("read back %+v, want %+v", back.Record, r)
	}
}

func TestLeadOnlyJoinsDecisions(t *testing.T) {
	ctx := context.Background()
	j := &Journal{}
	j.Write(ctx, loan("d1", "first", "approved", 0.9))
	j.Write(ctx, loan("d2", "second", "approved", 0.9, "house"))

	if err := j.Lead("d1", "d2", "vaguely related to"); err == nil {
		t.Error("a label that is not causal was accepted")
	}
	if err := j.Lead("d1", "nobody", Caused); !errors.Is(err, ErrNoDecision) {
		t.Errorf("joining to a decision that is not there gave %v, want ErrNoDecision", err)
	}
	if err := j.Lead("d1", "house", Caused); err == nil {
		t.Error("a causal link to an entity was accepted; that is evidence, not causality")
	}
	if err := j.Lead("d1", "d2", Precedes); err != nil {
		t.Fatalf("Lead: %v", err)
	}
	if got := decisionIDs(j.Before("d2")); !reflect.DeepEqual(got, []string{"d1"}) {
		t.Errorf("the precedents of d2 are %v, want d1", got)
	}
	if got := j.Before("d1"); got != nil {
		t.Errorf("d1 has precedents %v, want none", decisionIDs(got))
	}
}

func TestLikeFindsPrecedents(t *testing.T) {
	ctx := context.Background()
	j := &Journal{}
	j.Write(ctx, loan("d1", "mortgage for a first time buyer", "approved", 0.9, "ada"))
	j.Write(ctx, loan("d2", "mortgage for a second home", "refused", 0.6, "bob"))
	other := loan("d3", "mortgage for a first time buyer", "approved", 0.8, "ada")
	other.Topic = "credit"
	j.Write(ctx, other)

	got := j.Like(Ask{Case: "mortgage for a first time buyer", Topic: "loan"})
	if want := []string{"d1", "d2"}; !reflect.DeepEqual(matchIDs(got), want) {
		t.Fatalf("precedents are %v, want %v: d3 is another topic", matchIDs(got), want)
	}
	if got[0].Words <= got[1].Words {
		t.Errorf("d1 shares every word and d2 does not, but scored %v against %v", got[0].Words, got[1].Words)
	}
	if s := got[0].Score; s != byWords*got[0].Words+byShape*got[0].Shape {
		t.Errorf("the score %v is not the two parts weighted", s)
	}

	if got := j.Like(Ask{Case: "mortgage for a first time buyer", Floor: 0.6}); len(got) != 2 {
		t.Errorf("a floor of 0.6 kept %v, want the two whose words match", matchIDs(got))
	}
	if got := j.Like(Ask{Case: "mortgage", Of: []string{"ada"}}); !reflect.DeepEqual(matchIDs(got), []string{"d1", "d3"}) {
		t.Errorf("narrowed to ada: %v, want d1 and d3", matchIDs(got))
	}
	if got := j.Like(Ask{Case: "mortgage", N: 1}); len(got) != 1 {
		t.Errorf("asked for one, got %d", len(got))
	}
	if got := j.Like(Ask{}); got != nil {
		t.Errorf("asking with no case gave %v", matchIDs(got))
	}
}

// The port of test_find_precedents_by_scenario_filters_superseded_as_of and
// its sibling: a decision that has been superseded is not a precedent unless
// you ask for it, or ask as of a time when it still held.
func TestLikeAndSupersededDecisions(t *testing.T) {
	ctx := context.Background()
	j := &Journal{}
	old := loan("d1", "mortgage for a first time buyer", "approved", 0.9)
	old.Span = Span{From: at(1), Until: at(3)}
	j.Write(ctx, old)
	now := loan("d2", "mortgage for a first time buyer", "refused", 0.9)
	now.Span = Span{From: at(3)}
	j.Write(ctx, now)

	ask := Ask{Case: "mortgage for a first time buyer"}
	if got := j.Like(ask); !reflect.DeepEqual(matchIDs(got), []string{"d2"}) {
		t.Errorf("today's precedents are %v, want only the one still standing", matchIDs(got))
	}
	ask.Old = true
	if got := j.Like(ask); len(got) != 2 {
		t.Errorf("asking for superseded decisions gave %v, want both", matchIDs(got))
	}
	ask = Ask{Case: "mortgage for a first time buyer", At: at(2)}
	if got := j.Like(ask); !reflect.DeepEqual(matchIDs(got), []string{"d1"}) {
		t.Errorf("as of day 2 the precedents are %v, want only d1", matchIDs(got))
	}
}

func TestWriteRespectsAClosedContext(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	stop()
	j := &Journal{}
	if _, err := j.Write(ctx, loan("d1", "a case", "approved", 1)); !errors.Is(err, context.Canceled) {
		t.Errorf("Write gave %v, want the cancellation", err)
	}
	if _, err := j.By(ctx, "officer"); !errors.Is(err, context.Canceled) {
		t.Errorf("By gave %v, want the cancellation", err)
	}
}

func decisionIDs(ds []Decision) []string {
	var out []string
	for _, d := range ds {
		out = append(out, d.ID)
	}
	return out
}

func recordIDs(rs []decision.Record) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.ID)
	}
	return out
}

func matchIDs(ms []Match) []string {
	var out []string
	for _, m := range ms {
		out = append(out, m.Decision.ID)
	}
	return out
}
