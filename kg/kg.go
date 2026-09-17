// Package kg folds extracted triples into a knowledge graph and answers
// questions about its shape.
//
// The same assertion arriving twice is one edge, not two. Nodes are keyed by
// the folded form of their name and edges by (subject, predicate, object), so
// repetition raises confidence and adds provenance rather than duplicating
// structure. Where a graph is kept is store's business; Mem is the in-memory
// default so this package and its callers work without a database.
package kg

import (
	"fmt"
	"hash/fnv"
	"maps"
	"slices"
	"strings"

	"github.com/hanzoai/semantic"
)

// Node is one thing the graph knows about.
type Node struct {
	ID    string         // stable key, the folded form of the name
	Name  string         // surface form, as first asserted
	Type  string         // set by a type predicate; empty until one arrives
	Score float64        // best confidence any assertion gave it
	Props map[string]any // properties, as written by a store caller
	Docs  []string       // documents that asserted it, first seen first
	Alias []string       // ids folded into this one by Merge
}

// Edge is one relation asserted between two nodes.
type Edge struct {
	From  string
	To    string
	Label string   // predicate, surface form as first asserted
	Score float64  // best confidence any assertion gave it
	Count int      // how many assertions carried it
	Docs  []string // documents that asserted it, first seen first
}

// ID returns the stable identifier of the assertion this edge records, the
// name a provenance entry uses to point at the claim.
func (e Edge) ID() string { return Hash(e.From, e.Label, e.To) }

