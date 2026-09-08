package agent

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Policy is a named, versioned set of tests a decision has to pass. Rules are
// data rather than code so a policy can be written, versioned and audited by
// people who do not write Go, which is who usually owns them.
//
// A rule's name says what it tests and of what:
//
//	min_x   the decision's x is at least the bound
//	max_x   the decision's x is at most the bound
//	has_x   x carries every value listed, or equals the bound if it is single
//	one_x   x is one of the values listed
//
// Any other name requires only that the field be there at all, which is how a
// policy says "this must have been considered" without saying what the answer
// had to be.
//
// x is looked for in the decision's properties first, then in a property
// whose name ends in x — so a rule on status finds a verification_status —
// and finally among the decision's own fields: topic, case, reason, choice,
// confidence, agent, rule.
type Policy struct {
	ID      string
	Name    string
	Topic   string // the decisions it governs; empty governs every topic
	Version string // major.minor, and optionally more parts and a label
	Text    string // what it is for, in words
	Rules   map[string]any
	At      time.Time
}

// Waiver lets one decision past one policy on somebody's authority. An
// exception that is written down stays an exception; one that is not becomes
// a quiet edit to the rule, and then nobody knows what the rule was.
type Waiver struct {
	Decision string
	Policy   string
	Why      string
	By       string
	At       time.Time
}

// Rules holds policies and answers whether a decision passes them. Every
// answer comes with the reason, because a refusal without one cannot be
// argued with, appealed, or fixed.
//
// Its zero value is an empty book, and it is safe for concurrent use. It
// satisfies decision.Policy.
type Rules struct {
	mu     sync.RWMutex
	by     map[string][]Policy // id, every version, oldest first
	order  []string            // ids in the order they were first added
	waived []Waiver
}

var version = regexp.MustCompile(`^\d+\.\d+(\.\d+)*(-[A-Za-z0-9]+)?$`)

// Add writes a policy. It fills in an id and a first version when the caller
// left them empty, and refuses a version that already exists — a rule that
// changed under a version anyone has already been judged by is not the same
// rule.
func (r *Rules) Add(p Policy) (Policy, error) {
	if p.Name == "" {
		return Policy{}, fmt.Errorf("agent: policy needs a name")
	}
	if len(p.Rules) == 0 {
		return Policy{}, fmt.Errorf("agent: policy %q has no rules", p.Name)
	}
	if err := check(p.Rules); err != nil {
		return Policy{}, err
	}
	if p.Version == "" {
		p.Version = "1.0"
	}
	if !version.MatchString(p.Version) {
		return Policy{}, fmt.Errorf("agent: %q is not a version", p.Version)
	}
	if p.ID == "" {
		p.ID = mint("policy")
	}
	if p.At.IsZero() {
		p.At = time.Now().UTC()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.by == nil {
		r.by = map[string][]Policy{}
	}
	for _, old := range r.by[p.ID] {
		if old.Version == p.Version {
			return Policy{}, fmt.Errorf("agent: policy %s already has version %s", p.ID, p.Version)
		}
	}
	if len(r.by[p.ID]) == 0 {
		r.order = append(r.order, p.ID)
	}
	r.by[p.ID] = append(r.by[p.ID], p)
	return p, nil
}

// Revise writes the next version of a policy: the same name and topic, new
// rules, the last version's number with its final part raised by one. The
// version it replaces stays readable, which is what makes a decision taken
// last year still explainable this year.
func (r *Rules) Revise(id string, rules map[string]any) (Policy, error) {
	last, ok := r.Get(id, "")
	if !ok {
		return Policy{}, fmt.Errorf("agent: no policy %s", id)
	}
	next := last
	next.Rules = rules
	next.Version = bump(last.Version)
	next.At = time.Now().UTC()
	return r.Add(next)
}

// bump raises the last numeric part of a version by one.
func bump(v string) string {
	parts := strings.Split(v, ".")
	last := parts[len(parts)-1]
	n, err := strconv.Atoi(last)
	if err != nil {
		return v + ".1"
	}
	parts[len(parts)-1] = strconv.Itoa(n + 1)
	return strings.Join(parts, ".")
}

// Get reads one policy. An empty version reads the newest.
func (r *Rules) Get(id, want string) (Policy, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	all := r.by[id]
	if len(all) == 0 {
		return Policy{}, false
	}
	if want == "" {
		return all[len(all)-1], true
	}
	for _, p := range all {
		if p.Version == want {
			return p, true
		}
	}
	return Policy{}, false
}

// History is every version of one policy, oldest first.
func (r *Rules) History(id string) []Policy {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Policy(nil), r.by[id]...)
}

