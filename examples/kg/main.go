// Command kg folds assertions into a knowledge graph and asks what shape it
// is. The measures answer different questions and disagree on purpose: the
// node with the most edges is rarely the node the graph would come apart
// without.
//
// Repetition is the other half. The same assertion arriving twice is one edge,
// not two, so a second source raises confidence and adds provenance rather
// than duplicating structure.
//
//	go run ./examples/kg
package main

import (
	"fmt"
	"strings"

	"github.com/hanzoai/semantic"
	"github.com/hanzoai/semantic/kg"
)

// facts are two teams and the people in them, joined by one person who works
// with both. That person is the interesting node, and no degree count finds
// her.
var facts = []semantic.Triple{
	// The engine team.
	tri("Ada Lovelace", "works_for", "Babbage Engines", 0.9, "doc1"),
	tri("Charles Babbage", "works_for", "Babbage Engines", 0.9, "doc1"),
	tri("Joseph Clement", "works_for", "Babbage Engines", 0.8, "doc1"),
	tri("Ada Lovelace", "works_with", "Charles Babbage", 0.8, "doc1"),
	tri("Charles Babbage", "works_with", "Joseph Clement", 0.7, "doc2"),
	tri("Babbage Engines", "located_in", "London", 0.9, "doc1"),
	tri("Babbage Engines", "type", "Organization", 1, "doc1"),

	// The Montréal office, its own cluster.
	tri("Luigi Menabrea", "works_for", "Menabrea Works", 0.9, "doc3"),
	tri("Sophie Germain", "works_for", "Menabrea Works", 0.8, "doc3"),
	tri("Luigi Menabrea", "works_with", "Sophie Germain", 0.8, "doc3"),
	tri("Menabrea Works", "located_in", "Montréal", 0.9, "doc3"),
	tri("Menabrea Works", "type", "Organization", 1, "doc3"),

	// The one link between them.
	tri("Ada Lovelace", "works_with", "Luigi Menabrea", 0.7, "doc4"),
	tri("Ada Lovelace", "type", "Person", 1, "doc1"),

	tri("Difference Engine", "built_by", "Joseph Clement", 0.6, "doc5"),
	tri("Analytical Engine", "designed_by", "Charles Babbage", 0.6, "doc5"),

	// A part of the graph nothing else touches, from a document about
	// something else entirely.
	tri("Jacquard Loom", "designed_by", "Joseph Marie Jacquard", 0.7, "doc8"),

	// A claim asserted twice, by two documents.
	tri("Ada Lovelace", "works_for", "Babbage Engines", 0.95, "doc6"),
}

func main() {
	g := kg.Build(facts)

	shape(g)
	central(g)
	structure(g)
	paths(g)
	repetition(g)
	resolve()
}

// shape is the graph from a distance: how big, how dense, how connected, how
// far apart two nodes are on average.
func shape(g *kg.Graph) {
	s := g.Stat()
	fmt.Println("shape")
	fmt.Printf("  %d nodes, %d edges, %d distinct links\n", s.Nodes, s.Edges, s.Links)
	fmt.Printf("  density %.3f, %s\n", s.Density, s.Shape)
	fmt.Printf("  degree min %d, max %d, mean %.1f\n", s.Deg.Min, s.Deg.Max, s.Deg.Avg)
	fmt.Printf("  components %d, biggest holds %d, everything reaches everything: %v\n", s.Parts, s.Biggest, s.Whole)
	fmt.Printf("  mean shortest path %.2f\n", s.Path)

	// A type predicate states what a node is instead of making an edge.
	if n, ok := g.Node("ada lovelace"); ok {
		fmt.Printf("  node %q is a %s, asserted by %v, best confidence %.2f\n", n.Name, n.Type, n.Docs, n.Score)
	}
}

// central is four measures of importance that answer four questions. Degree
// asks who has the most edges; Rank asks where a random walker spends its
// time; Between asks whose removal would cut the graph; Close asks who is
// nearest to everything.
func central(g *kg.Graph) {
	fmt.Println("\ncentrality")
	fmt.Printf("  %-20s %-20s %-20s %s\n", "degree", "pagerank", "betweenness", "closeness")
	deg, rank, btw, cls := g.Degree(), g.Rank(), g.Between(), g.Close()
	top := func(m map[string]float64) []kg.Pair { return kg.Top(m, 4) }
	a, b, c, d := top(deg), top(rank), top(btw), top(cls)
	for i := range a {
		fmt.Printf("  %-20s %-20s %-20s %s\n", pair(a[i]), pair(b[i]), pair(c[i]), pair(d[i]))
	}
	fmt.Println("  the top of betweenness is the node joining the two teams, which no degree count finds")

	// Eigenvector centrality: being joined to important nodes counts for more
	// than being joined to many.
	fmt.Printf("  eigenvector: %s\n", row(kg.Top(g.Eigen(), 3)))
}

