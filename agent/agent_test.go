package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestLearnPutsNotesInBothPlaces(t *testing.T) {
	ctx := context.Background()
	k := New()
	k.Scope = Scope{Agent: "ada", Session: "s1"}

	id, err := k.Learn(ctx, Note{Text: "ada asked about mortgages", Of: []string{"ada", "mortgage"}, At: at(1)})
	if err != nil {
		t.Fatalf("Learn: %v", err)
	}
	if _, ok := k.Memory.Get(id); !ok {
		t.Fatal("the note was not kept")
	}
	n, ok := k.Graph.Node(id)
	if !ok {
		t.Fatal("the note is not in the graph")
	}
	if n.Kind != kindNote || n.Scope != k.Scope {
		t.Errorf("the note node is %+v, want a note in the agent's scope", n)
	}
	if !n.Span.From.Equal(at(1)) {
		t.Errorf("the note holds from %v, want the moment it was written", n.Span.From)
	}
	for _, e := range []string{"ada", "mortgage"} {
		if _, ok := k.Graph.Edge(Link{From: id, To: e, Label: About}); !ok {
			t.Errorf("nothing joins the note to %s", e)
		}
	}
	// Two things named in one note are one hop apart through it, and no closer.
	if _, ok := k.Graph.Edge(Link{From: "ada", To: "mortgage", Label: About}); ok {
		t.Error("an edge was drawn straight between two entities; the note is the only evidence there is")
	}
	if _, err := k.Learn(ctx, Note{}); err == nil {
		t.Error("a note with no text was kept")
	}
}

func TestRecallUsesWordsAndTheGraph(t *testing.T) {
	ctx := context.Background()
	k := New()
	// Half the question by its words, and about nothing.
	k.Learn(ctx, Note{ID: "n1", Text: "mortgage rates rose", At: at(1)})
	// None of the question by its words, but about the thing it names.
	k.Learn(ctx, Note{ID: "n2", Text: "the file is complete", Of: []string{"mortgage"}, At: at(2)})
	// Half by its words and about the thing as well.
	k.Learn(ctx, Note{ID: "n3", Text: "mortgage rejected", Of: []string{"mortgage"}, At: at(3)})

	got := k.Recall(ctx, "mortgage approved", 0)
	if want := []string{"n3", "n1", "n2"}; !reflect.DeepEqual(found(got), want) {
		t.Fatalf("recalled %v, want %v: right twice beats right once", found(got), want)
	}
	almost(t, "the note found both ways", got[0].Score, 0.6*1.2)
	almost(t, "the note found by its words alone", got[1].Score, 0.6)
	almost(t, "the note reached only through the graph", got[2].Score, 0.5*byEntity)
	if n := len(k.Recall(ctx, "mortgage approved", 2)); n != 2 {
		t.Errorf("asked for two, got %d", n)
	}
	if got := k.Recall(ctx, "nothing here", 0); got != nil {
		t.Errorf("a question nothing answers recalled %v", found(got))
	}
}

func TestRecallStaysInScope(t *testing.T) {
	ctx := context.Background()
	k := New()
	k.Scope = Scope{Agent: "ada"}
	k.Learn(ctx, Note{ID: "mine", Text: "loan approved", Of: []string{"loan"}, At: at(1)})
	k.Learn(ctx, Note{ID: "theirs", Text: "loan approved", Of: []string{"loan"}, Scope: Scope{Agent: "bob"}, At: at(2)})

	if got := found(k.Recall(ctx, "loan", 0)); !reflect.DeepEqual(got, []string{"mine"}) {
		t.Errorf("ada recalled %v, want only her own note", got)
	}
	k.Scope = Scope{Agent: "bob"}
	if got := found(k.Recall(ctx, "loan", 0)); !reflect.DeepEqual(got, []string{"theirs"}) {
		t.Errorf("bob recalled %v, want only his own note", got)
	}
}

