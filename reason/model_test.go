package reason

import (
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/hanzoai/semantic"
)

// family is the Python suite's ancestor program: two generations of parents
// and the pair of rules that close over them.
func family(t *testing.T) *Model {
	t.Helper()
	e := Engine{Rules: []Rule{
		rule(t, "ancestor(?x, ?y) :- parent(?x, ?y)."),
		rule(t, "ancestor(?x, ?y) :- parent(?x, ?z), ancestor(?z, ?y)."),
	}}
	return run(t, e, tri("tom", "parent", "bob"), tri("bob", "parent", "ann"))
}

func TestMatch(t *testing.T) {
	e := Engine{Rules: []Rule{rule(t, "ancestor(?x, ?y) :- parent(?x, ?y).")}}
	m := run(t, e, tri("tom", "parent", "bob"), tri("tom", "parent", "alex"))

	got := m.Match(Pattern{"tom", "ancestor", "?y"})
	answers := make([]string, 0, len(got))
	for _, b := range got {
		answers = append(answers, b["y"])
	}
	sort.Strings(answers)
	if want := []string{"alex", "bob"}; !reflect.DeepEqual(answers, want) {
		t.Errorf("ancestor(tom, ?y) = %v, want %v", answers, want)
	}
}

func TestMatchNothing(t *testing.T) {
	m := run(t, Engine{}, tri("tom", "parent", "bob"))
	if got := m.Match(Pattern{"sarah", "parent", "?y"}); len(got) != 0 {
		t.Errorf("matched %v, want nothing", got)
	}
}

func TestMatchBound(t *testing.T) {
	e := Engine{Rules: []Rule{rule(t, "ancestor(?x, ?y) :- parent(?x, ?y).")}}
	m := run(t, e, tri("tom", "parent", "bob"))

	if got := m.Match(Pattern{"tom", "ancestor", "bob"}); len(got) != 1 {
		t.Errorf("a fully ground pattern matched %d times, want 1", len(got))
	}
	if got := m.Match(Pattern{"tom", "ancestor", "ann"}); len(got) != 0 {
		t.Errorf("matched %v, want nothing: ann is not tom's", got)
	}
}

func TestMatchEveryPredicate(t *testing.T) {
	m := run(t, Engine{Rules: []Rule{Symmetric("near")}}, tri("a", "near", "b"), tri("a", "type", "place"))
	got := m.Match(Pattern{"a", "?p", "?o"})
	if len(got) != 2 {
		t.Fatalf("matched %d facts about a, want 2", len(got))
	}
	preds := []string{got[0]["p"], got[1]["p"]}
	sort.Strings(preds)
	if want := []string{"near", "type"}; !reflect.DeepEqual(preds, want) {
		t.Errorf("predicates %v, want %v", preds, want)
	}
}

func TestWhy(t *testing.T) {
	m := family(t)
	steps, err := m.Why(Key(tri("tom", "ancestor", "ann")))
	if err != nil {
		t.Fatalf("Why: %v", err)
	}
	want := []string{
		"parent(tom, bob)",
		"parent(bob, ann)",
		"ancestor(bob, ann)",
		"ancestor(tom, ann)",
	}
	got := make([]string, len(steps))
	for i, f := range steps {
		got[i] = f.String()
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Why = %v, want %v (what it rests on, then the fact)", got, want)
	}
}

func TestWhyGiven(t *testing.T) {
	m := family(t)
	steps, err := m.Why(Key(tri("tom", "parent", "bob")))
	if err != nil {
		t.Fatalf("Why: %v", err)
	}
	if len(steps) != 1 || steps[0].String() != "parent(tom, bob)" {
		t.Errorf("Why over a given fact = %v, want just itself", steps)
	}
	if steps[0].Rule != "" || steps[0].Premises != nil || steps[0].Depth != 0 {
		t.Errorf("a given fact claimed a derivation: %+v", steps[0])
	}
}

func TestWhyMissing(t *testing.T) {
	m := family(t)
	if _, err := m.Why(Key(tri("ann", "ancestor", "tom"))); !errors.Is(err, ErrMissing) {
		t.Errorf("Why over an unknown fact = %v, want ErrMissing", err)
	}
}

func TestWhyCountsEachFactOnce(t *testing.T) {
	// Two derivations reach edge(1, 4); its explanation must still list
	// every supporting fact exactly once.
	m := run(t, Engine{Rules: []Rule{Transitive("edge")}},
		tri("1", "edge", "2"), tri("2", "edge", "3"), tri("3", "edge", "4"))

	steps, err := m.Why(Key(tri("1", "edge", "4")))
	if err != nil {
		t.Fatalf("Why: %v", err)
	}
	seen := map[string]int{}
	for _, f := range steps {
		seen[f.Key()]++
	}
	for k, n := range seen {
		if n != 1 {
			t.Errorf("%q appears %d times in one explanation", k, n)
		}
	}
	if last := steps[len(steps)-1].String(); last != "edge(1, 4)" {
		t.Errorf("explanation ends with %q, want edge(1, 4)", last)
	}
}

