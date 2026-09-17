package extract

import (
	"regexp"
	"sort"
	"strings"
)

// Coreference: which words in a text point at the same thing. It runs between
// the entity pass and the relation pass rather than after both, because
// "he founded it" is only a relation between two names once the pronouns are
// resolved to them.

// Mention is one reference to a thing in a text: a name the entity pass
// found, or a pronoun standing in for one.
type Mention struct {
	Text    string
	Start   int
	End     int
	Pronoun bool
	Label   string // the entity label, for a mention that came from an entity
	Of      string // what this mention refers to, once Refer has resolved it
}

// Chain is the mentions that name one thing. Head is the mention that names
// it best: a name rather than a pronoun, and the earliest of those.
type Chain struct {
	Mentions []Mention
	Head     Mention
	Label    string
}

// Ref is a pronoun and the mention it was resolved to.
type Ref struct {
	Pronoun string
	Of      string
}

// Coref is the whole pass over a text: its mentions, the pronouns among them
// resolved, and the chains that result.
func Coref(text string, known []Entity) []Chain {
	ms := Mentions(text, known)
	Refer(ms)
	return Chains(ms)
}

// Mentions finds every reference in text: the pronouns it contains, plus the
// entities already found, ordered by position.
func Mentions(text string, known []Entity) []Mention {
	var out []Mention
	for _, at := range pronouns.FindAllStringIndex(text, -1) {
		out = append(out, Mention{
			Text: text[at[0]:at[1]], Start: at[0], End: at[1], Pronoun: true,
		})
	}
	for _, e := range known {
		out = append(out, Mention{
			Text: e.Text, Start: e.Start, End: e.End, Label: e.Label,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Start != out[j].Start {
			return out[i].Start < out[j].Start
		}
		return out[i].End < out[j].End
	})
	return out
}

// Refer resolves each pronoun to the nearest preceding name whose label the
// pronoun admits, and to the nearest preceding name of any label when none
// does — a wrong antecedent of the right shape is worth more to a reader than
// no antecedent, and the label is on the chain for a caller who wants to
// check. It records what it settled on each pronoun mention, which is what
// Chains reads, and returns the pairs.
func Refer(ms []Mention) []Ref {
	var out []Ref
	for i, m := range ms {
		if !m.Pronoun {
			continue
		}
		kinds := admits[strings.ToLower(m.Text)]
		near, fit := -1, -1
		for j := range ms {
			if ms[j].Pronoun || ms[j].End > m.Start {
				continue
			}
			near = j
			if has(kinds, ms[j].Label) {
				fit = j
			}
		}
		if fit >= 0 {
			near = fit
		}
		if near < 0 {
			continue
		}
		ms[i].Of = ms[near].Text
		out = append(out, Ref{Pronoun: m.Text, Of: ms[near].Text})
	}
	return out
}

// Chains groups the mentions that name one thing: names that match, and every
// pronoun Refer bound to one of them. A thing mentioned once is not a chain,
// so the result holds only what was said more than once.
func Chains(ms []Mention) []Chain {
	taken := make([]bool, len(ms))
	var out []Chain
	for i := range ms {
		if taken[i] {
			continue
		}
		taken[i] = true
		group := []Mention{ms[i]}
		for j := i + 1; j < len(ms); j++ {
			if taken[j] || !alike(ms[i], ms[j]) {
				continue
			}
			taken[j] = true
			group = append(group, ms[j])
		}
		if len(group) < 2 {
			continue
		}
		head := group[0]
		for _, m := range group[1:] {
			if m.Pronoun {
				continue
			}
			if head.Pronoun || m.Start < head.Start {
				head = m
			}
		}
		out = append(out, Chain{Mentions: group, Head: head, Label: head.Label})
	}
	return out
}

// alike reports whether two mentions name the same thing. Names are compared
// as text; a pronoun joins only through the antecedent Refer gave it, since a
// pronoun's own letters say nothing — comparing those as text is what makes
// "it" a mention of "Bitcoin".
func alike(a, b Mention) bool {
	if !a.Pronoun && !b.Pronoun {
		return same(a.Text, b.Text)
	}
	if a.Of != "" && (same(a.Of, b.Text) || (b.Of != "" && same(a.Of, b.Of))) {
		return true
	}
	return b.Of != "" && (same(b.Of, a.Text) || (a.Of != "" && same(b.Of, a.Of)))
}

// same reports whether two surface forms name one thing: equal, one inside
// the other, or more than seven words in ten shared.
func same(a, b string) bool {
	x, y := strings.ToLower(strings.TrimSpace(a)), strings.ToLower(strings.TrimSpace(b))
	if x == "" || y == "" {
		return false
	}
	if x == y || strings.Contains(x, y) || strings.Contains(y, x) {
		return true
	}
	return overlap(strings.Fields(x), strings.Fields(y)) > 0.7
}

// overlap is the share of the longer word set that the two share.
func overlap(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	in := make(map[string]bool, len(a))
	for _, w := range a {
		in[w] = true
	}
	n := 0
	seen := map[string]bool{}
	for _, w := range b {
		if in[w] && !seen[w] {
			seen[w] = true
			n++
		}
	}
	most := max(len(b), len(a))
	return float64(n) / float64(most)
}

// pronouns are the words that stand in for a name. Longest first, so "its"
// is not read as "it".
var pronouns = regexp.MustCompile(`(?i)\b(their|them|they|she|his|him|her|its|it|he)\b`)

// admits are the entity labels each pronoun can stand for.
var admits = map[string][]string{
	"he":  {"PERSON"},
	"him": {"PERSON"},
	"his": {"PERSON"},
	"she": {"PERSON"},
	"her": {"PERSON"},
	"it": {"ORG", "GPE", "LOC", "PRODUCT", "EVENT", "FAC", "WORK_OF_ART", "LAW",
		"LANGUAGE", "DATE", "TIME", "PERCENT", "MONEY", "QUANTITY", "ORDINAL", "CARDINAL"},
	"its": {"ORG", "GPE", "LOC", "PRODUCT", "EVENT", "FAC", "WORK_OF_ART", "LAW",
		"LANGUAGE", "DATE", "TIME", "PERCENT", "MONEY", "QUANTITY", "ORDINAL", "CARDINAL"},
	"they":  {"ORG", "GPE", "PERSON", "NORP"},
	"them":  {"ORG", "GPE", "PERSON", "NORP"},
	"their": {"ORG", "GPE", "PERSON", "NORP"},
}