// For is the newest version of every policy that governs a topic, in the
// order the policies were first added.
func (r *Rules) For(topic string) []Policy {
	r.mu.RLock()
	ids := append([]string(nil), r.order...)
	r.mu.RUnlock()
	var out []Policy
	for _, id := range ids {
		p, ok := r.Get(id, "")
		if !ok || (p.Topic != "" && p.Topic != topic) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// Drop removes a policy and every version of it.
func (r *Rules) Drop(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.by[id]) == 0 {
		return false
	}
	delete(r.by, id)
	r.order = without(r.order, id)
	return true
}

// Waive lets one decision past one policy.
func (r *Rules) Waive(w Waiver) error {
	if w.Decision == "" || w.Policy == "" {
		return fmt.Errorf("agent: a waiver names a decision and a policy")
	}
	if w.By == "" {
		return fmt.Errorf("agent: a waiver names who granted it")
	}
	if w.At.IsZero() {
		w.At = time.Now().UTC()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.waived = append(r.waived, w)
	return nil
}

// Waivers lists every waiver granted, oldest first.
func (r *Rules) Waivers() []Waiver {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Waiver(nil), r.waived...)
}

func (r *Rules) waiver(decision, policy string) (Waiver, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, w := range r.waived {
		if w.Decision == decision && w.Policy == policy {
			return w, true
		}
	}
	return Waiver{}, false
}

// Verdict is what the rules said: whether the choice stands, which policy
// settled it, and why. The reason travels with the answer because a refusal
// nobody can read is a refusal nobody can appeal or fix.
type Verdict struct {
	OK     bool
	Policy string // the policy that settled it
	Why    string
}

// Check tests one decision against one policy. A waiver passes the decision
// and says on whose authority.
func (r *Rules) Check(d Decision, p Policy) Verdict {
	if w, ok := r.waiver(d.ID, p.ID); ok {
		return Verdict{OK: true, Policy: p.ID, Why: fmt.Sprintf("%s waived by %s: %s", p.Name, w.By, w.Why)}
	}
	for _, rule := range sorted(p.Rules) {
		if ok, why := test(d, rule, p.Rules[rule]); !ok {
			return Verdict{Policy: p.ID, Why: fmt.Sprintf("%s %s: %s", p.Name, p.Version, why)}
		}
	}
	return Verdict{OK: true, Policy: p.ID, Why: fmt.Sprintf("%s %s: passed", p.Name, p.Version)}
}

// Vet tests a decision against every policy that governs its topic. The first
// refusal is the answer, since a decision that fails one rule is refused
// whatever the others say.
func (r *Rules) Vet(d Decision) Verdict {
	all := r.For(d.Topic)
	if len(all) == 0 {
		return Verdict{OK: true, Why: "no policy governs " + d.Topic}
	}
	var passed []string
	last := ""
	for _, p := range all {
		v := r.Check(d, p)
		if !v.OK {
			return v
		}
		last = p.ID
		// Each policy's own words, not a tally: a decision that stands only
		// because somebody waived a rule has to say so here.
		passed = append(passed, v.Why)
	}
	return Verdict{OK: true, Policy: last, Why: strings.Join(passed, "; ")}
}

// Allow is decision.Policy's way in: the coarse question, asked with nothing
// but an agent and a choice. Two things follow from how little that is. Only
// rules about the choice itself can be answered, so a policy testing
// confidence or evidence is not consulted; and with no topic to go on, only
// policies that govern every topic are, since applying a lending rule to a
// medical choice would be worse than not asking. Vet is the full question.
func (r *Rules) Allow(ctx context.Context, agent, choice string) (bool, string) {
	if err := ctx.Err(); err != nil {
		return false, err.Error()
	}
	d := Decision{}
	d.Agent, d.Choice = agent, choice
	for _, p := range r.For("") {
		for _, rule := range sorted(p.Rules) {
			if !about(rule, "choice") {
				continue
			}
			if ok, why := test(d, rule, p.Rules[rule]); !ok {
				return false, fmt.Sprintf("%s %s: %s", p.Name, p.Version, why)
			}
		}
	}
	return true, fmt.Sprintf("no rule forbids %q", choice)
}

// about reports whether a rule tests the named field.
func about(rule, field string) bool {
	name, _ := split(rule)
	return name == field
}

// Sway is what changing a policy's rules would do to the decisions already
// taken under it. A rule change is cheap to write and expensive to mean, so
// the cost is worth seeing first.
type Sway struct {
	Policy   string
	Judged   int
	Affected int             // decisions that pass now and would not
	Fall     float64         // the share that would pass, less one: 0 costs nothing
	Risk     float64         // half the share that would fail
	Changed  map[string]Swap // the rules that differ
}

// Swap is one rule before and after.
type Swap struct{ Was, Now any }

// Sway measures a proposed change against decisions already taken.
func (r *Rules) Sway(id string, proposed map[string]any, ds []Decision) (Sway, error) {
	p, ok := r.Get(id, "")
	if !ok {
		return Sway{}, fmt.Errorf("agent: no policy %s", id)
	}
	s := Sway{Policy: id, Judged: len(ds), Changed: map[string]Swap{}}
	for k, was := range p.Rules {
		if now, ok := proposed[k]; !ok || !same(was, now) {
			s.Changed[k] = Swap{Was: was, Now: proposed[k]}
		}
	}
	for k, now := range proposed {
		if _, ok := p.Rules[k]; !ok {
			s.Changed[k] = Swap{Now: now}
		}
	}
	after := p
	after.Rules = proposed
	pass := 0
	for _, d := range ds {
		now, then := r.Check(d, p), r.Check(d, after)
		if then.OK {
			pass++
		}
		if now.OK && !then.OK {
			s.Affected++
		}
	}
	if len(ds) > 0 {
		share := float64(pass) / float64(len(ds))
		s.Fall = share - 1
		s.Risk = (1 - share) * 0.5
	}
	return s, nil
}

// test applies one rule to one decision and says why it failed.
func test(d Decision, rule string, bound any) (bool, string) {
	name, kind := split(rule)
	got, ok := field(d, name)
	if !ok {
		return false, fmt.Sprintf("%s is missing", name)
	}
	switch kind {
	case "min":
		if c, ok := compare(got, bound); !ok || c < 0 {
			return false, fmt.Sprintf("%s is %v, under %v", name, got, bound)
		}
	case "max":
		if c, ok := compare(got, bound); !ok || c > 0 {
			return false, fmt.Sprintf("%s is %v, over %v", name, got, bound)
		}
	case "has":
		for _, want := range list(bound) {
			if !carries(got, want) {
				return false, fmt.Sprintf("%s lacks %v", name, want)
			}
		}
	case "one":
		found := false
		for _, want := range list(bound) {
			if same(got, want) {
				found = true
				break
			}
		}
		if !found {
			return false, fmt.Sprintf("%s is %v, not one of %v", name, got, bound)
		}
	}
	return true, ""
}

// split reads a rule name as a test and the field it tests.
func split(rule string) (name, kind string) {
	for _, k := range [...]string{"min", "max", "has", "one"} {
		if strings.HasPrefix(rule, k+"_") {
			return rule[len(k)+1:], k
		}
	}
	return rule, ""
}

// field finds what a rule is about: a property of that name, a property whose
// name ends in it, or one of the decision's own fields.
func field(d Decision, name string) (any, bool) {
	if v, ok := d.Props[name]; ok {
		return v, true
	}
	for k, v := range d.Props {
		if strings.HasSuffix(k, "_"+name) {
			return v, true
		}
	}
	switch name {
	case "topic":
		return d.Topic, d.Topic != ""
	case "case":
		return d.Case, d.Case != ""
	case "reason":
		return d.Reason, d.Reason != ""
	case "choice":
		return d.Choice, d.Choice != ""
	case "confidence":
		return d.Confidence, true
	case "agent":
		return d.Agent, d.Agent != ""
	case "rule":
		return d.Rule, d.Rule != ""
	}
	return nil, false
}

// check reads a set of rules for the mistakes that make one meaningless.
func check(rules map[string]any) error {
	var bad []string
	for _, name := range sorted(rules) {
		v := rules[name]
		_, kind := split(name)
		switch kind {
		case "min", "max":
			if _, ok := number(v); !ok {
				bad = append(bad, fmt.Sprintf("%s wants a number, has %T", name, v))
			}
		case "one":
			if len(list(v)) == 0 {
				bad = append(bad, name+" wants a list of values")
			}
		}
		if strings.HasSuffix(name, "_ratio") {
			if f, ok := number(v); ok && (f < 0 || f > 1) {
				bad = append(bad, fmt.Sprintf("%s is %v, outside 0 to 1", name, v))
			}
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("agent: %s", strings.Join(bad, "; "))
	}
	return nil
}

func sorted(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// number reads a value as a number, if it is one.
func number(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

// compare orders two values: numbers by size, strings by their letters.
func compare(a, b any) (int, bool) {
	if x, ok := number(a); ok {
		if y, ok := number(b); ok {
			switch {
			case x < y:
				return -1, true
			case x > y:
				return 1, true
			}
			return 0, true
		}
		return 0, false
	}
	x, ok := a.(string)
	y, okb := b.(string)
	if !ok || !okb {
		return 0, false
	}
	return strings.Compare(x, y), true
}

func same(a, b any) bool {
	if c, ok := compare(a, b); ok {
		return c == 0
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

// list reads a value as the values it holds, or as itself when it is one.
func list(v any) []any {
	switch xs := v.(type) {
	case []any:
		return xs
	case []string:
		out := make([]any, len(xs))
		for i, x := range xs {
			out[i] = x
		}
		return out
	case nil:
		return nil
	}
	return []any{v}
}

// carries reports whether a field holds a value: a list that contains it, or
// a single value equal to it.
func carries(field, want any) bool {
	for _, have := range list(field) {
		if same(have, want) {
			return true
		}
	}
	return false
}
