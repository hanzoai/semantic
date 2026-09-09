// Command reason derives the facts a set of triples implies but does not
// state. There is one way a fact gets derived — forward chaining to a fixpoint
// — so there is one place to look when asking why, and transitivity, symmetry
// and inverse are rules rather than special cases.
//
// Every derived fact names the rule that produced it and the facts it rests
// on. An assertion nobody can explain is not evidence, and the point of
// deriving it was to answer for it later.
//
//	go run ./examples/reason
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/hanzoai/semantic"
	"github.com/hanzoai/semantic/reason"
)

// given is what the documents said. Nobody wrote down that Ada Lovelace works
// for the parent company, or that Charles Babbage is her colleague, or that a
// difference engine is a machine.
var given = []semantic.Triple{
	// Ownership, which is transitive.
	t("Babbage Engines", "part_of", "Engines Group"),
	t("Engines Group", "part_of", "Difference Holdings"),

	// Employment, and its inverse.
	t("Ada Lovelace", "works_for", "Babbage Engines"),
	t("Charles Babbage", "works_for", "Babbage Engines"),

	// A symmetric relation stated once.
	t("Ada Lovelace", "colleague_of", "Charles Babbage"),

	// Types, for the hierarchy below to entail from.
	t("Ada Lovelace", "type", "Analyst"),
	t("Difference Engine", "type", "DifferenceEngine"),
	t("Babbage Engines", "type", "Company"),
}

// classes is the hierarchy the schema contributes. An Analyst is an Employee
// is a Person; a DifferenceEngine is a Machine is an Artifact.
var classes = reason.Tree{
	"Analyst":          "Employee",
	"Employee":         "Person",
	"DifferenceEngine": "Machine",
	"Machine":          "Artifact",
	"Company":          "Organization",
}

func main() {
	ctx := context.Background()
	m := run(ctx)
	explain(m)
	query(m)
	written()
	limits(ctx)
}

// run states the rules and closes the facts under them. Transitive, Symmetric
// and Inverse are constructors for ordinary rules; the schema contributes the
// two entailments a class hierarchy licenses.
func run(ctx context.Context) *reason.Model {
	employs := reason.Inverse("works_for", "employs")

	// A rule of the caller's own, written the way Datalog writes one.
	sibling, err := reason.Parse("colleague_of(?a, ?b) :- works_for(?a, ?c), works_for(?b, ?c).")
	if err != nil {
		log.Fatal(err)
	}

	// And one that composes two relations: whoever works for a subsidiary
	// works for its parent.
	group, err := reason.Parse("works_for(?p, ?g) :- works_for(?p, ?c), part_of(?c, ?g).")
	if err != nil {
		log.Fatal(err)
	}

	e := reason.Engine{
		Rules: []reason.Rule{
			reason.Transitive("part_of"),
			reason.Symmetric("colleague_of"),
			employs,
			sibling,
			group,
		},
		Class: classes,
	}

	fmt.Println("rules")
	for _, r := range e.Rules {
		fmt.Printf("  %s\n", r)
	}

	m, err := e.Run(ctx, given)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\n%d facts given, %d derived, %d in the closure\n",
		len(m.Given()), len(m.Derived()), len(m.All()))

	fmt.Println("\nderived")
	for _, f := range m.Derived() {
		fmt.Printf("  %-46s by %-22s depth %d\n", f, rule(f.Rule), f.Depth)
	}
	fmt.Println("  the colleague rule as written makes everyone their own colleague: it says")
	fmt.Println("  nothing about ?a and ?b being different, and Datalog has no inequality to")
	fmt.Println("  say it with — that is a fact about the rule, not about the engine")
	return m
}

