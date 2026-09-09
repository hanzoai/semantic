// Command dedupe decides which records are the same thing. Four documents
// describe three companies between them, spelled differently, and the job is
// to say which two are one.
//
// Everything here is a value the caller can read: a pair carries the evidence
// for the judgement, a group carries how tight it is, and a merge carries the
// records it was made from and every property they disagreed on. A graph that
// has forgotten those cannot say what it was built from.
//
//	go run ./examples/dedupe
package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/hanzoai/semantic"
	"github.com/hanzoai/semantic/dedupe"
)

// records are what four documents said. Two of them describe one company under
// two spellings; one describes a different company that shares a word with it;
// one describes a person who shares a name with nothing.
var records = []dedupe.Entity{
	{
		ID: "e1", Name: "Babbage Engines Inc.", Kind: "Company", Score: 0.9,
		Props: map[string]any{"founded": 1843, "city": "London", "staff": 40},
		From:  dedupe.Source{Doc: "filing.pdf", At: day("1843-06-01"), Score: 0.95},
	},
	{
		ID: "e2", Name: "Babbage Engines", Kind: "Company", Score: 0.8,
		Props: map[string]any{"founded": 1844, "city": "London", "staff": 55, "ceo": "Charles Babbage"},
		From:  dedupe.Source{Doc: "press.html", At: day("1850-01-01"), Score: 0.6},
	},
	{
		ID: "e3", Name: "Menabrea Works", Kind: "Company", Score: 0.85,
		Props: map[string]any{"founded": 1852, "city": "Montréal"},
		From:  dedupe.Source{Doc: "registry.csv", At: day("1852-01-01"), Score: 0.9},
	},
	{
		ID: "e4", Name: "Babbage Engines", Kind: "Person", Score: 0.5,
		Props: map[string]any{"city": "London"},
		From:  dedupe.Source{Doc: "scrape.json", At: day("1999-01-01"), Score: 0.2},
	},
}

func main() {
	ctx := context.Background()

	compare()
	screens()
	find(ctx)
	merge()
	incremental(ctx)
	assertions()
}

// compare is the one comparison everything else is built on: how alike two
// records are, and which evidence carried the weight. Only the parts that
// exist are weighed, so missing evidence neither helps nor hurts.
func compare() {
	fmt.Println("likeness")
	for _, p := range [][2]dedupe.Entity{{records[0], records[1]}, {records[0], records[2]}, {records[1], records[3]}} {
		s := dedupe.Likeness(p[0], p[1], dedupe.Weights{}, dedupe.Jaro)
		fmt.Printf("  %-22s vs %-22s %.2f  %v\n", p[0].Name, p[1].Name, s.Total, round(s.Parts))
	}

	fmt.Println("\nstring metrics on the same pair")
	for _, m := range []dedupe.Metric{dedupe.Exact, dedupe.Edit, dedupe.Jaro, dedupe.Gram, dedupe.Token} {
		fmt.Printf("  %-6s %.2f\n", m, dedupe.Text("Babbage Engines Inc.", "Babbage Engines", m))
	}
}

// screens are the cheap tests that come before the comparison. Neither is a
// verdict: they cut the candidate set down so the real comparison runs on
// pairs worth comparing.
func screens() {
	a := "Babbage Engines was founded by Charles Babbage in London in 1843."
	b := "Babbage Engines, founded in London in 1843 by Charles Babbage."
	c := "Menabrea Works opened an assembly floor in Montréal in 1852."

	fmt.Println("\nscreens")
	fmt.Printf("  simhash  same text reworded %.2f, unrelated text %.2f\n",
		dedupe.Near(dedupe.Hash(a), dedupe.Hash(b)), dedupe.Near(dedupe.Hash(a), dedupe.Hash(c)))
	fmt.Printf("  minhash  same text reworded %.2f, unrelated text %.2f  (128 hashes, 8 bytes each)\n",
		dedupe.Sign(a, 128).Like(dedupe.Sign(b, 128)), dedupe.Sign(a, 128).Like(dedupe.Sign(c, 128)))
	fmt.Println("  a simhash of unrelated text sits near 0.5, where half the bits differ by chance")
}

// find compares only records that share a blocking key, so the cost is far
// below every pair. Two records whose kinds are stated and differ are never a
// pair whatever they score: a person and a company that share a name are two
// things.
func find(ctx context.Context) {
	pairs, err := dedupe.Find(ctx, records, dedupe.Default)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\nfind  %d of the %d possible pairs, at Default (like 0.70, score 0.60)\n",
		len(pairs), len(records)*(len(records)-1)/2)
	for _, p := range pairs {
		fmt.Printf("  %s + %s  like %.2f  sure %.2f  because: %s\n",
			p.A.ID, p.B.ID, p.Like, p.Score, strings.Join(p.Why, "; "))
	}
	fmt.Printf("  e2 and e4 share a name exactly and are not a pair: their kinds differ\n")

	groups := dedupe.Groups(pairs)
	fmt.Printf("\ngroups  %d\n", len(groups))
	for _, g := range groups {
		fmt.Printf("  head %-22q %d records, sure %.2f, tight %.2f\n", g.Head.Name, len(g.Of), g.Score, g.Tight())
	}
	loose := dedupe.Loose(records, groups)
	fmt.Printf("  unclaimed: %s — not leftovers, but things only one document mentions\n", names(loose))
}