func TestDecideRecordsARefusal(t *testing.T) {
	ctx := context.Background()
	k := New()
	if _, err := k.Rules.Add(lending()); err != nil {
		t.Fatal(err)
	}

	out, err := k.Decide(ctx, applicant(600, 0.3))
	if err != nil {
		t.Fatalf("a refusal came back as an error, and a refusal is a decision: %v", err)
	}
	if out.Allowed {
		t.Error("the decision was allowed; the score is under the policy's floor")
	}
	if out.Rule != "p1" {
		t.Errorf("the decision was taken under %q, want the policy that refused it", out.Rule)
	}
	if !strings.Contains(out.Gloss, "credit_score is 600, under 650") {
		t.Errorf("the record reads %q, want the reason it was refused", out.Gloss)
	}
	back, ok := k.Journal.Read(out.ID)
	if !ok {
		t.Fatal("the refusal was not written down")
	}
	if back.Allowed || back.Gloss != out.Gloss {
		t.Errorf("read back %+v, want the refusal and its reason kept", back)
	}

	fine, err := k.Decide(ctx, applicant(700, 0.3))
	if err != nil {
		t.Fatal(err)
	}
	if !fine.Allowed || !strings.Contains(fine.Gloss, "passed") {
		t.Errorf("a decision inside the rules reads %+v, want it allowed", fine)
	}
}

func TestDecideFillsTheAgentFromScope(t *testing.T) {
	ctx := context.Background()
	k := New()
	k.Scope = Scope{Agent: "ada"}
	d := Decision{Topic: "loan", Case: "something", Confidence: 0.5,
		Choice: "held"}
	out, err := k.Decide(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if out.Agent != "ada" {
		t.Errorf("the decision was taken by %q, want the agent in scope", out.Agent)
	}
	if _, err := k.Decide(ctx, Decision{Case: "no topic"}); err == nil {
		t.Error("a decision with no topic and no choice was written; that is a mistake in the caller, not a refusal")
	}
}

func TestWhy(t *testing.T) {
	ctx := context.Background()
	k := New()
	k.Rules.Add(lending())

	first, err := k.Decide(ctx, applicant(700, 0.3))
	if err != nil {
		t.Fatal(err)
	}
	second := applicant(720, 0.2)
	second.ID = "d2"
	second.Because = []string{"credit file", "appraisal"}
	if _, err := k.Decide(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := k.Journal.Lead(first.ID, "d2", Caused); err != nil {
		t.Fatal(err)
	}

	s, err := k.Why(ctx, "d2")
	if err != nil {
		t.Fatalf("Why: %v", err)
	}
	if s.Decision.ID != "d2" {
		t.Errorf("the story is about %s, want d2", s.Decision.ID)
	}
	if got := ids(s.Because); !reflect.DeepEqual(got, []string{"credit file", "appraisal"}) {
		t.Errorf("it rested on %v, want the two pieces of evidence", got)
	}
	if got := awayIDs(s.Led); !reflect.DeepEqual(got, []string{first.ID}) {
		t.Errorf("what led to it is %v, want the first decision", got)
	}
	if got := awayIDs(s.Roots); !reflect.DeepEqual(got, []string{first.ID}) {
		t.Errorf("the chain ends at %v, want the first decision", got)
	}
	if !strings.Contains(s.Gloss, "allowed") || !strings.Contains(s.Gloss, "2 pieces of evidence") {
		t.Errorf("the story reads %q, want the verdict and what it stood on", s.Gloss)
	}

	// Evidence retracted after the fact shows as retracted rather than
	// quietly staying in the story.
	k.Graph.Retract("appraisal", "withdrawn", at(9))
	s, _ = k.Why(ctx, "d2")
	if len(s.Because) != 2 {
		t.Fatalf("it now rests on %v; a retraction does not rewrite the record", ids(s.Because))
	}
	if s.Because[1].Span.Until.IsZero() {
		t.Error("the withdrawn evidence still reads as current")
	}
	if _, err := k.Why(ctx, "nobody"); !errors.Is(err, ErrNoDecision) {
		t.Errorf("explaining a decision that is not there gave %v, want ErrNoDecision", err)
	}
}

func TestParts(t *testing.T) {
	k := New()
	if k.Journal.G != k.Graph || k.Cause.G != k.Graph {
		t.Error("the parts do not share one graph, so a decision would not be reachable from the knowledge it rests on")
	}
	if k.Memory == nil || k.Rules == nil {
		t.Error("New left a part nil")
	}
}
