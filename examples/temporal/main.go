// Command temporal is the graph asked how it stood, not how it stands. Every
// node and edge carries the span of time it is held true, so a fact can stop
// being current without being erased, and a question asked of an earlier
// moment gets the answer that was right then.
//
// Two ways of losing a fact, and they are not the same. Retract closes the
// span and keeps the record: the fact stopped being true, and a decision that
// rested on it stays explainable. Purge removes the record and leaves a
// tombstone: the data itself had to be gone, and what is left is proof that
// something was removed, without the thing itself.
//
//	go run ./examples/temporal
package main

import (
	"fmt"
	"time"

	"github.com/hanzoai/semantic/agent"
)

// The dates the story happens on, so two runs print the same thing.
var (
	y1843 = day("1843-01-01")
	y1849 = day("1849-01-01")
	y1852 = day("1852-01-01")
	y1855 = day("1855-01-01")
	now   = day("1860-01-01")
)

func main() {
	g := &agent.Graph{}
	audit := watch(g)

	build(g)
	travel(g)
	lose(g)
	walk(g)
	trail(audit)
}

// build writes the facts with the spans they held over. A zero bound is open,
// so a fact with neither is held always — which is what an ordinary fact wants.
func build(g *agent.Graph) {
	g.Add(agent.Node{ID: "babbage engines", Kind: "company", Text: "Babbage Engines",
		Span: agent.Span{From: y1843}})
	g.Add(agent.Node{ID: "ada lovelace", Kind: "person", Text: "Ada Lovelace analyst"})
	g.Add(agent.Node{ID: "london", Kind: "place", Text: "London"})
	g.Add(agent.Node{ID: "montréal", Kind: "place", Text: "Montréal"})

	// The company was in London from 1843 and moved to Montréal in 1852.
	// Both edges are true, at different times.
	g.Join(agent.Edge{Link: agent.Link{From: "babbage engines", To: "london", Label: "located_in"},
		Weight: 0.9, Span: agent.Span{From: y1843, Until: y1852}})
	g.Join(agent.Edge{Link: agent.Link{From: "babbage engines", To: "montréal", Label: "located_in"},
		Weight: 0.9, Span: agent.Span{From: y1852}})
	g.Join(agent.Edge{Link: agent.Link{From: "ada lovelace", To: "babbage engines", Label: "works_for"},
		Weight: 0.8, Span: agent.Span{From: y1843, Until: y1855}})

	s := g.Stats()
	fmt.Printf("graph  %d nodes, %d edges, density %.2f\n", s.Nodes, s.Edges, s.Density)
	fmt.Printf("  kinds %v  labels %v\n", s.Kinds, s.Labels)
}

// travel asks the graph how it stood at four moments. An edge to a node that
// was not yet true is not a relation, so it is left out rather than dangling.
func travel(g *agent.Graph) {
	fmt.Println("\nas it stood")
	for _, at := range []time.Time{day("1840-01-01"), y1849, day("1853-01-01"), now} {
		nodes, edges := g.At(at)
		fmt.Printf("  %s  nodes %d, edges %d", at.Format("2006"), len(nodes), len(edges))
		for _, e := range edges {
			if e.Label == "located_in" {
				fmt.Printf("   — located_in %s", e.To)
			}
		}
		fmt.Println()
	}
	fmt.Println("  1840 is before the company existed, so the edges to it are not relations yet")

	// The span itself answers the question directly.
	employed := agent.Span{From: y1843, Until: y1855}
	fmt.Printf("  was Ada employed in 1849? %v   in 1860? %v\n", employed.Holds(y1849), employed.Holds(now))
	fmt.Printf("  closing a span only ever shortens it: closing 1855 at 1860 leaves %s\n",
		employed.Close(now).Until.Format("2006"))
}