// Fold is the canonical form of a name and the id of the node that carries it:
// leading, trailing and repeated whitespace removed, lower case. Two spellings
// that fold alike are one node.
func Fold(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

// Hash is the stable identifier of an assertion: FNV-1a over the folded
// triple, hex. The same claim hashes the same in every process and every run,
// which is what naming a claim in a provenance record needs.
func Hash(s, p, o string) string {
	h := fnv.New64a()
	for _, part := range [3]string{Fold(s), Fold(p), Fold(o)} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return fmt.Sprintf("%016x", h.Sum64())
}

// Types are the predicates read as a statement about what a node is rather
// than as an edge, when Build is given no others.
var Types = []string{"type", "is_a", "isa", "instance_of", "rdf:type"}

// key identifies an edge. The predicate is folded so that WORKS_AT and
// works_at are the same claim.
type key struct{ from, label, to string }

// Graph holds nodes and the edges between them. Insertion order is preserved
// so every listing and every traversal is reproducible.
type Graph struct {
	types map[string]bool
	node  map[string]*Node
	edge  map[key]*Edge
	ord   []string // node ids, first asserted first
	eord  []key    // edge keys, first asserted first
	out   map[string][]key
	in    map[string][]key
}

// Build folds triples into a graph. Predicates named in types state a node's
// type instead of making an edge; with none given, the package Types apply.
// Build(nil) is the empty graph, ready for Add.
func Build(ts []semantic.Triple, types ...string) *Graph {
	if len(types) == 0 {
		types = Types
	}
	g := &Graph{
		types: make(map[string]bool, len(types)),
		node:  map[string]*Node{},
		edge:  map[key]*Edge{},
		out:   map[string][]key{},
		in:    map[string][]key{},
	}
	for _, t := range types {
		g.types[Fold(t)] = true
	}
	for _, t := range ts {
		g.Add(t)
	}
	return g
}

// Add asserts one triple and reports whether it said anything: a triple
// missing a subject, predicate or object is dropped. Repeating an assertion
// raises its count, keeps the highest confidence seen, and records the
// further source; it never makes a second edge. A zero Score counts as 1,
// because an extractor that states no confidence is not stating no
// confidence.
func (g *Graph) Add(t semantic.Triple) bool {
	s, o := Fold(t.Subject), Fold(t.Object)
	if s == "" || o == "" || strings.TrimSpace(t.Predicate) == "" {
		return false
	}
	score := t.Score
	if score == 0 {
		score = 1
	}
	doc := t.From.DocID
	n := g.touch(s, t.Subject, score, doc)
	if g.types[Fold(t.Predicate)] {
		if n.Type == "" {
			n.Type = strings.TrimSpace(t.Object)
		}
		return true
	}
	g.touch(o, t.Object, score, doc)
	g.link(s, t.Predicate, o, score, doc)
	return true
}

func (g *Graph) touch(id, name string, score float64, doc string) *Node {
	n := g.node[id]
	if n == nil {
		n = &Node{ID: id, Name: strings.TrimSpace(name)}
		g.node[id] = n
		g.ord = append(g.ord, id)
	}
	if score > n.Score {
		n.Score = score
	}
	n.Docs = keep(n.Docs, doc)
	return n
}

func (g *Graph) link(from, label, to string, score float64, doc string) *Edge {
	k := key{from, Fold(label), to}
	e := g.edge[k]
	if e == nil {
		e = &Edge{From: from, To: to, Label: strings.TrimSpace(label)}
		g.edge[k] = e
		g.eord = append(g.eord, k)
		g.out[from] = append(g.out[from], k)
		g.in[to] = append(g.in[to], k)
	}
	e.Count++
	if score > e.Score {
		e.Score = score
	}
	e.Docs = keep(e.Docs, doc)
	return e
}

// Set records a node and its properties, creating it if this is the first the
// graph has heard of it, and reports whether the id was usable. Properties
// already present are left alone: the first writer of a value wins. It is how
// a node with no assertions yet — the isolated node analytics must still
// count — gets into the graph.
func (g *Graph) Set(id string, props map[string]any) bool {
	folded := Fold(id)
	if folded == "" {
		return false
	}
	n := g.touch(folded, id, 0, "")
	for k, v := range props {
		if n.Props == nil {
			n.Props = make(map[string]any, len(props))
		}
		if _, ok := n.Props[k]; !ok {
			n.Props[k] = v
		}
	}
	return true
}

// Node returns the node with an id and whether the graph has one.
func (g *Graph) Node(id string) (Node, bool) {
	n := g.node[Fold(id)]
	if n == nil {
		return Node{}, false
	}
	return n.copy(), true
}

// Nodes lists every node, first asserted first.
func (g *Graph) Nodes() []Node {
	out := make([]Node, 0, len(g.ord))
	for _, id := range g.ord {
		out = append(out, g.node[id].copy())
	}
	return out
}

// Edges lists every edge, first asserted first.
func (g *Graph) Edges() []Edge {
	out := make([]Edge, 0, len(g.eord))
	for _, k := range g.eord {
		out = append(out, g.edge[k].copy())
	}
	return out
}

// Size reports how many nodes and how many edges the graph holds.
func (g *Graph) Size() (nodes, edges int) { return len(g.ord), len(g.eord) }

// Out lists the nodes this one points at.
func (g *Graph) Out(id string) []string {
	var out []string
	for _, k := range g.out[Fold(id)] {
		out = keep(out, k.to)
	}
	return out
}

// In lists the nodes that point at this one.
func (g *Graph) In(id string) []string {
	var out []string
	for _, k := range g.in[Fold(id)] {
		out = keep(out, k.from)
	}
	return out
}

// From lists the assertions made out of a node, first asserted first, and To
// those made into it. Out and In name the far end and nothing else; these
// carry the label, the confidence and the documents along with it, which is
// what a caller that means to cite its evidence needs.
func (g *Graph) From(id string) []Edge { return g.edges(g.out[Fold(id)]) }

// To lists the assertions made into a node.
func (g *Graph) To(id string) []Edge { return g.edges(g.in[Fold(id)]) }

func (g *Graph) edges(ks []key) []Edge {
	out := make([]Edge, 0, len(ks))
	for _, k := range ks {
		out = append(out, g.edge[k].copy())
	}
	return out
}

// Adj lists the neighbours of a node in either direction, out first.
func (g *Graph) Adj(id string) []string {
	id = Fold(id)
	var out []string
	for _, k := range g.out[id] {
		if k.to != id {
			out = keep(out, k.to)
		}
	}
	for _, k := range g.in[id] {
		if k.from != id {
			out = keep(out, k.from)
		}
	}
	return out
}

// Sub is the subgraph induced by a set of nodes: those nodes, and every edge
// with both ends among them.
func (g *Graph) Sub(ids ...string) *Graph {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[Fold(id)] = true
	}
	return g.pick(want)
}

// Near is the neighbourhood of a node: everything reachable within hops
// steps, ignoring direction, as a graph in its own right. Zero hops is the
// node alone; a node the graph does not hold gives an empty graph.
func (g *Graph) Near(id string, hops int) *Graph {
	id = Fold(id)
	if g.node[id] == nil {
		return g.pick(nil)
	}
	seen := map[string]bool{id: true}
	front := []string{id}
	for i := 0; i < hops && len(front) > 0; i++ {
		var next []string
		for _, n := range front {
			for _, m := range g.Adj(n) {
				if !seen[m] {
					seen[m] = true
					next = append(next, m)
				}
			}
		}
		front = next
	}
	return g.pick(seen)
}

func (g *Graph) pick(want map[string]bool) *Graph {
	s := &Graph{
		types: g.types,
		node:  map[string]*Node{},
		edge:  map[key]*Edge{},
		out:   map[string][]key{},
		in:    map[string][]key{},
	}
	for _, id := range g.ord {
		if !want[id] {
			continue
		}
		n := g.node[id].copy()
		s.node[id] = &n
		s.ord = append(s.ord, id)
	}
	for _, k := range g.eord {
		if !want[k.from] || !want[k.to] {
			continue
		}
		e := g.edge[k].copy()
		s.edge[k] = &e
		s.eord = append(s.eord, k)
		s.out[k.from] = append(s.out[k.from], k)
		s.in[k.to] = append(s.in[k.to], k)
	}
	return s
}

