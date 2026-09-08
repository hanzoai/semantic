package reason

import (
	"errors"
	"reflect"
	"testing"

	"github.com/hanzoai/semantic"
)

func TestParse(t *testing.T) {
	for _, c := range []struct {
		in   string
		want Rule
	}{
		{
			"ancestor(?x, ?y) :- parent(?x, ?y).",
			Rule{Body: []Pattern{{"?x", "parent", "?y"}}, Head: Pattern{"?x", "ancestor", "?y"}},
		},
		{
			"ancestor(?x, ?y) :- parent(?x, ?z), ancestor(?z, ?y).",
			Rule{
				Body: []Pattern{{"?x", "parent", "?z"}, {"?z", "ancestor", "?y"}},
				Head: Pattern{"?x", "ancestor", "?y"},
			},
		},
		{ // no trailing period, ragged whitespace
			"  grandparent( ?x ,?y )  :-  parent(?x,?z) ,parent(?z,?y)  ",
			Rule{
				Body: []Pattern{{"?x", "parent", "?z"}, {"?z", "parent", "?y"}},
				Head: Pattern{"?x", "grandparent", "?y"},
			},
		},
		{ // constants may be capitalised and may contain spaces
			"rival(?x, Acme Corp) :- works_at(?x, Globex Inc).",
			Rule{
				Body: []Pattern{{"?x", "works_at", "Globex Inc"}},
				Head: Pattern{"?x", "rival", "Acme Corp"},
			},
		},
		{ // a variable predicate is a pattern like any other
			"seen(?s, ?o) :- ?p(?s, ?o).",
			Rule{Body: []Pattern{{"?s", "?p", "?o"}}, Head: Pattern{"?s", "seen", "?o"}},
		},
	} {
		got, err := Parse(c.in)
		if err != nil {
			t.Errorf("Parse(%q): %v", c.in, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Parse(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestParseRejects(t *testing.T) {
	for _, c := range []struct{ name, in string }{
		{"no implication", "ancestor(?x, ?y), parent(?x, ?y)."},
		{"empty body", "ancestor(?x, ?y) :- ."},
		{"unary head", "person(?x) :- type(?x, person)."},
		{"unary body atom", "person(?x, ?y) :- human(?x)."},
		{"unclosed atom", "ancestor(?x, ?y) :- parent(?x, ?y."},
		{"body atom with no terms", "ancestor(?x, ?y) :- parent"},
		{"no predicate", "ancestor(?x, ?y) :- (?x, ?y)."},
		{"two heads", "a(?x, ?y), b(?x, ?y) :- parent(?x, ?y)."},
		{"unbound head variable", "ancestor(?x, ?z) :- parent(?x, ?y)."},
		{"nothing at all", ""},
	} {
		if _, err := Parse(c.in); !errors.Is(err, ErrRule) {
			t.Errorf("%s: Parse(%q) error = %v, want ErrRule", c.name, c.in, err)
		}
	}
}

func TestRuleRoundTrip(t *testing.T) {
	for _, in := range []string{
		"ancestor(?x, ?y) :- parent(?x, ?z), ancestor(?z, ?y).",
		"child(?y, ?x) :- type(?x, Person), parent(?x, ?y).",
	} {
		first, err := Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", in, err)
		}
		if got := first.String(); got != in {
			t.Errorf("String() = %q, want %q", got, in)
		}
		second, err := Parse(first.String())
		if err != nil {
			t.Fatalf("Parse(String()): %v", err)
		}
		if !reflect.DeepEqual(first, second) {
			t.Errorf("round trip changed the rule: %+v then %+v", first, second)
		}
	}
}

func TestCheck(t *testing.T) {
	for _, c := range []struct {
		name string
		rule Rule
		ok   bool
	}{
		{"sound", Rule{Body: []Pattern{{"?x", "p", "?y"}}, Head: Pattern{"?y", "q", "?x"}}, true},
		{"no body", Rule{Head: Pattern{"?x", "q", "?y"}}, false},
		{"head variable unbound", Rule{Body: []Pattern{{"?x", "p", "?y"}}, Head: Pattern{"?x", "q", "?z"}}, false},
		{"head has no object", Rule{Body: []Pattern{{"?x", "p", "?y"}}, Head: Pattern{"?x", "q", ""}}, false},
		{"body atom has no predicate", Rule{Body: []Pattern{{"?x", " ", "?y"}}, Head: Pattern{"?x", "q", "?y"}}, false},
		{"body atom has no subject", Rule{Body: []Pattern{{"", "p", "?y"}}, Head: Pattern{"?y", "q", "?y"}}, false},
		{"score out of range", Rule{Body: []Pattern{{"?x", "p", "?y"}}, Head: Pattern{"?x", "q", "?y"}, Score: 1.5}, false},
		{"constant head is bound already", Rule{Body: []Pattern{{"?x", "p", "?y"}}, Head: Pattern{"?x", "q", "thing"}}, true},
	} {
		err := c.rule.Check()
		if (err == nil) != c.ok {
			t.Errorf("%s: Check() = %v, want ok=%v", c.name, err, c.ok)
		}
		if err != nil && !errors.Is(err, ErrRule) {
			t.Errorf("%s: Check() error = %v, want ErrRule", c.name, err)
		}
	}
}

func TestBuiltins(t *testing.T) {
	for _, c := range []struct {
		rule Rule
		want string
	}{
		{Transitive("part_of"), "part_of(?x, ?z) :- part_of(?x, ?y), part_of(?y, ?z)."},
		{Symmetric("sibling"), "sibling(?y, ?x) :- sibling(?x, ?y)."},
		{Inverse("parent", "child"), "child(?y, ?x) :- parent(?x, ?y)."},
	} {
		if err := c.rule.Check(); err != nil {
			t.Errorf("%s: Check() = %v", c.rule.Name, err)
		}
		if got := c.rule.String(); got != c.want {
			t.Errorf("%s = %q, want %q", c.rule.Name, got, c.want)
		}
		if c.rule.Name == "" {
			t.Errorf("%q: built-in rules carry a name", c.rule)
		}
	}
}

func TestLabel(t *testing.T) {
	named := Rule{Name: "transitive p", Body: []Pattern{{"?x", "p", "?y"}}, Head: Pattern{"?x", "p", "?y"}}
	if got := named.label(); got != "transitive p" {
		t.Errorf("label() = %q, want the name", got)
	}
	bare := Rule{Body: []Pattern{{"?x", "p", "?y"}}, Head: Pattern{"?y", "q", "?x"}}
	if got, want := bare.label(), bare.String(); got != want {
		t.Errorf("label() = %q, want %q — an unnamed rule explains itself", got, want)
	}
}

func TestPatternMatch(t *testing.T) {
	fact := semantic.Triple{Subject: "tom", Predicate: "parent", Object: "bob"}
	for _, c := range []struct {
		name string
		p    Pattern
		in   Bind
		want Bind
		ok   bool
	}{
		{"binds both ends", Pattern{"?x", "parent", "?y"}, nil, Bind{"x": "tom", "y": "bob"}, true},
		{"constant subject holds", Pattern{"tom", "parent", "?y"}, nil, Bind{"y": "bob"}, true},
		{"constant subject differs", Pattern{"ann", "parent", "?y"}, nil, nil, false},
		{"predicate differs", Pattern{"?x", "child", "?y"}, nil, nil, false},
		{"agrees with a binding", Pattern{"?x", "parent", "?y"}, Bind{"x": "tom"}, Bind{"x": "tom", "y": "bob"}, true},
		{"conflicts with a binding", Pattern{"?x", "parent", "?y"}, Bind{"x": "ann"}, nil, false},
		{"same variable twice", Pattern{"?x", "parent", "?x"}, nil, nil, false},
		{"variable predicate", Pattern{"?x", "?p", "?y"}, nil, Bind{"x": "tom", "p": "parent", "y": "bob"}, true},
		{"bare ? is a constant", Pattern{"?", "parent", "?y"}, nil, nil, false},
	} {
		got, ok := c.p.Match(fact, c.in)
		if ok != c.ok {
			t.Errorf("%s: Match ok = %v, want %v", c.name, ok, c.ok)
			continue
		}
		if ok && !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: Match = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestMatchLeavesCallerBindings(t *testing.T) {
	in := Bind{"x": "tom"}
	p := Pattern{"?x", "parent", "?y"}
	if _, ok := p.Match(semantic.Triple{Subject: "tom", Predicate: "parent", Object: "bob"}, in); !ok {
		t.Fatal("match failed")
	}
	if !reflect.DeepEqual(in, Bind{"x": "tom"}) {
		t.Errorf("caller bindings were written to: %v", in)
	}
	if _, ok := p.Match(semantic.Triple{Subject: "ann", Predicate: "parent", Object: "bob"}, in); ok {
		t.Fatal("match should have failed")
	}
	if !reflect.DeepEqual(in, Bind{"x": "tom"}) {
		t.Errorf("a failed match wrote to the caller's bindings: %v", in)
	}
}

func TestFill(t *testing.T) {
	p := Pattern{"?x", "knows", "?y"}
	got, ok := p.Fill(Bind{"x": "ada", "y": "bob"})
	if !ok {
		t.Fatal("Fill reported an unbound variable where all were bound")
	}
	want := semantic.Triple{Subject: "ada", Predicate: "knows", Object: "bob"}
	if got != want {
		t.Errorf("Fill = %+v, want %+v", got, want)
	}
	for _, c := range []struct {
		name string
		p    Pattern
		b    Bind
	}{
		{"subject unbound", Pattern{"?x", "knows", "bob"}, Bind{}},
		{"predicate unbound", Pattern{"ada", "?p", "bob"}, Bind{}},
		{"object unbound", Pattern{"?x", "knows", "?y"}, Bind{"x": "ada"}},
	} {
		if _, ok := c.p.Fill(c.b); ok {
			t.Errorf("%s: Fill accepted an unbound variable; a triple with a hole is not a fact", c.name)
		}
	}
}

func TestVars(t *testing.T) {
	got := Pattern{"?x", "?p", "?x"}.Vars()
	if want := []string{"x", "p"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Vars = %v, want %v", got, want)
	}
	if got := (Pattern{"a", "b", "c"}).Vars(); got != nil {
		t.Errorf("Vars over constants = %v, want none", got)
	}
}

func TestTreeAncestors(t *testing.T) {
	tree := Tree{"dog": "mammal", "mammal": "animal", "animal": "thing"}
	for _, c := range []struct {
		term string
		want []string
	}{
		{"dog", []string{"mammal", "animal", "thing"}},
		{"mammal", []string{"animal", "thing"}},
		{"thing", nil},
		{"fish", nil},
	} {
		if got := tree.Ancestors(c.term); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Ancestors(%q) = %v, want %v", c.term, got, c.want)
		}
	}
}

func TestTreeCycle(t *testing.T) {
	tree := Tree{"a": "b", "b": "c", "c": "a"}
	got := tree.Ancestors("a")
	if want := []string{"b", "c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Ancestors through a cycle = %v, want %v", got, want)
	}
}

func TestKey(t *testing.T) {
	a := semantic.Triple{Subject: "tom", Predicate: "parent", Object: "bob"}
	b := semantic.Triple{Subject: "tom", Predicate: "parent", Object: "bob", Score: 0.5}
	if Key(a) != Key(b) {
		t.Error("confidence changed the key; the same claim must key the same")
	}
	c := semantic.Triple{Subject: "tom parent", Predicate: "bob", Object: "x"}
	if Key(a) == Key(c) {
		t.Error("two different claims collided on one key")
	}
}
