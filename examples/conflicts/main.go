// Command conflicts finds where documents disagree and settles the
// disagreements it can. Three sources describe one company and contradict each
// other about when it was founded, how many people it employs, where it is,
// and what it even is.
//
// A disagreement is a value, never an error. It carries every claim, the
// document behind each, how sure the disagreement is real and how much it
// matters — and a rule that cannot settle it returns it unsettled rather than
// dropping it, because refusing to return an assertion is how a graph loses
// what a document said.
//
//	go run ./examples/conflicts
package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/hanzoai/semantic"
	"github.com/hanzoai/semantic/dedupe"
)

// sources are three readings of one company. The filing is old and trusted,
// the press release is recent and less so, the scrape is recent and barely
// trusted at all — and the scrape also calls the company a person.
var sources = []dedupe.Entity{
	{
		ID: "babbage", Name: "Babbage Engines", Kind: "Company",
		Props: map[string]any{"founded": 1843, "staff": 40, "city": "London", "share": 0.12},
		From:  dedupe.Source{Doc: "filing.pdf", At: day("1844-01-01"), Score: 0.95},
	},
	{
		ID: "babbage", Name: "Babbage Engines", Kind: "Company",
		Props: map[string]any{"founded": 1843, "staff": 55, "city": "London", "share": 0.14},
		From:  dedupe.Source{Doc: "press.html", At: day("1851-01-01"), Score: 0.6},
	},
	{
		ID: "babbage", Name: "Babbage Engines", Kind: "Person",
		Props: map[string]any{"founded": 1852, "staff": 55, "city": "Montréal", "share": 1.4},
		From:  dedupe.Source{Doc: "scrape.json", At: day("1999-01-01"), Score: 0.2},
	},
}

func main() {
	kinds()
	all()
	settle()
	edges()
}

// kinds runs each check on its own, because each answers a different question
// and a caller usually wants one of them.
func kinds() {
	fmt.Println("checks")

	show("Values(staff)", dedupe.Values(sources, "staff"))
	show("Values(city)", dedupe.Values(sources, "city"))
	show("Types", dedupe.Types(sources))
	show("Excludes", dedupe.Excludes(sources, nil))
	show("Times(founded)", dedupe.Times(sources, "founded"))
	show("Limits(share 0..1)", dedupe.Limits(sources, map[string]dedupe.Limit{"share": {Lo: 0, Hi: 1}}))

	fmt.Println("\n  Types asks whether the documents call it different things.")
	fmt.Println("  Excludes asks whether two of those can both be true — a company is not a person.")
	fmt.Println("  Limits is the one disagreement a single document can hold on its own:")
	fmt.Println("  a share of 1.4 is at odds with what a share means, not with another document.")
}

// all runs every check at once and tallies what came back, which is the view a
// reviewer wants: what kind of disagreement, how much it matters, which
// documents keep turning up, and which properties keep disagreeing.
func all() {
	cs := dedupe.Conflicts(sources, dedupe.Watch{
		Times:  []string{"founded"},
		Limits: map[string]dedupe.Limit{"share": {Lo: 0, Hi: 1}},
	})
	t := dedupe.Count(cs)
	fmt.Printf("\ntally  %d conflicts\n", t.Total)
	fmt.Printf("  by kind      %v\n", t.Kind)
	fmt.Printf("  by severity  %v\n", t.Level)
	fmt.Printf("  by document  %v\n", t.Doc)
	fmt.Printf("  by property  %v\n", t.Prop)

	// Every value stated for a property, with the document that stated it.
	// This is what a disagreement is settled from.
	fmt.Println("\nclaims about staff")
	for subject, claims := range dedupe.Claims(sources, "staff") {
		for _, c := range claims {
			fmt.Printf("  %s = %-4v  %s  (%s, trusted %.2f)\n",
				subject, c.Value, c.From.At.Format("2006"), c.From.Doc, c.From.Score)
		}
	}
}

// settle applies one rule to every conflict. Six rules, six different answers
// to the same question, and one of them — Flag — is the honest answer that
// nothing here can decide it.
func settle() {
	cs := dedupe.Values(sources, "staff")
	cs = append(cs, dedupe.Values(sources, "city")...)
	trust := dedupe.Trust{"filing.pdf": 0.95, "press.html": 0.6, "scrape.json": 0.2}

	fmt.Println("\nsettling  the same conflicts under six rules")
	fmt.Printf("  %-9s %-42s %-42s %s\n", "rule", "staff", "city", "why")
	for _, r := range []dedupe.Rule{dedupe.Vote, dedupe.Credible, dedupe.Newest, dedupe.Oldest, dedupe.Sure, dedupe.Flag} {
		picks := dedupe.Settle(cs, r, trust)
		fmt.Printf("  %-9s %-42s %-42s %s\n", r, pick(picks[0]), pick(picks[1]), picks[0].Why)
	}
	fmt.Println("  Flag settles nothing and says so: the disagreement stays in the graph,")
	fmt.Println("  visible, until a person decides.")
}

// edges is the same question asked of relations rather than properties: a
// subject given more than one object for a relation that admits only one.
func edges() {
	ts := []semantic.Triple{
		{Subject: "Babbage Engines", Predicate: "headquartered_in", Object: "London", Score: 0.9},
		{Subject: "Babbage Engines", Predicate: "headquartered_in", Object: "Montréal", Score: 0.5},
		{Subject: "Babbage Engines", Predicate: "employs", Object: "Ada Lovelace", Score: 0.9},
		{Subject: "Babbage Engines", Predicate: "employs", Object: "Charles Babbage", Score: 0.9},
	}
	// A predicate named in many may hold several objects and is not checked.
	// With no such table every predicate is read as admitting one object,
	// which is the strict reading and the one to start from.
	many := map[string]bool{"employs": true}
	fmt.Println("\nedges")
	show("strict", dedupe.Edges(ts, dedupe.Canon{}, nil))
	show("employs may repeat", dedupe.Edges(ts, dedupe.Canon{}, many))
}

func show(name string, cs []dedupe.Conflict) {
	if len(cs) == 0 {
		fmt.Printf("  %-20s none\n", name)
		return
	}
	for _, c := range cs {
		var said []string
		for _, v := range c.Values {
			said = append(said, fmt.Sprintf("%v (%s)", v.Value, v.From.Doc))
		}
		fmt.Printf("  %-20s %-8s %-8s %-6s %.2f  %s\n",
			name, c.Kind, c.Level, c.Prop, c.Score, strings.Join(said, " vs "))
	}
}

func pick(p dedupe.Pick) string {
	if !p.Done {
		return "unsettled"
	}
	return fmt.Sprintf("%v (%.2f, from %v)", p.Value, p.Score, p.From)
}

func day(s string) time.Time {
	t, _ := time.Parse("2006-01-02", s)
	return t
}
