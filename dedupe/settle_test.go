package dedupe

import (
	"math"
	"reflect"
	"testing"
	"time"
)

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// rift is the Python resolver fixture: three documents, two of which agree
// on 30 while the latest says 32.
func rift() Conflict {
	return Conflict{
		ID:      "c1",
		Kind:    Value,
		Subject: "e1",
		Prop:    "age",
		Values: []Claim{
			{Value: 30, From: Source{Doc: "doc1", Score: 0.9, At: day(2023, time.January, 1)}},
			{Value: 30, From: Source{Doc: "doc3", Score: 0.9, At: day(2023, time.January, 2)}},
			{Value: 32, From: Source{Doc: "doc2", Score: 0.8, At: day(2023, time.June, 1)}},
		},
	}
}

func TestSettleByEachRule(t *testing.T) {
	for _, c := range []struct {
		rule  Rule
		trust Trust
		done  bool
		value any
		from  []string
	}{
		{Vote, nil, true, 30, []string{"doc1", "doc3"}},
		{Newest, nil, true, 32, []string{"doc2"}},
		{Oldest, nil, true, 30, []string{"doc1"}},
		{Sure, nil, true, 30, []string{"doc1"}},
		{Credible, nil, true, 30, []string{"doc1", "doc3"}},
		// One document believed outright outweighs two barely believed.
		{Credible, Trust{"doc1": 0.1, "doc3": 0.1, "doc2": 1}, true, 32, []string{"doc2"}},
		{Flag, nil, false, nil, nil},
	} {
		p := c.rule.Settle(rift(), c.trust)
		if p.Done != c.done {
			t.Errorf("%s: done = %v, want %v (%s)", c.rule, p.Done, c.done, p.Why)
		}
		if !reflect.DeepEqual(p.Value, c.value) {
			t.Errorf("%s: value = %v, want %v", c.rule, p.Value, c.value)
		}
		if !reflect.DeepEqual(p.From, c.from) {
			t.Errorf("%s: rests on %v, want %v", c.rule, p.From, c.from)
		}
		if p.Of != "c1" || p.By != c.rule {
			t.Errorf("%s: pick does not name what it settled: %+v", c.rule, p)
		}
		if p.Why == "" {
			t.Errorf("%s: pick says nothing about why", c.rule)
		}
	}
}

func TestVoteScoresTheShareOfDocuments(t *testing.T) {
	p := Vote.Settle(rift(), nil)
	if math.Abs(p.Score-2.0/3) > 1e-9 {
		t.Errorf("confidence = %v, want two votes of three", p.Score)
	}
}

func TestVoteBreaksTiesByWhatWasStatedFirst(t *testing.T) {
	c := Conflict{ID: "c", Values: []Claim{
		{Value: "b", From: Source{Doc: "doc2"}},
		{Value: "a", From: Source{Doc: "doc1"}},
	}}
	if p := Vote.Settle(c, nil); p.Value != "b" {
		t.Errorf("value = %v, want the one stated first", p.Value)
	}
}

func TestCredibleScoresTheShareOfCredit(t *testing.T) {
	p := Credible.Settle(rift(), Trust{"doc1": 0.1, "doc3": 0.1, "doc2": 1})
	// 0.8 x 1 of credit for 32, against 0.9 x 0.1 twice for 30.
	if math.Abs(p.Score-0.8/(0.8+0.18)) > 1e-9 {
		t.Errorf("confidence = %v", p.Score)
	}
	if got := (Trust{}).Of("nobody"); got != 0.5 {
		t.Errorf("an unrated document is trusted %v, want a half", got)
	}
	if got := Trust(nil).Of("nobody"); got != 0.5 {
		t.Errorf("with no table at all a document is trusted %v, want a half", got)
	}
}

func TestNewestAndOldestFallBackToTheOrderStated(t *testing.T) {
	c := Conflict{ID: "c", Values: []Claim{
		{Value: 30, From: Source{Doc: "doc1", Score: 0.9}},
		{Value: 32, From: Source{Doc: "doc2", Score: 0.9}},
	}}
	if p := Oldest.Settle(c, nil); p.Value != 30 || !p.Done {
		t.Errorf("oldest = %v, want the value stated first", p.Value)
	}
	if p := Newest.Settle(c, nil); p.Value != 32 || !p.Done {
		t.Errorf("newest = %v, want the value stated last", p.Value)
	}
}

func TestNewestIgnoresUndatedDocumentsWhenAnyIsDated(t *testing.T) {
	c := Conflict{ID: "c", Values: []Claim{
		{Value: 30, From: Source{Doc: "undated"}},
		{Value: 32, From: Source{Doc: "dated", At: day(2020, time.March, 4)}},
		{Value: 34, From: Source{Doc: "also undated"}},
	}}
	p := Newest.Settle(c, nil)
	if p.Value != 32 {
		t.Errorf("value = %v, want the only dated one", p.Value)
	}
}

func TestSureTakesTheMostConfidentDocument(t *testing.T) {
	c := Conflict{ID: "c", Values: []Claim{
		{Value: 30, From: Source{Doc: "doc1", Score: 0.4}},
		{Value: 32, From: Source{Doc: "doc2", Score: 0.95}},
	}}
	p := Sure.Settle(c, nil)
	if p.Value != 32 || p.Score != 0.95 {
		t.Errorf("pick = %+v, want the value from the surer document", p)
	}
}

func TestFlagLeavesTheDisagreementStanding(t *testing.T) {
	p := Flag.Settle(rift(), nil)
	if p.Done || p.Value != nil {
		t.Errorf("flagging settled something: %+v", p)
	}
}

func TestSettleNothing(t *testing.T) {
	empty := Conflict{ID: "c"}
	for _, r := range [...]Rule{Vote, Credible, Newest, Oldest, Sure, Flag} {
		if p := r.Settle(empty, nil); p.Done {
			t.Errorf("%s settled a conflict with no values: %+v", r, p)
		}
	}
	if p := Rule("nonsense").Settle(rift(), nil); p.Done || p.Why == "" {
		t.Errorf("an unknown rule settled something: %+v", p)
	}
}

func TestSettleReturnsOnePickPerConflict(t *testing.T) {
	cs := []Conflict{rift(), {ID: "c2", Values: []Claim{{Value: 1, From: Source{Doc: "d"}}}}}
	ps := Settle(cs, Vote, nil)
	if len(ps) != 2 {
		t.Fatalf("settled %d of %d conflicts", len(ps), len(cs))
	}
	if ps[0].Of != "c1" || ps[1].Of != "c2" {
		t.Errorf("picks do not line up with the conflicts: %+v", ps)
	}
	if none := Settle(nil, Vote, nil); len(none) != 0 {
		t.Errorf("settling nothing gave %v", none)
	}
}

func TestSettleKeepsTheValueAsItWasStated(t *testing.T) {
	// A number settled by voting comes back a number, not the text of one.
	p := Vote.Settle(rift(), nil)
	if _, ok := p.Value.(int); !ok {
		t.Errorf("value came back as %T, want the int that was stated", p.Value)
	}
}
