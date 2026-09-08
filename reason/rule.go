package reason

import (
	"fmt"
	"strings"
)

// Rule derives Head from every binding that satisfies the whole of Body at
// once. Variables are shared across the rule, so an atom that reuses a
// variable narrows the match rather than widening it.
//
// A rule with no Name explains itself: what a derived fact records is the
// rule written out.
type Rule struct {
	Name  string
	Body  []Pattern
	Head  Pattern
	Score float64 // weight of what it derives; zero means 1
}

// Check reports whether the rule can fire: something to match, something to
// derive, and no head variable the body leaves unbound. An unbound head
// variable would derive a fact with a hole in it, which is why this is an
// error and not a warning.
func (r Rule) Check() error {
	if len(r.Body) == 0 {
		return fmt.Errorf("%w: no body", ErrRule)
	}
	bound := map[string]bool{}
	for i, p := range r.Body {
		if err := atomOK(p); err != nil {
			return fmt.Errorf("%w: body %d: %s", ErrRule, i+1, err)
		}
		for _, v := range p.Vars() {
			bound[v] = true
		}
	}
	if err := atomOK(r.Head); err != nil {
		return fmt.Errorf("%w: head: %s", ErrRule, err)
	}
	for _, v := range r.Head.Vars() {
		if !bound[v] {
			return fmt.Errorf("%w: head variable ?%s is not bound by the body", ErrRule, v)
		}
	}
	if r.Score < 0 || r.Score > 1 {
		return fmt.Errorf("%w: score %v is outside [0,1]", ErrRule, r.Score)
	}
	return nil
}

// String renders the rule in the Horn-clause form Parse reads.
func (r Rule) String() string {
	body := make([]string, len(r.Body))
	for i, p := range r.Body {
		body[i] = p.String()
	}
	return r.Head.String() + " :- " + strings.Join(body, ", ") + "."
}

// label is what a derived fact records as its rule: the given name, or the
// rule itself when it was not named.
func (r Rule) label() string {
	if r.Name != "" {
		return r.Name
	}
	return r.String()
}

// weight is the confidence the rule lends what it derives.
func (r Rule) weight() float64 {
	if r.Score == 0 {
		return 1
	}
	return r.Score
}

// Transitive returns the rule that closes a predicate under composition:
// p(x, y) and p(y, z) give p(x, z).
func Transitive(pred string) Rule {
	return Rule{
		Name: "transitive " + pred,
		Body: []Pattern{{"?x", pred, "?y"}, {"?y", pred, "?z"}},
		Head: Pattern{"?x", pred, "?z"},
	}
}

// Symmetric returns the rule that reads a predicate both ways: p(x, y) gives
// p(y, x).
func Symmetric(pred string) Rule {
	return Rule{
		Name: "symmetric " + pred,
		Body: []Pattern{{"?x", pred, "?y"}},
		Head: Pattern{"?y", pred, "?x"},
	}
}

// Inverse returns the rule from(x, y) gives to(y, x). Being inverse is
// mutual but a rule is not, so add Inverse(to, from) as well to read the
// pair in both directions.
func Inverse(from, to string) Rule {
	return Rule{
		Name: "inverse " + from + " " + to,
		Body: []Pattern{{"?x", from, "?y"}},
		Head: Pattern{"?y", to, "?x"},
	}
}

// Parse reads a rule in Horn-clause form:
//
//	ancestor(?x, ?y) :- parent(?x, ?z), ancestor(?z, ?y).
//
// An atom is a triple written predicate(subject, object), and a term
// beginning with '?' is a variable. Datalog's convention of capitalising
// variables is not used here: the terms are names out of real data and many
// of them are capitalised already. The trailing period is optional.
func Parse(s string) (Rule, error) {
	text := strings.TrimSpace(s)
	text = strings.TrimSuffix(text, ".")
	head, body, ok := strings.Cut(text, ":-")
	if !ok {
		return Rule{}, fmt.Errorf("%w: %q has no :-", ErrRule, s)
	}
	heads, err := atoms(head)
	if err != nil {
		return Rule{}, fmt.Errorf("%w: head: %s", ErrRule, err)
	}
	if len(heads) != 1 {
		return Rule{}, fmt.Errorf("%w: head: want one atom, got %d", ErrRule, len(heads))
	}
	rest, err := atoms(body)
	if err != nil {
		return Rule{}, fmt.Errorf("%w: body: %s", ErrRule, err)
	}
	rule := Rule{Body: rest, Head: heads[0]}
	if err := rule.Check(); err != nil {
		return Rule{}, err
	}
	return rule, nil
}

// atoms reads a comma-separated conjunction of predicate(subject, object)
// atoms. Commas inside an atom separate its terms, so the scan follows the
// parentheses rather than splitting the text on commas.
func atoms(s string) ([]Pattern, error) {
	var out []Pattern
	rest := s
	for {
		rest = strings.TrimLeft(rest, " \t\n,")
		if rest == "" {
			return out, nil
		}
		open := strings.IndexByte(rest, '(')
		if open < 0 {
			return nil, fmt.Errorf("%q: want predicate(subject, object)", strings.TrimSpace(rest))
		}
		shut := strings.IndexByte(rest[open:], ')')
		if shut < 0 {
			return nil, fmt.Errorf("%q: unclosed (", strings.TrimSpace(rest))
		}
		shut += open
		pred := strings.TrimSpace(rest[:open])
		args := strings.Split(rest[open+1:shut], ",")
		if len(args) != 2 {
			return nil, fmt.Errorf("%q: want two terms, got %d", strings.TrimSpace(rest[:shut+1]), len(args))
		}
		p := Pattern{
			Subject:   strings.TrimSpace(args[0]),
			Predicate: pred,
			Object:    strings.TrimSpace(args[1]),
		}
		if err := atomOK(p); err != nil {
			return nil, err
		}
		out = append(out, p)
		rest = rest[shut+1:]
	}
}

// atomOK reports the terms an atom cannot do without.
func atomOK(p Pattern) error {
	switch {
	case blank(p.Predicate):
		return fmt.Errorf("%s: no predicate", p)
	case blank(p.Subject):
		return fmt.Errorf("%s: no subject", p)
	case blank(p.Object):
		return fmt.Errorf("%s: no object", p)
	}
	return nil
}
