// Package agent holds what an agent knows and what it decided.
//
// What it knows is a graph. Every node and edge carries the span of time it
// is held true, so the graph can be asked how it stood at a moment rather
// than only how it stands now, and a fact can stop being current without
// being erased. What it decided is a node in that same graph, written with
// the evidence the choice rested on and the rule it was taken under. Because
// a decision is a node, "why did the agent do this" is a walk over edges, not
// a stored explanation that can drift from the graph it explains.
//
// The parts stay separable. [Graph] is the store, [Memory] the recent window,
// [Journal] the write path for decisions, [Cause] the read path for why,
// [Rules] the tests a choice must pass, and [Ken] composes the five for a
// caller who wants one thing instead of five. Journal, Cause and Rules
// satisfy decision.Recorder, decision.Cause and decision.Policy, so a caller
// may keep those interfaces and swap any implementation out.
//
// A refusal is a decision. It is recorded, with the rule that refused and the
// reason, and never returned as an error: an agent that was stopped still
// made a choice, and the reason it was stopped is what somebody will want to
// read later.
package agent

import (
	"sort"
	"sync"
	"time"
)

// Span is the stretch of time a fact is held true. A zero bound is open, so
// the zero Span holds always — which is what an ordinary fact wants.
type Span struct {
	From  time.Time
	Until time.Time
}

// Holds reports whether the span covers at.
func (s Span) Holds(at time.Time) bool {
	if !s.From.IsZero() && at.Before(s.From) {
		return false
	}
	if !s.Until.IsZero() && at.After(s.Until) {
		return false
	}
	return true
}

// Close ends the span at at. It only ever shortens: a span that already ended
// earlier keeps its own end, so closing something twice cannot revive it.
func (s Span) Close(at time.Time) Span {
	if s.Until.IsZero() || at.Before(s.Until) {
		s.Until = at
	}
	return s
}

// Node is one thing the agent knows about.
type Node struct {
	ID    string
	Kind  string // entity, decision, policy — the caller's vocabulary
	Text  string // what it says, and what keyword search reads
	Props map[string]any
	Scope Scope
	Span  Span
}

// Link names an edge by its ends and what it asserts. Two assertions with the
// same ends and label are the same edge, so repeating one updates it rather
// than growing the graph.
type Link struct {
	From  string
	To    string
	Label string
}

// Edge is one relation asserted between two nodes.
type Edge struct {
	Link
	Weight float64 // how much confidence survives a walk across it; 1 by default
	Props  map[string]any
	Span   Span
}

// Loss is the note left when a node or edge stops being current. A retraction
// closes the span and keeps the record; a purge removes the record and leaves
// only this note, which is what erasure needs: proof that something was
// removed, without the thing itself.
type Loss struct {
	Node   string // set when a node was lost
	Edge   Link   // set when an edge was lost
	At     time.Time
	Why    string
	Purged bool
	Via    string // the node whose retraction or purge reached this edge
}

// Op is what happened to a node or edge.
type Op string

const (
	OpAdd   Op = "add"   // asserted, or asserted again
	OpClose Op = "close" // retracted: kept, and no longer believed
	OpPurge Op = "purge" // erased: gone, with a tombstone left behind
)

// Change is one mutation, as handed to [Graph.Watch].
type Change struct {
	Op   Op
	Node string
	Edge Link
}

// Graph is the agent's knowledge: nodes, edges, and when each is true. Its
// zero value is an empty graph ready to write to, and it is safe for
// concurrent use.
type Graph struct {
	mu    sync.RWMutex
	nodes map[string]*Node
	order []string
	edges map[Link]*Edge
	seq   []Link
	out   map[string][]Link
	in    map[string][]Link
	kinds map[string]map[string]bool

	lost     []Loss
	lostNode map[string]int
	lostEdge map[Link]int
	doors    map[string][]Where

	// Watch, when set, is called once per change after the graph is
	// consistent and the lock is released. It is how an audit trail is kept
	// outside the graph, so the graph does not have to know what an audit is.
	Watch func(Change)
}

