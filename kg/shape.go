package kg

import (
	"math"
	"sort"
)

// Analytics read the graph as undirected: an assertion from A to B and one
// from B to A are the same link, and a self-assertion is not a link at all.
// Direction is what Out and In are for.
const (
	iterations = 100  // ceiling for the two power iterations
	tolerance  = 1e-6 // when successive iterations stop moving
	damping    = 0.85 // the random walker's chance of following an edge
)

// Span is the low, high and mean of a distribution, used here for degree.
type Span struct {
	Min int
	Max int
	Avg float64
}

// Stat is what the graph looks like from a distance.
type Stat struct {
	Nodes   int     // nodes
	Edges   int     // asserted edges
	Links   int     // distinct undirected links between different nodes
	Density float64 // links over the links a complete graph would have
	Deg     Span    // neighbours per node
	Parts   int     // connected components
	Biggest int     // nodes in the largest component
	Whole   bool    // one component, so everything reaches everything
	Path    float64 // mean shortest path over the pairs that have one
	Shape   string  // disconnected, sparse, moderate or dense
}

// Stat measures the graph.
func (g *Graph) Stat() Stat {
	adj := g.adj()
	n := len(g.ord)
	s := Stat{Nodes: n, Edges: len(g.eord)}

	total, low := 0, math.MaxInt
	for _, id := range g.ord {
		d := len(adj[id])
		total += d
		if d < low {
			low = d
		}
		if d > s.Deg.Max {
			s.Deg.Max = d
		}
	}
	if n > 0 {
		s.Deg.Min = low
		s.Deg.Avg = float64(total) / float64(n)
	}
	s.Links = total / 2
	if n > 1 {
		s.Density = float64(s.Links) / (float64(n) * float64(n-1) / 2)
	}

	parts := g.Parts()
	s.Parts = len(parts)
	for _, p := range parts {
		if len(p) > s.Biggest {
			s.Biggest = len(p)
		}
	}
	s.Whole = s.Parts == 1

	sum, pairs := 0, 0
	for _, id := range g.ord {
		for to, d := range walk(adj, id) {
			if to != id && d > 0 {
				sum += d
				pairs++
			}
		}
	}
	if pairs > 0 {
		s.Path = float64(sum) / float64(pairs)
	}

	switch {
	case s.Parts > 1:
		s.Shape = "disconnected"
	case s.Density > 0.5:
		s.Shape = "dense"
	case s.Density < 0.1:
		s.Shape = "sparse"
	default:
		s.Shape = "moderate"
	}
	return s
}

// Parts are the connected components, largest first and then in the order
// their first node was asserted. Every node is in exactly one.
func (g *Graph) Parts() [][]string {
	adj := g.adj()
	seen := map[string]bool{}
	var parts [][]string
	for _, id := range g.ord {
		if seen[id] {
			continue
		}
		var part []string
		queue := []string{id}
		seen[id] = true
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			part = append(part, cur)
			for _, next := range adj[cur] {
				if !seen[next] {
					seen[next] = true
					queue = append(queue, next)
				}
			}
		}
		parts = append(parts, part)
	}
	sort.SliceStable(parts, func(i, j int) bool { return len(parts[i]) > len(parts[j]) })
	return parts
}

// Path is the shortest route between two nodes, ignoring direction, as the
// nodes along it including both ends. It is nil when there is no route; the
// number of steps is one less than its length.
func (g *Graph) Path(from, to string) []string {
	from, to = Fold(from), Fold(to)
	if g.node[from] == nil || g.node[to] == nil {
		return nil
	}
	if from == to {
		return []string{from}
	}
	adj := g.adj()
	prev := map[string]string{from: ""}
	queue := []string{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range adj[cur] {
			if _, ok := prev[next]; ok {
				continue
			}
			prev[next] = cur
			if next == to {
				return trace(prev, to)
			}
			queue = append(queue, next)
		}
	}
	return nil
}