// lose is the difference between a fact that stopped being true and data that
// had to be gone.
func lose(g *agent.Graph) {
	fmt.Println("\nlosing a fact")

	// The analyst left. The record stays, so a question asked of 1849 still
	// finds her and a decision that rested on her stays explainable. Edges
	// close with the node, because a live relation around a node that is no
	// longer true reads as current when it is not.
	g.Retract("ada lovelace", "left the company", y1855)
	nodes, edges := g.At(y1849)
	fmt.Printf("  after retracting: 1849 still holds nodes %d, edges %d\n", len(nodes), len(edges))
	nodes, _ = g.At(now)
	fmt.Printf("                    1860 holds nodes %d\n", len(nodes))
	if n, ok := g.Node("ada lovelace"); ok {
		fmt.Printf("  the node is still readable, its span now ends %s\n", n.Span.Until.Format("2006"))
	}

	// A subject exercises a right to erasure. The data goes; the note that it
	// went stays, which is what erasure actually needs — proof that something
	// was removed, without the thing itself.
	g.Add(agent.Node{ID: "supplier contact", Kind: "person", Text: "a named individual"})
	g.Join(agent.Edge{Link: agent.Link{From: "supplier contact", To: "babbage engines", Label: "supplies"}})
	g.Purge("supplier contact", "erasure requested", now)
	_, ok := g.Node("supplier contact")
	fmt.Printf("  after purging: the node is readable at all? %v\n", ok)

	fmt.Println("\nlosses")
	for _, l := range g.Losses() {
		what := l.Node
		if what == "" {
			what = l.Edge.From + " –" + l.Edge.Label + "→ " + l.Edge.To
		}
		kind := "retracted"
		if l.Purged {
			kind = "purged"
		}
		via := ""
		if l.Via != "" {
			via = "  (reached through " + l.Via + ")"
		}
		fmt.Printf("  %-9s %s  %s  %q%s\n", kind, l.At.Format("2006"), what, l.Why, via)
	}
}

// walk is a traversal that knows what time it is. Confidence decays by the
// weight of each edge crossed, so a fact three weak edges away arrives marked
// as the weak evidence it is; and a walk given a moment sees the graph as it
// stood then.
func walk(g *agent.Graph) {
	fmt.Println("\nwalking")
	for _, at := range []time.Time{y1849, now} {
		steps := g.Near("babbage engines", agent.Reach{Hops: 2, At: at})
		fmt.Printf("  from babbage engines in %s:\n", at.Format("2006"))
		for _, s := range steps {
			fmt.Printf("    hop %d  %-16s via %-11s weight %.2f  decay %.2f  %s\n",
				s.Hop, s.Node.ID, s.Label, s.Weight, s.Decay, s.Band)
		}
	}

	route := g.Path("ada lovelace", "london", agent.Reach{At: y1849})
	fmt.Printf("  route from ada lovelace to london in 1849: %v, %d hops, decay %.2f\n",
		route.IDs(), route.Hops, route.Decay)

	// One task's context stays its own while still being reachable from
	// another: a door joins two graphs without merging them.
	other := &agent.Graph{}
	other.Add(agent.Node{ID: "thames", Kind: "place", Text: "the Thames"})
	other.Join(agent.Edge{Link: agent.Link{From: "thames", To: "wharf", Label: "runs_past"}})
	g.Door("london", other, "thames")
	crossed := g.Cross("babbage engines", other, "wharf", agent.Reach{Hops: 6, At: y1849})
	fmt.Printf("  across a door into another graph: found=%v, %d hops, crossing %d boundary\n",
		crossed.Found, crossed.Hops, crossed.Doors)
}

// watch is how an audit trail is kept outside the graph, so the graph does not
// have to know what an audit is. It is called once per change, after the graph
// is consistent and the lock is released.
func watch(g *agent.Graph) *[]agent.Change {
	var log []agent.Change
	g.Watch = func(c agent.Change) { log = append(log, c) }
	return &log
}

func trail(log *[]agent.Change) {
	counts := map[agent.Op]int{}
	for _, c := range *log {
		counts[c.Op]++
	}
	fmt.Printf("\naudit trail  %d changes: %d added, %d closed, %d purged\n",
		len(*log), counts[agent.OpAdd], counts[agent.OpClose], counts[agent.OpPurge])
	fmt.Println("  the last four:")
	for _, c := range (*log)[len(*log)-4:] {
		what := c.Node
		if what == "" {
			what = c.Edge.From + " –" + c.Edge.Label + "→ " + c.Edge.To
		}
		fmt.Printf("    %-6s %s\n", c.Op, what)
	}
}

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}
