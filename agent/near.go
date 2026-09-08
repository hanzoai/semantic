package agent

import (
	"sort"
	"time"
)

// Band is how far apart two nodes are, said in words. Hop counts are exact
// but not comparable across graphs of different density; the band is what a
// reader should act on.
type Band string

const (
	BandDirect Band = "direct" // one edge, or none
	BandNear   Band = "near"   // two or three
	BandMid    Band = "mid"    // four to six: reachable, but separated
	BandFar    Band = "far"    // seven or more: weakly coupled
)

// Apart names the band a hop count falls in.
func Apart(hops int) Band {
	switch {
	case hops <= 1:
		return BandDirect
	case hops <= 3:
		return BandNear
	case hops <= 6:
		return BandMid
	}
	return BandFar
}

// Reach says how far a walk may go and what it may follow. Its zero value
// walks one hop over every edge, ignoring time.
type Reach struct {
	Hops   int       // edges deep; zero means one
	Labels []string  // follow only these labels; empty follows every label
	Min    float64   // do not cross an edge weaker than this
	Floor  float64   // drop what arrives with less confidence than this
	At     time.Time // walk the graph as it stood then; zero ignores spans
}

func (r Reach) depth() int {
	if r.Hops < 1 {
		return 1
	}
	return r.Hops
}

func (r Reach) follows(e *Edge) bool {
	if e.Weight < r.Min {
		return false
	}
	if !r.At.IsZero() && !e.Span.Holds(r.At) {
		return false
	}
	if len(r.Labels) == 0 {
		return true
	}
	for _, l := range r.Labels {
		if e.Label == l {
			return true
		}
	}
	return false
}

// Step is one node a walk reached, and what the walk cost to get there.
type Step struct {
	Node   Node
	Label  string  // the edge followed to arrive
	Weight float64 // that edge's weight
	Hop    int
	Decay  float64 // the product of every weight along the path
	Path   []string
	Band   Band
}

// Near walks out from a node and reports what it reaches. Confidence decays
// by the weight of each edge crossed, so a fact three weak edges away arrives
// marked as the weak evidence it is rather than as a neighbour.
//
// Each node is reached once, by the first path that gets there, and the
// result is ordered nearest first and strongest first within a hop.
func (g *Graph) Near(id string, r Reach) []Step {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if _, ok := g.nodes[id]; !ok {
		return nil
	}
	type walk struct {
		id    string
		hop   int
		decay float64
		path  []string
	}
	queue := []walk{{id: id, decay: 1, path: []string{id}}}
	seen := map[string]bool{id: true}
	var out []Step
	for len(queue) > 0 {
		w := queue[0]
		queue = queue[1:]
		if w.hop >= r.depth() {
			continue
		}
		for _, l := range g.out[w.id] {
			e := g.edges[l]
			if e == nil || seen[e.To] || !r.follows(e) {
				continue
			}
			n := g.nodes[e.To]
			if n == nil || (!r.At.IsZero() && !n.Span.Holds(r.At)) {
				continue
			}
			seen[e.To] = true
			step := walk{
				id:    e.To,
				hop:   w.hop + 1,
				decay: w.decay * e.Weight,
				path:  append(append([]string{}, w.path...), e.To),
			}
			queue = append(queue, step)
			if step.decay < r.Floor {
				continue
			}
			out = append(out, Step{
				Node:   *n,
				Label:  e.Label,
				Weight: e.Weight,
				Hop:    step.hop,
				Decay:  step.decay,
				Path:   step.path,
				Band:   Apart(step.hop),
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Hop != out[j].Hop {
			return out[i].Hop < out[j].Hop
		}
		return out[i].Decay > out[j].Decay
	})
	return out
}

// Where is one node in one graph, since a route may cross into another.
type Where struct {
	Graph *Graph
	ID    string
}

// Route is a way from one node to another: the fewest edges, not the
// strongest chain, so Decay describes the route found rather than the best
// route available.
type Route struct {
	Path  []Where
	Hops  int
	Decay float64
	Doors int // boundaries between graphs the route crossed
	Band  Band
	Found bool
}

// IDs is the route as node ids.
func (r Route) IDs() []string {
	out := make([]string, len(r.Path))
	for i, w := range r.Path {
		out[i] = w.ID
	}
	return out
}

// Path finds a way between two nodes of this graph. Unlike Near, which walks
// one hop unless told otherwise, a search with no Hops set looks up to ten
// edges deep: the caller is asking whether the two are connected at all.
func (g *Graph) Path(from, to string, r Reach) Route { return g.route(from, g, to, r) }

// Door opens from a node here onto a node in another graph, so a walk can
// leave this graph and continue there without the two being merged. It is how
// one task's context stays its own while still being reachable from another.
func (g *Graph) Door(from string, to *Graph, id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.init()
	if g.doors == nil {
		g.doors = map[string][]Where{}
	}
	g.doors[from] = append(g.doors[from], Where{Graph: to, ID: id})
}

// Cross finds a way from a node here to a node in another graph, through
// however many doors it takes. Hops bounds the whole route, doors included,
// and defaults to ten as in Path.
func (g *Graph) Cross(from string, to *Graph, id string, r Reach) Route {
	return g.route(from, to, id, r)
}

// route is one breadth-first search over this graph and everything its doors
// open onto.
func (g *Graph) route(from string, dst *Graph, to string, r Reach) Route {
	if _, ok := g.Node(from); !ok {
		return Route{}
	}
	if _, ok := dst.Node(to); !ok {
		return Route{}
	}
	hops := r.Hops
	if hops < 1 {
		hops = 10
	}
	type walk struct {
		at    Where
		path  []Where
		hop   int
		decay float64
		doors int
	}
	start := Where{Graph: g, ID: from}
	goal := Where{Graph: dst, ID: to}
	queue := []walk{{at: start, path: []Where{start}, decay: 1}}
	seen := map[Where]bool{start: true}
	for len(queue) > 0 {
		w := queue[0]
		queue = queue[1:]
		if w.at == goal {
			return Route{Path: w.path, Hops: w.hop, Decay: w.decay, Doors: w.doors, Band: Apart(w.hop), Found: true}
		}
		if w.hop >= hops {
			continue
		}
		cur := w.at.Graph
		cur.mu.RLock()
		var next []Edge
		for _, l := range cur.out[w.at.ID] {
			e := cur.edges[l]
			if e == nil || !r.follows(e) {
				continue
			}
			if n := cur.nodes[e.To]; n == nil || (!r.At.IsZero() && !n.Span.Holds(r.At)) {
				continue
			}
			next = append(next, *e)
		}
		doors := append([]Where(nil), cur.doors[w.at.ID]...)
		cur.mu.RUnlock()

		for _, e := range next {
			at := Where{Graph: cur, ID: e.To}
			if seen[at] {
				continue
			}
			seen[at] = true
			queue = append(queue, walk{at, append(append([]Where{}, w.path...), at), w.hop + 1, w.decay * e.Weight, w.doors})
		}
		for _, d := range doors {
			if seen[d] {
				continue
			}
			seen[d] = true
			queue = append(queue, walk{d, append(append([]Where{}, w.path...), d), w.hop + 1, w.decay, w.doors + 1})
		}
	}
	return Route{}
}
