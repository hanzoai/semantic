// Package reason derives the facts a set of triples implies but does not
// state: what a transitive predicate reaches, what a symmetric or inverse
// predicate reads backwards, what a class hierarchy entails, and whatever
// rules a caller writes.
//
// There is one way a fact gets derived — forward chaining to a fixpoint —
// so there is one place to look when asking why. Transitivity, symmetry and
// inverse are rules, not special cases, and a schema contributes the two
// entailments a hierarchy licenses. Every derived fact names the rule that
// produced it and the facts it rests on: an assertion nobody can explain is
// not evidence, and the point of deriving it was to answer for it later.
package reason

import (
	"errors"
	"maps"
	"slices"
	"strings"

	"github.com/hanzoai/semantic"
)

var (
	// ErrRule reports a rule that cannot fire: an empty body, a malformed
	// atom, or a head variable the body never binds.
	ErrRule = errors.New("reason: bad rule")

	// ErrDepth reports that derivation stopped at Engine.Depth with facts
	// still arriving. What was derived up to that point is sound, but the
	// closure is incomplete.
	ErrDepth = errors.New("reason: depth exhausted")

	// ErrMissing reports a key no fact in the model carries.
	ErrMissing = errors.New("reason: no such fact")
)

// Type is the predicate read as a statement of class when Engine.Type is
// empty, and the predicate subclass entailment walks.
const Type = "type"

// sep joins the parts of a Key. It is a control character, so no term can
// contain it and no two distinct triples can collide on one key.
const sep = "\x1f"

// Key names an assertion. Two triples with the same subject, predicate and
// object are the same claim however each was arrived at, so the key is what
// the fixpoint stops on and what a premise points at. Terms are used as
// written: fold names before reasoning if the source spells them differently.
func Key(t semantic.Triple) string {
	return t.Subject + sep + t.Predicate + sep + t.Object
}

// Bind maps a variable name, written without its leading '?', to the term it
// matched.
type Bind map[string]string

// Pattern is a triple with variables. A term beginning with '?' is a
// variable and binds whatever stands in its place; every other term matches
// exactly. A variable used twice in one rule must match the same term both
// times, which is what joins the atoms of a body together.
type Pattern struct{ Subject, Predicate, Object string }

// String renders the pattern as predicate(subject, object), the form Parse
// reads.
func (p Pattern) String() string { return p.Predicate + "(" + p.Subject + ", " + p.Object + ")" }

// Vars lists the variables in the pattern, subject first, without repeats.
func (p Pattern) Vars() []string {
	var out []string
	for _, term := range [3]string{p.Subject, p.Predicate, p.Object} {
		name, ok := variable(term)
		if !ok || contains(out, name) {
			continue
		}
		out = append(out, name)
	}
	return out
}

// Fill substitutes the bindings into the pattern. It reports false when a
// variable is unbound, because a triple with a hole in it is not a fact.
func (p Pattern) Fill(b Bind) (semantic.Triple, bool) {
	s, ok := fill(p.Subject, b)
	if !ok {
		return semantic.Triple{}, false
	}
	pred, ok := fill(p.Predicate, b)
	if !ok {
		return semantic.Triple{}, false
	}
	o, ok := fill(p.Object, b)
	if !ok {
		return semantic.Triple{}, false
	}
	return semantic.Triple{Subject: s, Predicate: pred, Object: o}, true
}

// Match extends the bindings with what this pattern binds against a triple,
// and reports whether the two agree. The bindings passed in are never
// written to: a failed match must leave the caller's state alone.
func (p Pattern) Match(t semantic.Triple, b Bind) (Bind, bool) {
	out, own := b, false
	pairs := [3][2]string{
		{p.Subject, t.Subject},
		{p.Predicate, t.Predicate},
		{p.Object, t.Object},
	}
	for _, pair := range pairs {
		term, value := pair[0], pair[1]
		name, ok := variable(term)
		if !ok {
			if term != value {
				return nil, false
			}
			continue
		}
		if seen, ok := out[name]; ok {
			if seen != value {
				return nil, false
			}
			continue
		}
		if !own {
			out, own = clone(b), true
		}
		out[name] = value
	}
	return out, true
}

// Fact is a triple and how it got there. A given fact names no rule and
// rests on nothing; a derived one names both, so it can be answered for.
type Fact struct {
	semantic.Triple

	Rule     string   // what derived it; empty when it was given
	Premises []string // keys of the facts it was derived from, in body order
	Depth    int      // rounds of derivation behind it; zero when it was given
}

// Key names the assertion this fact carries.
func (f Fact) Key() string { return Key(f.Triple) }

// String renders the fact as predicate(subject, object).
func (f Fact) String() string { return f.Predicate + "(" + f.Subject + ", " + f.Object + ")" }

// Schema is the one thing entailment needs from an ontology: the terms a
// term specialises, nearest first. An unknown term has none. Engine reads a
// schema twice over, once for classes and once for properties, because those
// are two hierarchies and conflating them would entail things nobody said.
//
// The method is named for what an ontology already calls this, so a schema
// that answers Ancestors satisfies it as it stands. Keeping it an interface
// is what lets reasoning depend on a hierarchy without depending on where
// the hierarchy is kept.
type Schema interface {
	Ancestors(term string) []string
}

// Tree is a hierarchy written child to parent, the common case and the
// smallest thing that satisfies Schema.
type Tree map[string]string

// Ancestors walks the parents of a term, nearest first. A cycle stops the walk
// rather than hanging it.
func (t Tree) Ancestors(term string) []string {
	var out []string
	seen := map[string]bool{term: true}
	for cur := t[term]; cur != "" && !seen[cur]; cur = t[cur] {
		seen[cur] = true
		out = append(out, cur)
	}
	return out
}

// variable returns the name a term stands for, and whether it is a variable
// at all. A bare "?" names nothing, so it is a constant.
func variable(term string) (string, bool) {
	if len(term) > 1 && term[0] == '?' {
		return term[1:], true
	}
	return "", false
}

// fill resolves one term against the bindings.
func fill(term string, b Bind) (string, bool) {
	name, ok := variable(term)
	if !ok {
		return term, true
	}
	value, bound := b[name]
	return value, bound
}

func clone(b Bind) Bind {
	if b == nil {
		return Bind{}
	}
	return maps.Clone(b)
}

func contains(list []string, s string) bool {
	return slices.Contains(list, s)
}

func blank(s string) bool { return strings.TrimSpace(s) == "" }