func (g *Graph) init() {
	if g.nodes == nil {
		g.nodes = map[string]*Node{}
		g.edges = map[Link]*Edge{}
		g.out = map[string][]Link{}
		g.in = map[string][]Link{}
		g.kinds = map[string]map[string]bool{}
		g.lostNode = map[string]int{}
		g.lostEdge = map[Link]int{}
	}
}

func (g *Graph) tell(cs ...Change) {
	if g.Watch == nil {
		return
	}
	for _, c := range cs {
		g.Watch(c)
	}
}

// Add writes a node, replacing any node of the same id. It reports whether
// the id was new. A node with an empty id is not a node, and is refused.
func (g *Graph) Add(n Node) bool {
	if n.ID == "" {
		return false
	}
	g.mu.Lock()
	g.init()
	_, had := g.nodes[n.ID]
	g.put(n)
	g.mu.Unlock()
	g.tell(Change{Op: OpAdd, Node: n.ID})
	return !had
}

// put writes a node. The caller holds the lock.
func (g *Graph) put(n Node) {
	if old, ok := g.nodes[n.ID]; ok {
		delete(g.kinds[old.Kind], n.ID)
	} else {
		g.order = append(g.order, n.ID)
	}
	kind := n.Kind
	if kind == "" {
		kind = kindEntity
	}
	n.Kind = kind
	if g.kinds[kind] == nil {
		g.kinds[kind] = map[string]bool{}
	}
	g.kinds[kind][n.ID] = true
	g.nodes[n.ID] = &n
}

// Join writes an edge, creating either end that does not exist yet as a bare
// entity. It reports whether the edge was new; an edge asserted again keeps
// its place and takes the new weight, properties and span.
func (g *Graph) Join(e Edge) bool {
	if e.From == "" || e.To == "" {
		return false
	}
	if e.Label == "" {
		e.Label = About
	}
	if e.Weight == 0 {
		e.Weight = 1
	}
	g.mu.Lock()
	g.init()
	var made []Change
	for _, id := range [2]string{e.From, e.To} {
		if _, ok := g.nodes[id]; !ok {
			g.put(Node{ID: id, Kind: kindEntity, Text: id})
			made = append(made, Change{Op: OpAdd, Node: id})
		}
	}
	_, had := g.edges[e.Link]
	if !had {
		g.seq = append(g.seq, e.Link)
		g.out[e.From] = append(g.out[e.From], e.Link)
		g.in[e.To] = append(g.in[e.To], e.Link)
	}
	g.edges[e.Link] = &e
	g.mu.Unlock()
	g.tell(append(made, Change{Op: OpAdd, Edge: e.Link})...)
	return !had
}

// Node reads one node.
func (g *Graph) Node(id string) (Node, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	n, ok := g.nodes[id]
	if !ok {
		return Node{}, false
	}
	return *n, true
}

// Edge reads one edge.
func (g *Graph) Edge(l Link) (Edge, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	e, ok := g.edges[l]
	if !ok {
		return Edge{}, false
	}
	return *e, true
}

// Nodes lists nodes of one kind in the order they were first written, or
// every node when kind is empty.
func (g *Graph) Nodes(kind string) []Node {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]Node, 0, len(g.order))
	for _, id := range g.order {
		n := g.nodes[id]
		if n == nil || (kind != "" && n.Kind != kind) {
			continue
		}
		out = append(out, *n)
	}
	return out
}

// Edges lists edges carrying one label in the order they were first written,
// or every edge when label is empty.
func (g *Graph) Edges(label string) []Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]Edge, 0, len(g.seq))
	for _, l := range g.seq {
		e := g.edges[l]
		if e == nil || (label != "" && e.Label != label) {
			continue
		}
		out = append(out, *e)
	}
	return out
}

