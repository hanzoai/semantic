// Command coref resolves the words in a text that point at the same thing.
// It runs between the entity pass and the relation pass rather than after
// both, because "she founded it" is a relation between two names only once the
// pronouns are bound to them.
//
// Nothing here needs a model. A pronoun is resolved to the nearest preceding
// name whose label it admits — "she" takes a person, "it" takes an
// organisation or a place — and to the nearest preceding name of any label
// when none does, since a wrong antecedent of the right shape tells a reader
// more than no antecedent.
//
//	go run ./examples/coref
package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hanzoai/semantic/extract"
)

const text = "Babbage Engines was founded by Charles Babbage. " +
	"Ada Lovelace is an analyst. " +
	"She works for it. " +
	"It is headquartered in Montréal."

// aside is the same rule showing its shape. A pronoun takes the nearest
// preceding name whose label it admits, and "it" admits a date, so a date
// standing between the pronoun and the thing it means wins.
const aside = "Babbage Engines opened in 1852. It employs forty people."

func main() {
	rules := extract.Rules{Names: map[string]string{
		"Ada Lovelace":    "PERSON",
		"Charles Babbage": "PERSON",
		"Babbage Engines": "ORG",
		"Montréal":        "LOC",
	}}
	known := rules.Entities(text)

	mentions(known)
	pronouns(known)
	chains(known)
	why(rules, known)
}

// mentions is every reference in the text: the names the entity pass found and
// the pronouns standing in for them, in the order they appear.
func mentions(known []extract.Entity) {
	fmt.Println("mentions")
	for _, m := range extract.Mentions(text, known) {
		kind := "name"
		if m.Pronoun {
			kind = "pronoun"
		}
		fmt.Printf("  %4d  %-7s %-16q %s\n", m.Start, kind, m.Text, m.Label)
	}
}

// pronouns binds each one. Refer records what it settled on and returns the
// pairs, so a caller can see the decision as well as act on it.
func pronouns(known []extract.Entity) {
	ms := extract.Mentions(text, known)
	fmt.Println("\nresolved")
	for _, r := range extract.Refer(ms) {
		fmt.Printf("  %-6q → %q\n", r.Pronoun, r.Of)
	}
}

// chains group the mentions that name one thing. Head is the mention that
// names it best — a name rather than a pronoun, and the earliest of those —
// which is the form to use when the graph needs one name for the node.
func chains(known []extract.Entity) {
	fmt.Println("\nchains")
	for _, c := range extract.Coref(text, known) {
		var said []string
		for _, m := range c.Mentions {
			said = append(said, m.Text)
		}
		fmt.Printf("  %-16q %-8s %d mentions: %s\n", c.Head.Text, c.Label, len(c.Mentions), strings.Join(said, ", "))
	}
	fmt.Println("  a thing mentioned once is not a chain, so it is not here")
}

// why is what the pass is for. The relation extractor joins entities it was
// given; a sentence that names one endpoint with a pronoun offers it only one.
// Rewriting each pronoun as the head of its chain turns those sentences into
// relations, and the count is the difference.
func why(rules extract.Rules, known []extract.Entity) {
	before := rules.Find(text)
	fmt.Printf("\nas written    %d relations\n", len(before.Relations))
	for _, r := range before.Relations {
		fmt.Printf("  %-16s %-12s %s\n", r.Subject.Text, r.Predicate, r.Object.Text)
	}

	resolved := substitute(text, extract.Coref(text, known))
	after := rules.Find(resolved)
	fmt.Printf("\nwith pronouns replaced by the head of their chain    %d relations\n", len(after.Relations))
	fmt.Printf("  %q\n", resolved)
	for _, r := range after.Relations {
		fmt.Printf("  %-16s %-12s %s\n", r.Subject.Text, r.Predicate, r.Object.Text)
	}

	// And where the rule shows its shape.
	fmt.Printf("\nnearest wins  %q\n", aside)
	for _, r := range extract.Refer(extract.Mentions(aside, rules.Entities(aside))) {
		fmt.Printf("  %-4q → %q — the date is nearer than the company, and \"it\" admits a date\n", r.Pronoun, r.Of)
	}
}

// substitute rewrites each resolved pronoun as the name it stands for,
// working back to front so the offsets it has not reached yet stay valid.
func substitute(text string, chains []extract.Chain) string {
	type edit struct {
		start, end int
		name       string
	}
	var edits []edit
	for _, c := range chains {
		for _, m := range c.Mentions {
			if m.Pronoun {
				edits = append(edits, edit{m.Start, m.End, c.Head.Text})
			}
		}
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	for _, e := range edits {
		text = text[:e.start] + e.name + text[e.end:]
	}
	return text
}
