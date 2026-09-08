package reason

import (
	"context"
	"fmt"
	"time"

	"github.com/hanzoai/semantic"
)

// DefaultDepth bounds derivation when Engine.Depth is zero. Rules only ever
// recombine terms that are already present, so a program does reach a
// fixpoint on its own; the bound is what keeps a large one from running away
// and what lets a caller ask for shallow entailment on purpose.
const DefaultDepth = 32

// Engine derives what a set of triples implies. The zero value derives
// nothing, which is the honest answer when it has been given no rule and no
// schema.
type Engine struct {
	Rules []Rule
	Class Schema // subclass hierarchy: x type C, and C under P, give x type P
	Prop  Schema // subproperty hierarchy: p(x, y), and p under q, give q(x, y)
	Type  string // the predicate that states a class; empty means "type"
	Depth int    // rounds of derivation allowed; zero means DefaultDepth
}

// rounds is how many rounds this engine may run before it must stop.
func (e Engine) rounds() int {
	if e.Depth <= 0 {
		return DefaultDepth
	}
	return e.Depth
}

// Run derives everything the rules and the schema entail, and returns the
// model: the facts it was given, then the facts it derived, each carrying
// its derivation.
//
// A rule that cannot fire is reported before any work is done. Stopping
// early — at Engine.Depth, or on a cancelled context — returns the partial
// model alongside the error, because facts already derived are still sound
// and still explained; a caller that needs the whole closure must check the
// error rather than the fact count.
//
// Reaching the bound with the last round still adding facts reports
// ErrDepth whether or not another round would have found anything, since the
// run stopped before it could tell. Certifying that a closure is complete
// therefore takes one more round than producing it.
func (e Engine) Run(ctx context.Context, facts []semantic.Triple) (*Model, error) {
	for i, r := range e.Rules {
		if err := r.Check(); err != nil {
			return nil, fmt.Errorf("reason: rule %d: %w", i+1, err)
		}
	}
	m := seed(facts)
	m.start = time.Now()
	defer func() { m.end = time.Now() }()

	depth := e.rounds()
	// Each round works only where the round before it changed something:
	// facts are appended, so the previous round's output is the contiguous
	// range [lo, hi). A round that adds nothing leaves lo == hi, and that is
	// the fixpoint.
	for round, lo, hi := 1, 0, len(m.facts); lo < hi; round, lo, hi = round+1, hi, len(m.facts) {
		if round > depth {
			return m, fmt.Errorf("%w: stopped after %d rounds, with %d facts from the last one not carried further",
				ErrDepth, depth, hi-lo)
		}
		if err := ctx.Err(); err != nil {
			return m, fmt.Errorf("reason: %w", err)
		}
		for _, r := range e.Rules {
			e.fire(m, r, lo, hi, round)
		}
		e.entail(m, lo, hi, round)
	}
	return m, nil
}

// fire applies one rule, with each body atom in turn required to match a
// fact from the last round. Restricting one atom to the new facts is what
// keeps a round to the work the round before it made possible; the rest of
// the body still ranges over everything known.
func (e Engine) fire(m *Model, r Rule, lo, hi, round int) {
	for i := range r.Body {
		for _, s := range m.solve(r.Body, i, lo, hi) {
			t, ok := r.Head.Fill(s.bind)
			if !ok {
				continue // Check proved the body binds every head variable
			}
			premises, weight := m.support(s.from...)
			t.Score = weight * r.weight()
			m.add(Fact{Triple: t, Rule: r.label(), Premises: premises, Depth: round})
		}
	}
}

// entail derives what the hierarchies license from the facts of the last
// round: a class stands for its ancestors, a property for the properties it
// specialises. The premise recorded is the fact itself, not the chain of
// intermediate classes, because the fact and the schema are what the
// derivation actually rests on.
func (e Engine) entail(m *Model, lo, hi, round int) {
	class, prop := e.Class, e.Prop
	if class == nil && prop == nil {
		return
	}
	kind := e.Type
	if kind == "" {
		kind = Type
	}
	for i := lo; i < hi; i++ {
		f := m.facts[i]
		premises, weight := m.support(i)
		if class != nil && f.Predicate == kind {
			for _, up := range class.Ancestors(f.Object) {
				t := semantic.Triple{Subject: f.Subject, Predicate: f.Predicate, Object: up, Score: weight}
				m.add(Fact{Triple: t, Rule: "subclass", Premises: premises, Depth: round})
			}
		}
		if prop != nil {
			for _, up := range prop.Ancestors(f.Predicate) {
				t := semantic.Triple{Subject: f.Subject, Predicate: up, Object: f.Object, Score: weight}
				m.add(Fact{Triple: t, Rule: "subproperty", Premises: premises, Depth: round})
			}
		}
	}
}

// bound is one partial answer: what the body has bound so far and the facts
// that bound it.
type bound struct {
	bind Bind
	from []int
}

// solve joins the body atom by atom. The atom at position only must match a
// fact from [lo, hi); every other atom ranges over everything known.
func (m *Model) solve(body []Pattern, only, lo, hi int) []bound {
	cur := []bound{{bind: Bind{}}}
	for i, p := range body {
		from, to := 0, hi
		if i == only {
			from = lo
		}
		var next []bound
		for _, c := range cur {
			for _, j := range m.candidates(p, from, to) {
				b, ok := p.Match(m.facts[j].Triple, c.bind)
				if !ok {
					continue
				}
				next = append(next, bound{bind: b, from: append(append([]int(nil), c.from...), j)})
			}
		}
		if cur = next; len(cur) == 0 {
			return nil
		}
	}
	return cur
}

// candidates lists the facts in [from, to) a pattern could match, narrowed
// by predicate when the pattern names one.
func (m *Model) candidates(p Pattern, from, to int) []int {
	if _, isVar := variable(p.Predicate); isVar {
		out := make([]int, 0, to-from)
		for i := from; i < to; i++ {
			out = append(out, i)
		}
		return out
	}
	var out []int
	for _, i := range m.pred[p.Predicate] {
		if i >= from && i < to {
			out = append(out, i)
		}
	}
	return out
}

// support returns the keys of the facts a derivation rests on, in body
// order and without repeats, and the confidence they carry: the weakest of
// them, since a conclusion is no better supported than its weakest premise.
// A fact stating no confidence is not stating no confidence, so a zero score
// counts as 1.
func (m *Model) support(from ...int) ([]string, float64) {
	keys := make([]string, 0, len(from))
	weight := 1.0
	for _, i := range from {
		f := m.facts[i]
		if k := f.Key(); !contains(keys, k) {
			keys = append(keys, k)
		}
		score := f.Score
		if score == 0 {
			score = 1
		}
		if score < weight {
			weight = score
		}
	}
	return keys, weight
}