// At is the graph as it stood at one moment: the nodes whose span covers at,
// and the edges that both cover at and still have both ends. An edge to a
// node that was not yet true is not a relation, so it is left out.
func (g *Graph) At(at time.Time) ([]Node, []Edge) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	live := map[string]bool{}
	var nodes []Node
	for _, id := range g.order {
		n := g.nodes[id]
		if n == nil || !n.Span.Holds(at) {
			continue
		}
		live[id] = true
		nodes = append(nodes, *n)
	}
	var edges []Edge
	for _, l := range g.seq {
		e := g.edges[l]
		if e == nil || !e.Span.Holds(at) || !live[e.From] || !live[e.To] {
			continue
		}
		edges = append(edges, *e)
	}
	return nodes, edges
}

// Out lists the edges leaving a node, in the order they were written.
func (g *Graph) Out(id string) []Edge { return g.side(g.outOf, id) }

// In lists the edges entering a node, in the order they were written.
func (g *Graph) In(id string) []Edge { return g.side(g.inTo, id) }

func (g *Graph) outOf(id string) []Link { return g.out[id] }
func (g *Graph) inTo(id string) []Link  { return g.in[id] }

func (g *Graph) side(pick func(string) []Link, id string) []Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	links := pick(id)
	out := make([]Edge, 0, len(links))
	for _, l := range links {
		if e := g.edges[l]; e != nil {
			out = append(out, *e)
		}
	}
	return out
}

// Retract closes a node's span at at, and every edge touching it. The record
// stays: a question asked of an earlier moment still finds the node, and a
// decision that rested on it stays explainable. Edges are closed with it
// because a live relation around a node that is no longer true reads as
// current when it is not.
//
// It reports whether the node was there and not already retracted.
func (g *Graph) Retract(id, why string, at time.Time) bool {
	at = when(at)
	g.mu.Lock()
	n, ok := g.nodes[id]
	if !ok {
		g.mu.Unlock()
		return false
	}
	if _, done := g.lostNode[id]; done {
		g.mu.Unlock()
		return false
	}
	n.Span = n.Span.Close(at)
	g.note(Loss{Node: id, At: at, Why: why})
	changes := []Change{{Op: OpClose, Node: id}}
	for _, l := range g.touching(id) {
		if _, done := g.lostEdge[l]; done {
			continue
		}
		g.edges[l].Span = g.edges[l].Span.Close(at)
		g.note(Loss{Edge: l, At: at, Why: why, Via: id})
		changes = append(changes, Change{Op: OpClose, Edge: l})
	}
	g.mu.Unlock()
	g.tell(changes...)
	return true
}

// Cut closes one edge's span, leaving its ends alone.
func (g *Graph) Cut(l Link, why string, at time.Time) bool {
	at = when(at)
	g.mu.Lock()
	e, ok := g.edges[l]
	if !ok {
		g.mu.Unlock()
		return false
	}
	if _, done := g.lostEdge[l]; done {
		g.mu.Unlock()
		return false
	}
	e.Span = e.Span.Close(at)
	g.note(Loss{Edge: l, At: at, Why: why})
	g.mu.Unlock()
	g.tell(Change{Op: OpClose, Edge: l})
	return true
}

// Purge removes a node and every edge touching it, keeping only the note that
// it was removed. Use it when the data itself has to be gone; use Retract
// when it merely stopped being true.
func (g *Graph) Purge(id, why string, at time.Time) bool {
	at = when(at)
	g.mu.Lock()
	if _, ok := g.nodes[id]; !ok {
		g.mu.Unlock()
		return false
	}
	changes := []Change{{Op: OpPurge, Node: id}}
	for _, l := range g.touching(id) {
		g.drop(l)
		g.note(Loss{Edge: l, At: at, Why: why, Purged: true, Via: id})
		changes = append(changes, Change{Op: OpPurge, Edge: l})
	}
	delete(g.kinds[g.nodes[id].Kind], id)
	delete(g.nodes, id)
	g.order = without(g.order, id)
	g.note(Loss{Node: id, At: at, Why: why, Purged: true})
	g.mu.Unlock()
	g.tell(changes...)
	return true
}