// merge folds the records of a group into one. Which record survives, and
// which value wins where two disagree, is the Keep strategy; every
// disagreement is recorded either way.
func merge() {
	fmt.Println("\nmerge")
	fmt.Printf("  %-8s %-24s %-28s %s\n", "keep", "survivor", "the two they disagreed on", "clashes")
	for _, k := range []dedupe.Keep{dedupe.First, dedupe.Last, dedupe.Fullest, dedupe.Surest, dedupe.All} {
		m, err := dedupe.Merge(records[:2], k)
		if err != nil {
			log.Fatal(err)
		}
		var clash []string
		for _, c := range m.Clash {
			clash = append(clash, fmt.Sprintf("%s: %v → %v", c.Prop, c.Values, c.Took))
		}
		fmt.Printf("  %-8s %-24q %-28s %s\n", k, m.Entity.Name, props(m.Entity), strings.Join(clash, ", "))
	}

	m, _ := dedupe.Merge(records[:2], dedupe.Fullest)
	fmt.Printf("  the merge keeps what it was made from: %v\n", m.Kept)
	fmt.Println("  note: last picks the later record as the survivor and then takes the")
	fmt.Println("  earlier record's value where the two disagree — the survivor choice and")
	fmt.Println("  the value choice point opposite ways")
}

// incremental is the call to make when a graph already exists and a document
// has just arrived: compare the new records against the held ones, and against
// nothing else. The cost is the product of the two sets, not the square of
// their sum.
func incremental(ctx context.Context) {
	fresh := []dedupe.Entity{{
		ID: "e5", Name: "Babbage Engines Company", Kind: "Company", Score: 0.7,
		Props: map[string]any{"founded": 1843, "city": "London"},
		From:  dedupe.Source{Doc: "wire.txt", At: day("1860-01-01"), Score: 0.5},
	}}
	pairs, err := dedupe.Add(ctx, fresh, records, dedupe.Default)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\nincremental  1 new record against %d held → %d pairs\n", len(records), len(pairs))
	for _, p := range pairs {
		fmt.Printf("  %s (%s) matches %s (%s) at %.2f\n", p.A.ID, p.A.Name, p.B.ID, p.B.Name, p.Like)
	}

	// Fuse is find and merge in one call, for a caller that wants the result
	// rather than the reasoning.
	merged, err := dedupe.Fuse(ctx, append(records, fresh...), dedupe.Default, dedupe.Fullest)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  Fuse over all %d records → %d merged\n", len(records)+1, len(merged))
	for _, m := range merged {
		fmt.Printf("    %-24q from %v, %d clashes\n", m.Entity.Name, m.Kept, len(m.Clash))
	}
}

// assertions is the same question asked of triples rather than records: which
// of these say the same thing. A synonym table folds two spellings of one
// relation together, and the kept triple takes the best confidence any
// repetition carried.
func assertions() {
	ts := []semantic.Triple{
		{Subject: "Babbage Engines", Predicate: "works at", Object: "London", Score: 0.6},
		{Subject: "Babbage Engines", Predicate: "WORKS_AT", Object: "london", Score: 0.9},
		{Subject: "Babbage Engines", Predicate: "employs", Object: "Ada Lovelace", Score: 0.8},
	}
	canon := dedupe.Canon{Fold: true, Synonyms: map[string]string{"works at": "located_in", "works_at": "located_in"}}

	fmt.Printf("\ntriples  %d asserted\n", len(ts))
	unique := dedupe.Unique(ts, canon)
	fmt.Printf("  %d distinct under a synonym table that folds works_at to located_in\n", len(unique))
	for _, t := range unique {
		fmt.Printf("    %-16s %-12s %-14s %.2f\n", t.Subject, t.Predicate, t.Object, t.Score)
	}
	fmt.Println("  the survivor keeps the first spelling and the best confidence of the claims it stands for")
}

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		log.Fatal(err)
	}
	return t
}

func names(es []dedupe.Entity) string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.ID + " " + e.Name
	}
	return strings.Join(out, ", ")
}

func props(e dedupe.Entity) string {
	var out []string
	for _, k := range []string{"founded", "staff"} {
		if v, ok := e.Props[k]; ok {
			out = append(out, fmt.Sprintf("%s=%v", k, v))
		}
	}
	return strings.Join(out, " ")
}

func round(m map[string]float64) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		out[k] = fmt.Sprintf("%.2f", v)
	}
	return out
}
