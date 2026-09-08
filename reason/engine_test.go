package reason

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sort"
	"testing"

	"github.com/hanzoai/semantic"
)

// tri writes a triple the way the tests read: subject, predicate, object.
func tri(s, p, o string) semantic.Triple {
	return semantic.Triple{Subject: s, Predicate: p, Object: o}
}

// rule parses a rule or fails the test; a malformed rule in a test is a bug
// in the test.
func rule(t *testing.T, s string) Rule {
	t.Helper()
	r, err := Parse(s)
	if err != nil {
		t.Fatalf("Parse(%q): %v", s, err)
	}
	return r
}

// run closes the engine over the facts and fails on any error.
func run(t *testing.T, e Engine, facts ...semantic.Triple) *Model {
	t.Helper()
	m, err := e.Run(context.Background(), facts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return m
}

// said lists facts as predicate(subject, object), sorted, which is how the
// Python suite reads its assertions.
func said(facts []Fact) []string {
	out := make([]string, 0, len(facts))
	for _, f := range facts {
		out = append(out, f.String())
	}
	sort.Strings(out)
	return out
}

func TestSingleRule(t *testing.T) {
	e := Engine{Rules: []Rule{rule(t, "ancestor(?x, ?y) :- parent(?x, ?y).")}}
	m := run(t, e, tri("tom", "parent", "bob"))

	if got, want := said(m.Derived()), []string{"ancestor(tom, bob)"}; !reflect.DeepEqual(got, want) {
		t.Errorf("derived %v, want %v", got, want)
	}
	if got, want := len(m.All()), 2; got != want {
		t.Errorf("model holds %d facts, want %d", got, want)
	}
}

func TestRecursiveAncestor(t *testing.T) {
	e := Engine{Rules: []Rule{
		rule(t, "ancestor(?x, ?y) :- parent(?x, ?y)."),
		rule(t, "ancestor(?x, ?y) :- parent(?x, ?z), ancestor(?z, ?y)."),
	}}
	m := run(t, e, tri("tom", "parent", "bob"), tri("bob", "parent", "ann"))

	want := []string{"ancestor(bob, ann)", "ancestor(tom, ann)", "ancestor(tom, bob)"}
	if got := said(m.Derived()); !reflect.DeepEqual(got, want) {
		t.Errorf("derived %v, want %v", got, want)
	}
}

func TestMultiHop(t *testing.T) {
	e := Engine{Rules: []Rule{
		rule(t, "reachable(?x, ?y) :- edge(?x, ?y)."),
		rule(t, "reachable(?x, ?y) :- edge(?x, ?z), reachable(?z, ?y)."),
	}}
	m := run(t, e, tri("1", "edge", "2"), tri("2", "edge", "3"), tri("3", "edge", "4"))

	if !m.Has(tri("1", "reachable", "4")) {
		t.Errorf("reachable(1, 4) was not derived; derived %v", said(m.Derived()))
	}
	if got, want := len(m.Derived()), 6; got != want {
		t.Errorf("derived %d facts, want %d (every pair on the chain)", got, want)
	}
}

func TestGrandparent(t *testing.T) {
	e := Engine{Rules: []Rule{rule(t, "grandparent(?x, ?y) :- parent(?x, ?z), parent(?z, ?y).")}}
	m := run(t, e, tri("tom", "parent", "bob"), tri("bob", "parent", "ann"))

	if !m.Has(tri("tom", "grandparent", "ann")) {
		t.Error("grandparent(tom, ann) was not derived")
	}
	if m.Has(tri("tom", "grandparent", "bob")) {
		t.Error("grandparent(tom, bob) was derived; a parent is not a grandparent")
	}
}

// TestConjunction is the Python reasoner's forward-chaining case written as
// triples: a type assertion and a relation join on the same subject.
func TestConjunction(t *testing.T) {
	e := Engine{Rules: []Rule{rule(t, "child(?y, ?x) :- type(?x, Person), parent(?x, ?y).")}}
	m := run(t, e, tri("John", "type", "Person"), tri("John", "parent", "Jane"))

	derived := m.Derived()
	if len(derived) != 1 || derived[0].String() != "child(Jane, John)" {
		t.Fatalf("derived %v, want [child(Jane, John)]", said(derived))
	}
	want := []string{Key(tri("John", "type", "Person")), Key(tri("John", "parent", "Jane"))}
	if got := derived[0].Premises; !reflect.DeepEqual(got, want) {
		t.Errorf("premises %q, want the two facts that matched the body", got)
	}
	if got := derived[0].Rule; got != "child(?y, ?x) :- type(?x, Person), parent(?x, ?y)." {
		t.Errorf("rule recorded as %q; an unnamed rule records itself", got)
	}
	if got := derived[0].Depth; got != 1 {
		t.Errorf("depth %d, want 1", got)
	}
}

func TestTransitiveCycle(t *testing.T) {
	e := Engine{Rules: []Rule{Transitive("part_of")}}
	m := run(t, e, tri("a", "part_of", "b"), tri("b", "part_of", "a"))

	want := []string{"part_of(a, a)", "part_of(b, b)"}
	if got := said(m.Derived()); !reflect.DeepEqual(got, want) {
		t.Errorf("derived %v, want %v — a cycle closes, it does not run", got, want)
	}
}

func TestSymmetric(t *testing.T) {
	e := Engine{Rules: []Rule{Symmetric("sibling")}}
	m := run(t, e, tri("ada", "sibling", "bob"))

	if got, want := said(m.Derived()), []string{"sibling(bob, ada)"}; !reflect.DeepEqual(got, want) {
		t.Errorf("derived %v, want %v", got, want)
	}
	if got := m.Derived()[0].Rule; got != "symmetric sibling" {
		t.Errorf("rule recorded as %q, want %q", got, "symmetric sibling")
	}
}

func TestInverse(t *testing.T) {
	e := Engine{Rules: []Rule{Inverse("parent", "child"), Inverse("child", "parent")}}
	m := run(t, e, tri("john", "parent", "jane"))

	if got, want := said(m.Derived()), []string{"child(jane, john)"}; !reflect.DeepEqual(got, want) {
		t.Errorf("derived %v, want %v — reading back gives nothing new", got, want)
	}
}

func TestSubclass(t *testing.T) {
	e := Engine{Class: Tree{"dog": "mammal", "mammal": "animal"}}
	m := run(t, e, tri("rex", "type", "dog"), tri("rex", "name", "Rex"))

	want := []string{"type(rex, animal)", "type(rex, mammal)"}
	if got := said(m.Derived()); !reflect.DeepEqual(got, want) {
		t.Errorf("derived %v, want %v", got, want)
	}
	for _, f := range m.Derived() {
		if f.Rule != "subclass" {
			t.Errorf("%s recorded as %q, want subclass", f, f.Rule)
		}
		if got, want := f.Premises, []string{Key(tri("rex", "type", "dog"))}; !reflect.DeepEqual(got, want) {
			t.Errorf("%s rests on %q, want the stated type", f, got)
		}
	}
}

func TestSubclassNeedsTheTypePredicate(t *testing.T) {
	e := Engine{Class: Tree{"dog": "mammal"}, Type: "is_a"}
	m := run(t, e, tri("rex", "type", "dog"), tri("fido", "is_a", "dog"))

	if got, want := said(m.Derived()), []string{"is_a(fido, mammal)"}; !reflect.DeepEqual(got, want) {
		t.Errorf("derived %v, want %v — only the configured predicate states a class", got, want)
	}
}

func TestSubproperty(t *testing.T) {
	e := Engine{Prop: Tree{"mother": "parent", "parent": "relative"}}
	m := run(t, e, tri("mary", "mother", "john"))

	want := []string{"parent(mary, john)", "relative(mary, john)"}
	if got := said(m.Derived()); !reflect.DeepEqual(got, want) {
		t.Errorf("derived %v, want %v", got, want)
	}
	if got := m.Derived()[0].Rule; got != "subproperty" {
		t.Errorf("rule recorded as %q, want subproperty", got)
	}
}

// TestSchemaFeedsRules is the interaction that makes one fixpoint worth
// having: the schema lifts mother to parent, and the transitive rule then
// closes over what the schema produced.
func TestSchemaFeedsRules(t *testing.T) {
	e := Engine{
		Rules: []Rule{Transitive("parent")},
		Prop:  Tree{"mother": "parent"},
	}
	m := run(t, e, tri("a", "mother", "b"), tri("b", "mother", "c"))

	want := []string{"parent(a, b)", "parent(a, c)", "parent(b, c)"}
	if got := said(m.Derived()); !reflect.DeepEqual(got, want) {
		t.Fatalf("derived %v, want %v", got, want)
	}
	deep, ok := m.Fact(Key(tri("a", "parent", "c")))
	if !ok {
		t.Fatal("parent(a, c) missing")
	}
	if deep.Depth != 2 {
		t.Errorf("parent(a, c) has depth %d, want 2: the schema had to fire first", deep.Depth)
	}
}

func TestDepth(t *testing.T) {
	facts := []semantic.Triple{
		tri("1", "edge", "2"), tri("2", "edge", "3"),
		tri("3", "edge", "4"), tri("4", "edge", "5"),
	}
	full := run(t, Engine{Rules: []Rule{Transitive("edge")}}, facts...)
	if got, want := len(full.Derived()), 6; got != want {
		t.Errorf("closure derived %d, want %d", got, want)
	}

	e := Engine{Rules: []Rule{Transitive("edge")}, Depth: 1}
	m, err := e.Run(context.Background(), facts)
	if !errors.Is(err, ErrDepth) {
		t.Fatalf("Run with Depth 1 error = %v, want ErrDepth", err)
	}
	if m == nil {
		t.Fatal("Run returned no model; a partial answer is still an answer")
	}
	want := []string{"edge(1, 3)", "edge(2, 4)", "edge(3, 5)"}
	if got := said(m.Derived()); !reflect.DeepEqual(got, want) {
		t.Errorf("one round derived %v, want %v", got, want)
	}
	for _, f := range m.Derived() {
		if f.Depth != 1 {
			t.Errorf("%s has depth %d after one round", f, f.Depth)
		}
	}
}

// TestDepthCertifiesClosure pins the one-round gap between producing a
// closure and knowing it is closed: two rounds derive everything a
// three-edge chain implies, and the third is what proves nothing is left.
func TestDepthCertifiesClosure(t *testing.T) {
	chain := []semantic.Triple{tri("1", "edge", "2"), tri("2", "edge", "3"), tri("3", "edge", "4")}

	short, err := Engine{Rules: []Rule{Transitive("edge")}, Depth: 2}.Run(context.Background(), chain)
	if !errors.Is(err, ErrDepth) {
		t.Fatalf("Depth 2 error = %v, want ErrDepth: the run stopped before it could tell", err)
	}
	want := []string{"edge(1, 3)", "edge(1, 4)", "edge(2, 4)"}
	if got := said(short.Derived()); !reflect.DeepEqual(got, want) {
		t.Errorf("Depth 2 derived %v, want %v", got, want)
	}

	full, err := Engine{Rules: []Rule{Transitive("edge")}, Depth: 3}.Run(context.Background(), chain)
	if err != nil {
		t.Fatalf("Depth 3: %v", err)
	}
	if got := said(full.Derived()); !reflect.DeepEqual(got, want) {
		t.Errorf("Depth 3 derived %v, want the same %v", got, want)
	}
}

func TestCancel(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	stop()
	e := Engine{Rules: []Rule{Transitive("edge")}}
	m, err := e.Run(ctx, []semantic.Triple{tri("1", "edge", "2"), tri("2", "edge", "3")})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	if m == nil || len(m.All()) != 2 {
		t.Fatal("a cancelled run must still return what it was given")
	}
	if len(m.Derived()) != 0 {
		t.Errorf("derived %v after cancellation", said(m.Derived()))
	}
}

func TestBadRuleStopsBeforeWork(t *testing.T) {
	e := Engine{Rules: []Rule{
		Transitive("edge"),
		{Body: []Pattern{{"?x", "edge", "?y"}}, Head: Pattern{"?x", "edge", "?z"}},
	}}
	m, err := e.Run(context.Background(), []semantic.Triple{tri("1", "edge", "2")})
	if !errors.Is(err, ErrRule) {
		t.Fatalf("Run error = %v, want ErrRule", err)
	}
	if m != nil {
		t.Error("a rule that cannot fire must be reported before any work is done")
	}
}

func TestGivenFacts(t *testing.T) {
	m := run(t, Engine{},
		tri("a", "p", "b"),
		tri("a", "p", "b"), // the same claim twice is one fact
		tri("", "p", "b"),  // states nothing
		tri("a", "", "b"),
		tri("a", "p", " "),
	)
	if got, want := said(m.Given()), []string{"p(a, b)"}; !reflect.DeepEqual(got, want) {
		t.Errorf("given %v, want %v", got, want)
	}
	if len(m.Derived()) != 0 {
		t.Errorf("an engine with no rule and no schema derived %v", said(m.Derived()))
	}
}

func TestScore(t *testing.T) {
	weak := tri("a", "edge", "b")
	weak.Score = 0.5
	strong := tri("b", "edge", "c")
	strong.Score = 0.9

	r := Transitive("edge")
	r.Score = 0.8
	m := run(t, Engine{Rules: []Rule{r}}, weak, strong)

	got, ok := m.Fact(Key(tri("a", "edge", "c")))
	if !ok {
		t.Fatal("edge(a, c) was not derived")
	}
	if want := 0.5 * 0.8; math.Abs(got.Score-want) > 1e-9 {
		t.Errorf("score %v, want %v: the weakest premise, discounted by the rule", got.Score, want)
	}
}

func TestScoreUnstated(t *testing.T) {
	m := run(t, Engine{Rules: []Rule{Transitive("edge")}},
		tri("a", "edge", "b"), tri("b", "edge", "c"))
	got, _ := m.Fact(Key(tri("a", "edge", "c")))
	if got.Score != 1 {
		t.Errorf("score %v, want 1: facts stating no confidence are not stating no confidence", got.Score)
	}
}

func TestDerivedCarriesNoChunk(t *testing.T) {
	m := run(t, Engine{Rules: []Rule{Symmetric("sibling")}}, semantic.Triple{
		Subject:   "ada",
		Predicate: "sibling",
		Object:    "bob",
		From:      semantic.Chunk{DocID: "d1", Index: 3, Text: "Ada and Bob"},
	})
	if got := m.Derived()[0].From; got != (semantic.Chunk{}) {
		t.Errorf("derived fact claims chunk %+v; it came from a rule, not a document", got)
	}
}

func TestDeterministic(t *testing.T) {
	e := Engine{
		Rules: []Rule{Transitive("edge"), Symmetric("near")},
		Class: Tree{"dog": "mammal"},
	}
	facts := []semantic.Triple{
		tri("1", "edge", "2"), tri("2", "edge", "3"), tri("3", "edge", "4"),
		tri("a", "near", "b"), tri("rex", "type", "dog"),
	}
	first := run(t, e, facts...)
	second := run(t, e, facts...)

	keys := func(m *Model) []string {
		out := make([]string, 0, len(m.All()))
		for _, f := range m.All() {
			out = append(out, f.String())
		}
		return out
	}
	if !reflect.DeepEqual(keys(first), keys(second)) {
		t.Error("two runs over the same input disagreed on order")
	}
}

func TestEmpty(t *testing.T) {
	m := run(t, Engine{Rules: []Rule{Transitive("edge")}})
	if len(m.All()) != 0 {
		t.Errorf("empty input gave %v", said(m.All()))
	}
	if got := m.Triples(); len(got) != 0 {
		t.Errorf("Triples = %v, want none", got)
	}
}