func trace(prev map[string]string, to string) []string {
	var path []string
	for at := to; at != ""; at = prev[at] {
		path = append(path, at)
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

// Bridges are the links whose loss would split the graph further. Removing
// one raises the number of components by exactly one, which makes them the
// edges a knowledge graph can least afford to be wrong about.
//
// This is Tarjan's rule: a link u–v bridges when nothing reachable from v
// without crossing it was discovered before u.
func (g *Graph) Bridges() []Edge {
	adj := g.adj()
	seen := map[string]int{}
	low := map[string]int{}
	cut := map[[2]string]bool{}
	clock := 0

	var walkFrom func(at, from string)
	walkFrom = func(at, from string) {
		clock++
		seen[at], low[at] = clock, clock
		used := false
		for _, next := range adj[at] {
			if next == from && !used {
				used = true // the edge we arrived by, crossed once
				continue
			}
			if seen[next] == 0 {
				walkFrom(next, at)
				if low[next] < low[at] {
					low[at] = low[next]
				}
				if low[next] > seen[at] {
					cut[pair(at, next)] = true
				}
			} else if seen[next] < low[at] {
				low[at] = seen[next]
			}
		}
	}
	for _, id := range g.ord {
		if seen[id] == 0 {
			walkFrom(id, "")
		}
	}

	var out []Edge
	for _, k := range g.eord {
		if k.from != k.to && cut[pair(k.from, k.to)] {
			out = append(out, g.edge[k].copy())
		}
	}
	return out
}

func pair(a, b string) [2]string {
	if a > b {
		a, b = b, a
	}
	return [2]string{a, b}
}

// Degree scores each node by how many neighbours it has, over the most it
// could have. A node joined to everything scores 1.
func (g *Graph) Degree() map[string]float64 {
	adj := g.adj()
	out := make(map[string]float64, len(g.ord))
	div := float64(len(g.ord) - 1)
	if div < 1 {
		div = 1
	}
	for _, id := range g.ord {
		out[id] = float64(len(adj[id])) / div
	}
	return out
}

// Close scores each node by how near it is to the rest: the nodes it reaches
// over the total distance to them. A node one step from everything scores 1,
// and one that reaches nothing scores 0.
func (g *Graph) Close() map[string]float64 {
	adj := g.adj()
	out := make(map[string]float64, len(g.ord))
	for _, id := range g.ord {
		sum, reached := 0, 0
		for to, d := range walk(adj, id) {
			if to != id {
				sum += d
				reached++
			}
		}
		if sum > 0 {
			out[id] = float64(reached) / float64(sum)
		}
	}
	return out
}

// Between scores each node by how many shortest paths run through it, over
// the pairs it could serve. It finds the nodes a graph would come apart
// without, which is not the same as the nodes with the most edges.
//
// Brandes' accumulation: one pass per node, dependencies summed backwards
// along the paths that pass through.
func (g *Graph) Between() map[string]float64 {
	adj := g.adj()
	score := make(map[string]float64, len(g.ord))
	for _, id := range g.ord {
		score[id] = 0
	}
	for _, src := range g.ord {
		var order []string
		prev := map[string][]string{}
		paths := map[string]float64{src: 1}
		dist := map[string]int{src: 0}
		queue := []string{src}
		for len(queue) > 0 {
			at := queue[0]
			queue = queue[1:]
			order = append(order, at)
			for _, next := range adj[at] {
				d, seen := dist[next]
				if !seen {
					dist[next] = dist[at] + 1
					d = dist[next]
					queue = append(queue, next)
				}
				if d == dist[at]+1 {
					paths[next] += paths[at]
					prev[next] = append(prev[next], at)
				}
			}
		}
		owed := map[string]float64{}
		for i := len(order) - 1; i >= 0; i-- {
			at := order[i]
			for _, up := range prev[at] {
				owed[up] += paths[up] / paths[at] * (1 + owed[at])
			}
			if at != src {
				score[at] += owed[at]
			}
		}
	}
	// Each pair was counted from both ends, and the pairs a node can sit
	// between number (n-1)(n-2)/2.
	n := float64(len(g.ord))
	if n > 2 {
		div := (n - 1) * (n - 2)
		for id := range score {
			score[id] /= div
		}
	}
	return score
}

// Eigen scores each node by the scores of its neighbours, settled by power
// iteration and scaled so the highest is 1. Being joined to important nodes
// counts for more than being joined to many.
func (g *Graph) Eigen() map[string]float64 {
	adj := g.adj()
	n := len(g.ord)
	out := make(map[string]float64, n)
	if n == 0 {
		return out
	}
	x := make(map[string]float64, n)
	start := 1 / math.Sqrt(float64(n))
	for _, id := range g.ord {
		x[id] = start
	}
	for i := 0; i < iterations; i++ {
		next := make(map[string]float64, n)
		var norm float64
		for _, id := range g.ord {
			// Iterate with A+I rather than A. A bipartite graph's adjacency
			// matrix has its eigenvalues in ± pairs, so plain power iteration
			// swings between the two forever and settles on nothing: a star
			// alternates between its centre and its leaves. Adding the
			// identity lifts every eigenvalue by one and moves no eigenvector,
			// so the same answer arrives and it arrives.
			sum := x[id]
			for _, m := range adj[id] {
				sum += x[m]
			}
			next[id] = sum
			norm += sum * sum
		}
		norm = math.Sqrt(norm)
		if norm == 0 {
			break
		}
		var move float64
		for id, v := range next {
			v /= norm
			next[id] = v
			move += (v - x[id]) * (v - x[id])
		}
		x = next
		if math.Sqrt(move) < tolerance {
			break
		}
	}
	var top float64
	for _, v := range x {
		if v > top {
			top = v
		}
	}
	for id, v := range x {
		if top > 0 {
			v /= top
		}
		out[id] = v
	}
	return out
}

// Rank is PageRank over the asserted direction: the share of a random walker's
// time spent at each node, following edges with probability damping and
// jumping anywhere with the rest. Scores sum to 1. A node with no outgoing
// edges hands its share to the jump rather than losing it.
func (g *Graph) Rank() map[string]float64 {
	n := len(g.ord)
	rank := make(map[string]float64, n)
	if n == 0 {
		return rank
	}
	share := 1 / float64(n)
	for _, id := range g.ord {
		rank[id] = share
	}
	// A pair joined by two labels is one way for the walker to go, so the
	// neighbours are taken once each, and taken once for the whole run.
	outs := make(map[string][]string, n)
	for _, k := range g.eord {
		outs[k.from] = keep(outs[k.from], k.to)
	}
	for i := 0; i < iterations; i++ {
		next := make(map[string]float64, n)
		var loose float64
		for _, id := range g.ord {
			out := outs[id]
			if len(out) == 0 {
				loose += rank[id]
				continue
			}
			part := rank[id] / float64(len(out))
			for _, to := range out {
				next[to] += part
			}
		}
		var move float64
		for _, id := range g.ord {
			v := (1-damping)*share + damping*(next[id]+loose*share)
			move += math.Abs(v - rank[id])
			next[id] = v
		}
		rank = next
		if move < tolerance {
			break
		}
	}
	return rank
}

// Groups partitions the nodes into communities by moving each node to the
// neighbouring group that raises modularity most, until nothing moves. Nodes
// with no edges stay alone. Groups come back largest first.
func (g *Graph) Groups() [][]string {
	adj := g.adj()
	m := 0
	for _, id := range g.ord {
		m += len(adj[id])
	}
	m /= 2
	group := make(map[string]string, len(g.ord))
	for _, id := range g.ord {
		group[id] = id
	}
	if m > 0 {
		weight := make(map[string]float64, len(g.ord)) // degree summed per group
		for _, id := range g.ord {
			weight[id] = float64(len(adj[id]))
		}
		two := 2 * float64(m)
		for i := 0; i < iterations; i++ {
			moved := false
			for _, id := range g.ord {
				deg := float64(len(adj[id]))
				here := group[id]
				weight[here] -= deg

				ties := map[string]float64{here: 0}
				for _, next := range adj[id] {
					ties[group[next]]++
				}
				best, gain := here, ties[here]-weight[here]*deg/two
				for _, next := range adj[id] { // neighbour order, so ties break by assertion order
					to := group[next]
					if to == best {
						continue
					}
					if q := ties[to] - weight[to]*deg/two; q > gain {
						best, gain = to, q
					}
				}
				weight[best] += deg
				if best != here {
					group[id] = best
					moved = true
				}
			}
			if !moved {
				break
			}
		}
	}
	return gather(g.ord, group)
}

// Modularity scores a partition: how much more of the graph's links fall
// inside its groups than chance would put there. Above about 0.3 is
// community structure worth believing; 0 is what random groups get.
func (g *Graph) Modularity(groups [][]string) float64 {
	adj := g.adj()
	m := 0
	for _, id := range g.ord {
		m += len(adj[id])
	}
	m /= 2
	if m == 0 {
		return 0
	}
	of := map[string]int{}
	for i, grp := range groups {
		for _, id := range grp {
			of[id] = i + 1 // 0 means ungrouped
		}
	}
	inside := make(map[int]float64)
	total := make(map[int]float64)
	for _, id := range g.ord {
		c := of[id]
		if c == 0 {
			continue
		}
		total[c] += float64(len(adj[id]))
		for _, next := range adj[id] {
			if of[next] == c {
				inside[c]++
			}
		}
	}
	two := 2 * float64(m)
	var q float64
	for c, tot := range total {
		q += inside[c]/two - (tot/two)*(tot/two)
	}
	return q
}

// Pair is a node and its score.
type Pair struct {
	ID    string
	Score float64
}

// Top orders a score highest first, ties by id so the answer is the same
// every run. A count of zero or less returns everything.
func Top(score map[string]float64, n int) []Pair {
	out := make([]Pair, 0, len(score))
	for id, v := range score {
		out = append(out, Pair{id, v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID < out[j].ID
	})
	if n > 0 && n < len(out) {
		out = out[:n]
	}
	return out
}

// adj is the undirected view every measure here reads: neighbours per node,
// each listed once, self-links left out, in the order they were asserted.
func (g *Graph) adj() map[string][]string {
	adj := make(map[string][]string, len(g.ord))
	for _, id := range g.ord {
		adj[id] = nil
	}
	for _, k := range g.eord {
		if k.from == k.to {
			continue
		}
		adj[k.from] = keep(adj[k.from], k.to)
		adj[k.to] = keep(adj[k.to], k.from)
	}
	return adj
}

// walk returns the number of steps from one node to every node it reaches.
func walk(adj map[string][]string, from string) map[string]int {
	dist := map[string]int{from: 0}
	queue := []string{from}
	for len(queue) > 0 {
		at := queue[0]
		queue = queue[1:]
		for _, next := range adj[at] {
			if _, seen := dist[next]; !seen {
				dist[next] = dist[at] + 1
				queue = append(queue, next)
			}
		}
	}
	return dist
}

// gather turns a node-to-group map into groups, largest first, keeping the
// order nodes were asserted in.
func gather(order []string, group map[string]string) [][]string {
	at := map[string]int{}
	var out [][]string
	for _, id := range order {
		g := group[id]
		i, ok := at[g]
		if !ok {
			i = len(out)
			at[g] = i
			out = append(out, nil)
		}
		out[i] = append(out[i], id)
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}