// Merge folds one node into another: edges, documents, properties and the
// higher confidence move to into, and from becomes one of its aliases. It is
// the mechanism an entity resolver drives — kg performs the merge, it does
// not decide which two names are the same thing. It reports whether anything
// moved.
func (g *Graph) Merge(into, from string) bool {
	into, from = Fold(into), Fold(from)
	a, b := g.node[into], g.node[from]
	if a == nil || b == nil || into == from {
		return false
	}
	if a.Name == "" {
		a.Name = b.Name
	}
	if a.Type == "" {
		a.Type = b.Type
	}
	if b.Score > a.Score {
		a.Score = b.Score
	}
	for _, d := range b.Docs {
		a.Docs = keep(a.Docs, d)
	}
	for k, v := range b.Props {
		if a.Props == nil {
			a.Props = map[string]any{}
		}
		if _, ok := a.Props[k]; !ok {
			a.Props[k] = v
		}
	}
	a.Alias = keep(a.Alias, from)
	for _, al := range b.Alias {
		a.Alias = keep(a.Alias, al)
	}

	delete(g.node, from)
	g.ord = drop(g.ord, from)

	old, oldOrd := g.edge, g.eord
	g.edge, g.eord = map[key]*Edge{}, nil
	g.out, g.in = map[string][]key{}, map[string][]key{}
	for _, k := range oldOrd {
		e := old[k]
		if k.from == from {
			k.from, e.From = into, into
		}
		if k.to == from {
			k.to, e.To = into, into
		}
		prior := g.edge[k]
		if prior == nil {
			g.edge[k] = e
			g.eord = append(g.eord, k)
			g.out[k.from] = append(g.out[k.from], k)
			g.in[k.to] = append(g.in[k.to], k)
			continue
		}
		prior.Count += e.Count
		if e.Score > prior.Score {
			prior.Score = e.Score
		}
		for _, d := range e.Docs {
			prior.Docs = keep(prior.Docs, d)
		}
	}
	return true
}

func (n *Node) copy() Node {
	c := *n
	c.Docs, c.Alias = clone(n.Docs), clone(n.Alias)
	if n.Props != nil {
		c.Props = make(map[string]any, len(n.Props))
		maps.Copy(c.Props, n.Props)
	}
	return c
}

func (e *Edge) copy() Edge {
	c := *e
	c.Docs = clone(e.Docs)
	return c
}

// keep appends v unless it is empty or already there, which is how every list
// in this package stays a set with an order.
func keep(list []string, v string) []string {
	if v == "" {
		return list
	}
	if slices.Contains(list, v) {
		return list
	}
	return append(list, v)
}

func drop(list []string, v string) []string {
	out := list[:0]
	for _, x := range list {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

func clone(list []string) []string {
	if list == nil {
		return nil
	}
	return append([]string(nil), list...)
}

// Step is one assertion a walk reached and how far out it was reached.
type Step struct {
	Edge
	Hop int
}

// Walk lists the assertions within hops steps of a node, nearest first.
//
// It is the edge-wise counterpart of Near. Near says which nodes lie close and
// gives them back as a graph; Walk says which assertions do, and an assertion
// is what carries a label, a confidence and the documents behind it. A caller
// that walks in order to cite what it found wants the assertions, and building
// a subgraph to read the edges off it copies the neighbourhood to look at it.
//
// Direction is ignored while walking, since an assertion relates both its ends
// however it happened to be written. Each edge is reported once, at the fewest
// hops any path reached it. Zero hops, or a node the graph does not hold,
// walks nowhere.
func (g *Graph) Walk(id string, hops int) []Step {
	id = Fold(id)
	if g.node[id] == nil || hops < 1 {
		return nil
	}
	var out []Step
	var next []string
	hop := 1
	seen := map[key]bool{}
	at := map[string]bool{id: true}
	cross := func(k key, far string) {
		if !seen[k] {
			seen[k] = true
			out = append(out, Step{Edge: g.edge[k].copy(), Hop: hop})
		}
		if !at[far] {
			at[far] = true
			next = append(next, far)
		}
	}
	for front := []string{id}; hop <= hops && len(front) > 0; hop, front, next = hop+1, next, nil {
		for _, n := range front {
			for _, k := range g.out[n] {
				cross(k, k.to)
			}
			for _, k := range g.in[n] {
				cross(k, k.from)
			}
		}
	}
	return out
}