// Drop removes one edge, keeping only the note that it was removed.
func (g *Graph) Drop(l Link, why string, at time.Time) bool {
	at = when(at)
	g.mu.Lock()
	if _, ok := g.edges[l]; !ok {
		g.mu.Unlock()
		return false
	}
	g.drop(l)
	g.note(Loss{Edge: l, At: at, Why: why, Purged: true})
	g.mu.Unlock()
	g.tell(Change{Op: OpPurge, Edge: l})
	return true
}

// Losses lists every retraction and purge, oldest first.
func (g *Graph) Losses() []Loss {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return append([]Loss(nil), g.lost...)
}

// Lost reports the last thing that happened to a node, if it was retracted or
// purged.
func (g *Graph) Lost(id string) (Loss, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	i, ok := g.lostNode[id]
	if !ok {
		return Loss{}, false
	}
	return g.lost[i], true
}

// note records a loss and indexes it. The caller holds the lock.
func (g *Graph) note(l Loss) {
	g.init()
	g.lost = append(g.lost, l)
	i := len(g.lost) - 1
	if l.Node != "" {
		g.lostNode[l.Node] = i
		return
	}
	g.lostEdge[l.Edge] = i
}

// touching lists every edge with the node at either end. The caller holds the
// lock.
func (g *Graph) touching(id string) []Link {
	seen := map[Link]bool{}
	var out []Link
	for _, l := range append(append([]Link{}, g.out[id]...), g.in[id]...) {
		if !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	return out
}

// drop removes an edge from every index. The caller holds the lock.
func (g *Graph) drop(l Link) {
	delete(g.edges, l)
	g.seq = cut(g.seq, l)
	g.out[l.From] = cut(g.out[l.From], l)
	g.in[l.To] = cut(g.in[l.To], l)
}

// Stats counts what the graph holds.
type Stats struct {
	Nodes   int
	Edges   int
	Kinds   map[string]int
	Labels  map[string]int
	Density float64
}

// Stats measures the graph. Density is edges over the number of ordered pairs
// of distinct nodes, so a graph of fewer than two nodes has density zero.
func (g *Graph) Stats() Stats {
	g.mu.RLock()
	defer g.mu.RUnlock()
	s := Stats{Nodes: len(g.nodes), Edges: len(g.edges), Kinds: map[string]int{}, Labels: map[string]int{}}
	for _, n := range g.nodes {
		s.Kinds[n.Kind]++
	}
	for _, e := range g.edges {
		s.Labels[e.Label]++
	}
	if s.Nodes > 1 {
		s.Density = float64(s.Edges) / float64(s.Nodes*(s.Nodes-1))
	}
	return s
}

// Hit is a node a query matched and how well.
type Hit struct {
	Node  Node
	Score float64
}

// Find scores every node against a keyword query by the share of query words
// its text carries, and returns those that carry at least one, best first.
// Ties keep the order the nodes were written in, so the answer is stable.
func (g *Graph) Find(q string) []Hit {
	terms := words(q)
	if len(terms) == 0 {
		return nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	var hits []Hit
	for _, id := range g.order {
		n := g.nodes[id]
		if n == nil {
			continue
		}
		have := set(words(n.Text))
		found := 0
		for _, t := range terms {
			if have[t] {
				found++
			}
		}
		if found == 0 {
			continue
		}
		hits = append(hits, Hit{Node: *n, Score: float64(found) / float64(len(terms))})
	}
	sortHits(hits)
	return hits
}

// sortHits orders by score, best first, keeping the order the nodes were
// written in for ties so the same question twice gives the same answer.
func sortHits(hits []Hit) {
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
}

// when fills in an unset time with now, in UTC.
func when(at time.Time) time.Time {
	if at.IsZero() {
		return time.Now().UTC()
	}
	return at.UTC()
}

func cut(ls []Link, l Link) []Link {
	out := ls[:0]
	for _, x := range ls {
		if x != l {
			out = append(out, x)
		}
	}
	return out
}

func without(ss []string, s string) []string {
	out := ss[:0]
	for _, x := range ss {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}