// explain walks a derivation back to the facts it rests on, leaves first. A
// given fact rests on nothing and explains itself in one line.
func explain(m *reason.Model) {
	key := reason.Key(t("Ada Lovelace", "works_for", "Difference Holdings"))
	why, err := m.Why(key)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("\nwhy is Ada Lovelace employed by Difference Holdings?")
	for _, f := range why {
		if f.Rule == "" {
			fmt.Printf("  %-46s given\n", f)
			continue
		}
		fmt.Printf("  %-46s by %s\n", f, rule(f.Rule))
	}

	fmt.Println("\nand the class entailments, which nobody stated")
	for _, f := range m.Derived() {
		if f.Predicate == reason.Type {
			fmt.Printf("  %-46s by %s\n", f, rule(f.Rule))
		}
	}
}

// query asks a question of the closure by leaving a variable where the answer
// goes. Every fact that matches comes back as one binding.
func query(m *reason.Model) {
	fmt.Println("\nquestions")
	for _, p := range []reason.Pattern{
		{Subject: "?who", Predicate: "type", Object: "Person"},
		{Subject: "Babbage Engines", Predicate: "employs", Object: "?who"},
		{Subject: "?who", Predicate: "works_for", Object: "Difference Holdings"},
		{Subject: "Ada Lovelace", Predicate: "?how", Object: "Charles Babbage"},
	} {
		var got []string
		for _, b := range m.Match(p) {
			for _, v := range p.Vars() {
				got = append(got, b[v])
			}
		}
		fmt.Printf("  %-44s %v\n", p, got)
	}

	// Has answers the closed question directly.
	claim := t("Charles Babbage", "colleague_of", "Ada Lovelace")
	fmt.Printf("  does the closure hold %v? %v (nobody wrote it down)\n",
		fmt.Sprintf("%s %s %s", claim.Subject, claim.Predicate, claim.Object), m.Has(claim))
}

// written is the round trip: a rule renders in the form it parses from, so a
// rule set can be kept in a file next to the data it reasons over.
func written() {
	fmt.Println("\nrules read and write in one form")
	for _, s := range []string{
		"ancestor(?x, ?y) :- parent(?x, ?z), ancestor(?z, ?y)",
		"grandparent(?x, ?z) :- parent(?x, ?y), parent(?y, ?z).",
	} {
		r, err := reason.Parse(s)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("  %-52q → %s\n", s, r)
	}
}

// limits are the two ways a run stops short, and both are reported rather than
// hidden. A rule that cannot fire is refused before any work is done; a run
// that hits its depth returns what it derived alongside the error, because
// facts already derived are still sound and still explained.
func limits(ctx context.Context) {
	fmt.Println("\nlimits")

	bad := reason.Rule{Name: "unbound", Body: []reason.Pattern{{Subject: "?x", Predicate: "p", Object: "?y"}},
		Head: reason.Pattern{Subject: "?x", Predicate: "q", Object: "?z"}}
	if err := bad.Check(); errors.Is(err, reason.ErrRule) {
		fmt.Printf("  %v\n", err)
		fmt.Println("  an unbound head variable would derive a fact with a hole in it")
	}

	// A chain long enough that two rounds cannot close it.
	chain := []semantic.Triple{
		t("a", "part_of", "b"), t("b", "part_of", "c"),
		t("c", "part_of", "d"), t("d", "part_of", "e"),
	}
	e := reason.Engine{Rules: []reason.Rule{reason.Transitive("part_of")}, Depth: 1}
	m, err := e.Run(ctx, chain)
	if errors.Is(err, reason.ErrDepth) {
		fmt.Printf("  at Depth 1: %d derived, and %v\n", len(m.Derived()), err)
	}
	full, err := reason.Engine{Rules: []reason.Rule{reason.Transitive("part_of")}}.Run(ctx, chain)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  at the default depth: %d derived, the whole transitive closure\n", len(full.Derived()))
}

func t(s, p, o string) semantic.Triple {
	return semantic.Triple{Subject: s, Predicate: p, Object: o, Score: 1}
}

// rule shortens a rule that explains itself by writing itself out, so the
// listing stays a listing.
func rule(s string) string {
	if i := strings.Index(s, " :- "); i > 0 && len(s) > 30 {
		return s[:i] + " :- …"
	}
	return s
}