// structure is the graph's parts: the communities it falls into, the
// components that never touch, and the edges it can least afford to be wrong
// about.
func structure(g *kg.Graph) {
	groups := g.Groups()
	fmt.Printf("\ncommunities  %d, modularity %.2f\n", len(groups), g.Modularity(groups))
	for _, grp := range groups {
		fmt.Printf("  %s\n", strings.Join(grp, ", "))
	}

	fmt.Printf("\ncomponents   %d\n", len(g.Parts()))
	for _, p := range g.Parts() {
		fmt.Printf("  %d nodes: %s\n", len(p), strings.Join(p, ", "))
	}

	fmt.Println("\nbridges      removing one raises the component count by exactly one")
	for _, e := range g.Bridges() {
		fmt.Printf("  %-18s %-12s %-18s asserted %dx, id %s\n", e.From, e.Label, e.To, e.Count, e.ID()[:8])
	}
}

// paths are the routes through the graph and the neighbourhoods around a node.
func paths(g *kg.Graph) {
	fmt.Println("\npaths")
	route := g.Path("sophie germain", "london")
	fmt.Printf("  sophie germain → london: %s (%d steps)\n", strings.Join(route, " → "), len(route)-1)
	fmt.Printf("  sophie germain → jacquard loom: %v (they are in different components)\n",
		g.Path("sophie germain", "jacquard loom"))

	near := g.Near("ada lovelace", 1)
	n, e := near.Size()
	fmt.Printf("  one hop from ada lovelace: %d nodes, %d edges\n", n, e)
	near = g.Near("ada lovelace", 2)
	n, e = near.Size()
	fmt.Printf("  two hops:                  %d nodes, %d edges\n", n, e)

	sub := g.Sub("ada lovelace", "charles babbage", "babbage engines")
	n, e = sub.Size()
	fmt.Printf("  induced subgraph of three named nodes: %d nodes, %d edges\n", n, e)
}

// repetition is what a second source does to a claim. It does not make a
// second edge; it raises the confidence and records the further document.
func repetition(g *kg.Graph) {
	fmt.Println("\nrepetition")
	for _, e := range g.Edges() {
		if e.Count > 1 {
			fmt.Printf("  %s %s %s: asserted %d times by %v, best confidence %.2f\n",
				e.From, e.Label, e.To, e.Count, e.Docs, e.Score)
		}
	}

	// The identifier of an assertion is a function of the assertion, so the
	// same claim names itself the same way in every process and every run —
	// which is what pointing at a claim from a provenance record needs.
	fmt.Printf("  Hash is stable across runs and spellings: %s == %s\n",
		kg.Hash("Ada Lovelace", "works_for", "Babbage Engines")[:12],
		kg.Hash("ada  lovelace", "works_for", "BABBAGE ENGINES")[:12])
}

// resolve is entity resolution from the outside. kg performs a merge; it does
// not decide that two names are one thing — that is dedupe's job, or a
// person's, and either way the decision arrives here as a call.
func resolve() {
	g := kg.Build([]semantic.Triple{
		tri("A. Lovelace", "works_for", "Babbage Engines", 0.8, "doc7"),
		tri("Ada Lovelace", "born_in", "London", 0.9, "doc1"),
		tri("Ada Lovelace", "works_for", "Babbage Engines", 0.9, "doc1"),
	})
	before, beforeEdges := g.Size()
	g.Merge("ada lovelace", "a. lovelace")
	after, afterEdges := g.Size()

	fmt.Printf("\nmerge  %d nodes, %d edges → %d nodes, %d edges\n", before, beforeEdges, after, afterEdges)
	n, _ := g.Node("ada lovelace")
	fmt.Printf("  %q now answers to %v and was asserted by %v\n", n.Name, n.Alias, n.Docs)
}

// tri writes one assertion with the document that made it.
func tri(s, p, o string, score float64, doc string) semantic.Triple {
	return semantic.Triple{
		Subject: s, Predicate: p, Object: o, Score: score,
		From: semantic.Chunk{DocID: doc},
	}
}

func pair(p kg.Pair) string { return fmt.Sprintf("%s %.2f", clip(p.ID, 13), p.Score) }

func row(ps []kg.Pair) string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = pair(p)
	}
	return strings.Join(out, ", ")
}

func clip(s string, n int) string {
	if len([]rune(s)) > n {
		return string([]rune(s)[:n-1]) + "…"
	}
	return s
}
