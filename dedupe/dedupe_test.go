package dedupe

import (
	"context"
	"errors"
	"testing"
)

func TestEntityValueReadsPropertiesThenTheNamedFields(t *testing.T) {
	e := Entity{
		Name:  "Ada Lovelace",
		Kind:  "Person",
		Props: map[string]any{"born": 1815, "name": "A. A. King"},
	}
	for _, c := range []struct {
		prop string
		want any
		ok   bool
	}{
		{"born", 1815, true},
		{"name", "A. A. King", true}, // a stated property wins over the field
		{"kind", "Person", true},
		{"type", "Person", true},
		{"class", "Person", true},
		{"Kind", "Person", true}, // the field aliases are read case-blind
		{"died", nil, false},
	} {
		got, ok := e.Value(c.prop)
		if ok != c.ok || got != c.want {
			t.Errorf("Value(%q) = %v,%v want %v,%v", c.prop, got, ok, c.want, c.ok)
		}
	}
	if _, ok := (Entity{}).Value("name"); ok {
		t.Error("a record with no name reported one")
	}
}

func TestRecordsWithNoIdentifierAreKnownByTheirName(t *testing.T) {
	es := []Entity{
		{Name: "Ada Lovelace", Props: map[string]any{"born": 1815}, From: Source{Doc: "a"}},
		{Name: "ada  lovelace", Props: map[string]any{"born": 1816}, From: Source{Doc: "b"}},
	}
	cs := Values(es, "born")
	if len(cs) != 1 {
		t.Fatalf("found %d conflicts, want the two spellings read as one thing", len(cs))
	}
	if cs[0].Subject != "ada lovelace" {
		t.Errorf("subject = %q, want the folded name", cs[0].Subject)
	}
}

func TestLimitsReadsNumbersHoweverTheyAreHeld(t *testing.T) {
	lim := map[string]Limit{"age": {Lo: 0, Hi: 130}}
	for _, v := range []any{200, int64(200), float32(200), 200.0, "200"} {
		cs := Limits([]Entity{{ID: "e1", Props: map[string]any{"age": v}}}, lim)
		if len(cs) != 1 {
			t.Errorf("%T(%v): found %d conflicts, want 1", v, v, len(cs))
		}
	}
	for _, v := range []any{nil, "old", []int{200}, true} {
		cs := Limits([]Entity{{ID: "e1", Props: map[string]any{"age": v}}}, lim)
		if len(cs) != 0 {
			t.Errorf("%T(%v): found %v, want nothing — it is not a number", v, v, cs)
		}
	}
}

func TestAddRejectsOptionsOutsideTheirRange(t *testing.T) {
	if _, err := Add(context.Background(), firms(), firms(), Opt{Score: -1}); !errors.Is(err, ErrOption) {
		t.Errorf("error = %v, want ErrOption", err)
	}
}

func TestAddStopsWhenTheContextIsDone(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	stop()
	if _, err := Add(ctx, firms(), firms(), loose); !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

func TestCredibleCannotChooseWhereNothingIsBelieved(t *testing.T) {
	c := Conflict{ID: "c", Values: []Claim{
		{Value: 30, From: Source{Doc: "doc1"}},
		{Value: 32, From: Source{Doc: "doc2"}},
	}}
	p := Credible.Settle(c, Trust{"doc1": 0, "doc2": 0})
	if p.Done {
		t.Errorf("settled a conflict where no document carries credit: %+v", p)
	}
	if p.Why == "" {
		t.Error("an unsettled pick should say why")
	}
}

func TestGroupWithNoPairsIsNotTight(t *testing.T) {
	g := Group{Of: []Entity{{ID: "a"}}}
	if g.Tight() != 0 || confidence(g) != 0 {
		t.Errorf("a group built from no pairs scored %v tight, %v sure", g.Tight(), confidence(g))
	}
}
