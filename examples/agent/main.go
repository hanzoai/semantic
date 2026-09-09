// Command agent is a credit desk that has to answer for itself. It keeps what
// it was told, puts each choice to the rules it operates under, and records
// the choice either way — a refusal is a decision, and the reason it was
// refused is the only thing anybody will want to read later.
//
// A decision is a node in the same graph as everything else, and its evidence
// is edges. So "why did the agent do this" is a walk over edges rather than a
// stored explanation that can drift from the graph it explains — and evidence
// that was later retracted shows as retracted rather than quietly staying in
// the story.
//
//	go run ./examples/agent
package main

import (
	"context"
	"fmt"
	"log"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/hanzoai/semantic/agent"
	"github.com/hanzoai/semantic/decision"
)

func main() {
	ctx := context.Background()
	k := agent.New()
	k.Scope = agent.Scope{Agent: "credit-desk", Session: "s-4471"}

	policy(k)
	learn(ctx, k)
	ids := decide(ctx, k)
	causality(ctx, k, ids)
	why(ctx, k, ids)
	precedent(k)
	revision(k)
}

// policy is the book the desk works to. Rules are data rather than code so a
// policy can be written, versioned and audited by the people who own it, who
// are usually not the people who write Go.
//
// A rule's name says what it tests: min_ and max_ bound a value, has_ requires
// one, one_ restricts to a set, and any other name requires only that the
// field be there at all — which is how a policy says "this must have been
// considered" without saying what the answer had to be.
func policy(k *agent.Ken) {
	p, err := k.Rules.Add(agent.Policy{
		Name:  "Credit underwriting",
		Topic: "credit",
		Text:  "What the desk has to have established before it advances money.",
		Rules: map[string]any{
			"min_confidence": 0.7,
			"has_collateral": true,
			"one_region":     []any{"UK", "CA"},
			"reason":         nil, // present at all: the choice must be argued
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("policy  %s v%s over topic %q\n", p.Name, p.Version, p.Topic)
	for _, name := range slices.Sorted(maps.Keys(p.Rules)) {
		fmt.Printf("  %-16s %v\n", name, p.Rules[name])
	}
}

// learn keeps a note and puts what it is about into the graph: the note
// becomes a node, and every entity it names becomes one too, joined to it.
// Two entities named in the same note are then one hop apart through it — a
// claim the note actually supports, unlike an edge drawn straight between them.
func learn(ctx context.Context, k *agent.Ken) {
	notes := []agent.Note{
		{Text: "Babbage Engines filed audited accounts for 1849; the mill is unencumbered.",
			Of: []string{"Babbage Engines", "mill"}},
		{Text: "Menabrea Works has no filed accounts and no collateral on record.",
			Of: []string{"Menabrea Works"}},
		{Text: "Babbage Engines is registered in the UK; Menabrea Works in Canada.",
			Of: []string{"Babbage Engines", "Menabrea Works"}},
	}
	for _, n := range notes {
		if _, err := k.Learn(ctx, n); err != nil {
			log.Fatal(err)
		}
	}

	const q = "audited accounts mill collateral"
	fmt.Printf("\nrecall  %q\n", q)
	for _, f := range k.Recall(ctx, q, 3) {
		fmt.Printf("  %.2f  fresh=%-5v  %s\n", f.Score, f.Fresh, clip(f.Note.Text, 62))
	}
	s := k.Graph.Stats()
	fmt.Printf("  the graph now holds %d nodes and %d edges: %v\n", s.Nodes, s.Edges, s.Kinds)
}

// decide puts three choices to the rules and records what happened to each.
// A refusal is not an error: the agent asked, the rules answered, and the
// answer with its reason is the record.
func decide(ctx context.Context, k *agent.Ken) map[string]string {
	ids := map[string]string{}
	for _, d := range []agent.Decision{
		{
			Topic: "credit", Case: "advance £40,000 against the mill",
			Reason:     "audited accounts, unencumbered collateral, UK registration",
			Confidence: 0.86,
			Props:      map[string]any{"collateral": true, "region": "UK", "amount": 40000},
			Record:     record("credit-desk", "advance", "Babbage Engines", "mill"),
		},
		{
			Topic: "credit", Case: "advance £15,000 to Menabrea Works",
			Reason:     "no filed accounts, nothing on record to secure it",
			Confidence: 0.55,
			Props:      map[string]any{"collateral": false, "region": "CA", "amount": 15000},
			Record:     record("credit-desk", "advance", "Menabrea Works"),
		},
		{
			Topic: "credit", Case: "advance £5,000 to a Montréal supplier",
			Reason:     "small, secured, but outside the regions the desk covers",
			Confidence: 0.9,
			Props:      map[string]any{"collateral": true, "region": "FR", "amount": 5000},
			Record:     record("credit-desk", "advance", "Menabrea Works"),
		},
	} {
		got, err := k.Decide(ctx, d)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("\ndecision  %s\n", got.Case)
		fmt.Printf("  allowed %-5v  under %-18q  %s\n", got.Allowed, got.Rule, got.Gloss)
		fmt.Printf("  evidence %v\n", got.Because)
		ids[got.Case] = got.ID
	}
	return ids
}

// causality is the second graph inside the first: which decision brought which
// other one about. Only three labels are causal, so a walk over them is a walk
// over causality and over nothing else — which keeps "why did the agent do
// this" from wandering into every entity the decision merely mentioned.
func causality(ctx context.Context, k *agent.Ken, ids map[string]string) {
	advance := ids["advance £40,000 against the mill"]
	refusal := ids["advance £15,000 to Menabrea Works"]

	// A later decision, caused by the first.
	review, err := k.Journal.Write(ctx, agent.Decision{
		Topic: "credit", Case: "put Babbage Engines on quarterly review",
		Reason: "an advance of this size is reviewed every quarter", Confidence: 0.8,
		Allowed: true,
		Record:  record("credit-desk", "schedule review", "Babbage Engines"),
	})
	if err != nil {
		log.Fatal(err)
	}
	audit, err := k.Journal.Write(ctx, agent.Decision{
		Topic: "credit", Case: "commission an audit of the mill valuation",
		Reason: "the review found the valuation is three years old", Confidence: 0.75,
		Allowed: true,
		Record:  record("credit-desk", "commission audit", "mill"),
	})
	if err != nil {
		log.Fatal(err)
	}

	must(k.Journal.Lead(advance, review.ID, agent.Caused))
	must(k.Journal.Lead(review.ID, audit.ID, agent.Caused))
	must(k.Journal.Lead(refusal, audit.ID, agent.Influenced))

	fmt.Println("\ncausality")
	for _, a := range k.Cause.Up(audit.ID, 5) {
		fmt.Printf("  %d hop%s back: %s\n", a.Hops, plural(a.Hops), a.Decision.Case)
	}
	for _, r := range k.Cause.Roots(audit.ID, 5) {
		fmt.Printf("  the explanation stops at: %s\n", r.Decision.Case)
	}

	// A hop count alone is not evidence: three strong links and three weak
	// ones are both "three hops", and only the decay tells them apart.
	tr := k.Cause.Trace(advance, audit.ID)
	fmt.Printf("  trace %d hops, band %s, decay %.2f — %s\n", tr.Hops, tr.Band, tr.Decay, tr.Gloss)
	fmt.Printf("  force of the first advance: %.2f (what it moved)\n", k.Cause.Force(advance))

	net := k.Cause.Net(nil)
	fmt.Printf("  network: %d decisions, %d causal edges, density %.2f, %d groups that never touch\n",
		len(net.Decisions), net.Edges, net.Density, len(net.Parts))
	fmt.Printf("  causal cycles (a modelling error more often than a fact): %v\n", k.Cause.Loops(6))
}

// why reads the graph rather than a stored explanation, so what it says is
// what the graph currently supports. Retracting a piece of evidence changes
// the answer, which is the whole reason for keeping the explanation in the
// graph.
func why(ctx context.Context, k *agent.Ken, ids map[string]string) {
	id := ids["advance £40,000 against the mill"]
	story, err := k.Why(ctx, id)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("\nwhy did the desk advance £40,000?")
	fmt.Printf("  choice   %s\n", story.Decision.Choice)
	fmt.Printf("  reason   %s\n", story.Decision.Reason)
	fmt.Printf("  gloss    %s\n", story.Gloss)
	for _, n := range story.Because {
		fmt.Printf("  because  %-18s %s\n", n.ID, held(n.Span))
	}
	for _, a := range story.Led {
		fmt.Printf("  led to   %s (%d hops)\n", a.Decision.Case, a.Hops)
	}

	// The mill turns out to be encumbered after all. Retract it: the record
	// stays, so a question asked of an earlier moment still finds it, and the
	// decision that rested on it stays explainable.
	k.Graph.Retract("mill", "a prior charge was found on the title", time.Now())
	story, err = k.Why(ctx, id)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("\n  after retracting the collateral:")
	for _, n := range story.Because {
		fmt.Printf("  because  %-18s %s\n", n.ID, held(n.Span))
	}
	fmt.Println("  the decision is unchanged and still explainable; what changed is what it rests on")
}

// precedent is the search a caller makes before deciding: what did we do the
// last time this came up. A past decision serves better when it was the same
// kind of question and reached the same choice.
func precedent(k *agent.Ken) {
	fmt.Println("\nprecedent for \"advance against unencumbered property\"")
	for _, m := range k.Journal.Like(agent.Ask{
		Case: "advance against unencumbered property", Topic: "credit", N: 3,
	}) {
		fmt.Printf("  %.2f (words %.2f, shape %.2f)  %s → %s\n",
			m.Score, m.Words, m.Shape, clip(m.Decision.Case, 44), m.Decision.Choice)
	}
}

// revision is what changing a rule costs. A rule change is cheap to write and
// expensive to mean, so the cost is worth seeing before the change is made —
// measured against the decisions already taken under the rule it replaces.
func revision(k *agent.Ken) {
	p := k.Rules.For("credit")[0]
	proposed := map[string]any{
		"min_confidence": 0.9, // tightened
		"has_collateral": true,
		"one_region":     []any{"UK", "CA"},
		"reason":         nil,
	}
	sway, err := k.Rules.Sway(p.ID, proposed, k.Journal.On("credit"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\nraising min_confidence from 0.70 to 0.90\n")
	fmt.Printf("  judged %d decisions filed under credit; %d pass today and would not\n", sway.Judged, sway.Affected)
	fmt.Printf("  under the proposal %.0f%% of them would pass, risk %.2f\n", (1+sway.Fall)*100, sway.Risk)
	fmt.Println("  the review and audit decisions carry no collateral property, so they fail either way")
	for name, swap := range sway.Changed {
		fmt.Printf("  %s: %v → %v\n", name, swap.Was, swap.Now)
	}

	next, err := k.Rules.Revise(p.ID, proposed)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  revised to v%s; v%s stays readable, so a decision taken under it stays explainable\n",
		next.Version, p.Version)
	fmt.Printf("  history: %d versions\n", len(k.Rules.History(p.ID)))

	// An exception that is written down stays an exception. One that is not
	// becomes a quiet edit to the rule, and then nobody knows what the rule
	// was.
	var refused agent.Decision
	for _, d := range k.Journal.On("credit") {
		if !d.Allowed {
			refused = d
			break
		}
	}
	must(k.Rules.Waive(agent.Waiver{
		Decision: refused.ID, Policy: p.ID,
		Why: "board approved a relationship advance on the Canadian file", By: "risk committee",
	}))
	v := k.Rules.Check(refused, next)
	fmt.Printf("  waived: %s — %s\n", refused.Case, v.Why)
}

// record is the root form of a decision: who chose what, on which evidence.
// The evidence is written into the graph as edges, so it is reachable by the
// same walk as anything else.
func record(who, choice string, because ...string) decision.Record {
	return decision.Record{Agent: who, Choice: choice, Because: because, At: time.Now()}
}

func held(s agent.Span) string {
	if s.Until.IsZero() {
		return "held true"
	}
	return "retracted at " + s.Until.Format("15:04:05")
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) > n {
		return string([]rune(s)[:n-1]) + "…"
	}
	return s
}