func TestOrder(t *testing.T) {
	m := family(t)
	all, given, derived := m.All(), m.Given(), m.Derived()
	if len(all) != len(given)+len(derived) {
		t.Fatalf("%d facts, %d given, %d derived", len(all), len(given), len(derived))
	}
	for i, f := range given {
		if all[i].Key() != f.Key() {
			t.Errorf("given fact %d out of place", i)
		}
	}
	for _, f := range given {
		if f.Rule != "" {
			t.Errorf("%s was given but claims rule %q", f, f.Rule)
		}
	}
	for _, f := range derived {
		if f.Rule == "" || len(f.Premises) == 0 || f.Depth == 0 {
			t.Errorf("%s was derived but cannot say how: %+v", f, f)
		}
	}
}

func TestAppendDoesNotReachModel(t *testing.T) {
	m := family(t)
	before := len(m.All())
	kept := append(m.Derived(), Fact{Triple: tri("x", "p", "y")}) //nolint:gocritic // deliberate
	if len(m.All()) != before {
		t.Error("appending to Derived() wrote into the model")
	}
	if kept[len(kept)-1].Subject != "x" {
		t.Error("append lost the caller's fact")
	}
}

func TestTriples(t *testing.T) {
	m := family(t)
	got := m.Triples()
	if len(got) != len(m.Derived()) {
		t.Fatalf("Triples returned %d, want the %d derived", len(got), len(m.Derived()))
	}
	for i, tr := range got {
		if Key(tr) != m.Derived()[i].Key() {
			t.Errorf("triple %d is %v, out of step with the derived facts", i, tr)
		}
	}
}

func TestFactLookup(t *testing.T) {
	m := family(t)
	f, ok := m.Fact(Key(tri("tom", "ancestor", "bob")))
	if !ok {
		t.Fatal("ancestor(tom, bob) missing")
	}
	if f.Depth != 1 {
		t.Errorf("depth %d, want 1", f.Depth)
	}
	if _, ok := m.Fact("nothing"); ok {
		t.Error("looked up a key no fact carries and got a fact")
	}
}

func TestProv(t *testing.T) {
	m := family(t)
	entities, activities := m.Prov("reasoner")

	if len(entities) != len(m.All()) {
		t.Fatalf("%d entities for %d facts", len(entities), len(m.All()))
	}
	if len(activities) != len(m.Derived()) {
		t.Fatalf("%d activities for %d derived facts", len(activities), len(m.Derived()))
	}

	byID := map[string]int{}
	for i, a := range activities {
		byID[a.ID] = i
	}
	for i, e := range entities {
		f := m.All()[i]
		if e.ID != f.Key() {
			t.Fatalf("entity %d names %q, want %q", i, e.ID, f.Key())
		}
		if f.Rule == "" {
			if e.GeneratedBy != "" || e.DerivedFrom != nil {
				t.Errorf("given fact %s claims a derivation: %+v", f, e)
			}
			continue
		}
		j, ok := byID[e.GeneratedBy]
		if !ok {
			t.Fatalf("%s was generated by %q, which is not among the activities", f, e.GeneratedBy)
		}
		a := activities[j]
		if a.Kind != f.Rule {
			t.Errorf("%s: activity kind %q, want the rule %q", f, a.Kind, f.Rule)
		}
		if a.Agent != "reasoner" {
			t.Errorf("%s: activity agent %q, want the one named", f, a.Agent)
		}
		if !reflect.DeepEqual(a.Used, f.Premises) || !reflect.DeepEqual(e.DerivedFrom, f.Premises) {
			t.Errorf("%s: PROV lost the premises %q", f, f.Premises)
		}
		if a.Start.IsZero() || a.End.Before(a.Start) {
			t.Errorf("%s: activity window %v to %v", f, a.Start, a.End)
		}
		for _, used := range a.Used {
			if _, ok := m.Fact(used); !ok {
				t.Errorf("%s: used %q, which is in no model", f, used)
			}
		}
	}
}

func TestHas(t *testing.T) {
	m := family(t)
	for _, c := range []struct {
		in   semantic.Triple
		want bool
	}{
		{tri("tom", "ancestor", "ann"), true},
		{tri("tom", "parent", "bob"), true},
		{tri("ann", "ancestor", "tom"), false},
	} {
		if got := m.Has(c.in); got != c.want {
			t.Errorf("Has(%v) = %v, want %v", c.in, got, c.want)
		}
	}
	scored := tri("tom", "ancestor", "ann")
	scored.Score = 0.2
	if !m.Has(scored) {
		t.Error("confidence changed what claim a triple makes")
	}
}
