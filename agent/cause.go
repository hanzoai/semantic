package agent

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/hanzoai/semantic/decision"
)

// The three ways one decision can bear on another. Nothing else is causal, so
// a walk over these labels is a walk over causality and over nothing else —
// which is what keeps "why did the agent do this" from wandering into every
// entity the decision merely mentioned.
const (
	Caused     = "caused"     // the earlier decision brought the later one about
	Influenced = "influenced" // it weighed on the later one without settling it
	Precedes   = "precedes"   // it is the precedent the later one followed
)

func causal(label string) bool {
	return label == Caused || label == Influenced || label == Precedes
}

// Cause reads a graph as causality: what led to a decision, what it led to,
// how far back it reaches and how much of the claim survives the distance.
//
// Its zero value reads an empty graph. It satisfies decision.Cause.
type Cause struct{ G *Graph }

var _ decision.Cause = (*Cause)(nil)

func (c *Cause) graph() *Graph {
	if c.G == nil {
		c.G = &Graph{}
	}
	return c.G
}

func (c *Cause) journal() *Journal { return &Journal{G: c.graph()} }

// Away is a decision reached by walking causality, and how many steps away it
// was found.
type Away struct {
	Decision Decision
	Hops     int
}

// Up walks back from a decision to what led to it, farthest first, so the
// answer reads as a story: this, because of that, because of the thing before
// it. A depth under one means ten.
func (c *Cause) Up(id string, depth int) []Away {
	out := c.walk(id, depth, false)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Hops > out[j].Hops })
	return out
}

// Down walks forward from a decision to what it led to, nearest first.
func (c *Cause) Down(id string, depth int) []Away {
	return c.walk(id, depth, true)
}

// walk is breadth-first over causal edges in one direction, following only
// the labels named or every causal label when none are. Nearest first, each
// decision reached once.
func (c *Cause) walk(id string, depth int, forward bool, labels ...string) []Away {
	g, j := c.graph(), c.journal()
	if n, ok := g.Node(id); !ok || n.Kind != kindDecision {
		return nil
	}
	if depth < 1 {
		depth = 10
	}
	type step struct {
		id  string
		hop int
	}
	queue := []step{{id: id}}
	seen := map[string]bool{id: true}
	var out []Away
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		if s.hop >= depth {
			continue
		}
		for _, e := range c.edges(s.id, forward, labels...) {
			next := e.To
			if !forward {
				next = e.From
			}
			if seen[next] {
				continue
			}
			seen[next] = true
			n, ok := g.Node(next)
			if !ok || n.Kind != kindDecision {
				continue
			}
			queue = append(queue, step{next, s.hop + 1})
			out = append(out, Away{Decision: j.decide(n), Hops: s.hop + 1})
		}
	}
	return out
}

// edges is the causal edges leaving a decision, or entering it, narrowed to
// the labels named.
func (c *Cause) edges(id string, forward bool, labels ...string) []Edge {
	g := c.graph()
	var all []Edge
	if forward {
		all = g.Out(id)
	} else {
		all = g.In(id)
	}
	want := set(labels)
	out := all[:0]
	for _, e := range all {
		if !causal(e.Label) {
			continue
		}
		if len(want) > 0 && !want[e.Label] {
			continue
		}
		out = append(out, e)
	}
	return out
}

// Chain is what a decision rests on, root cause first. It is decision.Cause's
// way in, and reads the same walk as Up.
func (c *Cause) Chain(ctx context.Context, id string) ([]decision.Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if n, ok := c.graph().Node(id); !ok || n.Kind != kindDecision {
		return nil, fmt.Errorf("%w: %s", ErrNoDecision, id)
	}
	var out []decision.Record
	for _, a := range c.Up(id, 0) {
		out = append(out, a.Decision.Record)
	}
	return out, nil
}

// Roots are the decisions a chain ends at: upstream of this one, with nothing
// causing them in turn. They are where an explanation stops.
func (c *Cause) Roots(id string, depth int) []Away {
	var out []Away
	for _, a := range c.Up(id, depth) {
		if len(c.edges(a.Decision.ID, false)) == 0 {
			out = append(out, a)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Hops < out[j].Hops })
	return out
}

// Loops are the causal cycles in the graph: a decision that, followed
// forward, leads back to itself. A cycle is a modelling error more often than
// a fact about the world, so finding them is how a graph is checked.
//
// Each cycle is returned once, starting and ending at the same decision.
// Depth bounds the search, and is worth setting low on a densely linked graph:
// the number of distinct paths through one grows faster than the graph does.
func (c *Cause) Loops(depth int) [][]string {
	g := c.graph()
	if depth < 2 {
		depth = 10
	}
	seen := map[string]bool{}
	var out [][]string
	var walk func(start, at string, path []string)
	walk = func(start, at string, path []string) {
		if len(path) > depth {
			return
		}
		for _, e := range c.edges(at, true) {
			if e.To == start {
				cycle := append(append([]string{}, path...), start)
				if k := canon(path); !seen[k] {
					seen[k] = true
					out = append(out, cycle)
				}
				continue
			}
			if has(path, e.To) {
				continue
			}
			walk(start, e.To, append(path, e.To))
		}
	}
	for _, n := range g.Nodes(kindDecision) {
		walk(n.ID, n.ID, []string{n.ID})
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i]) < len(out[j]) })
	return out
}

// canon names a cycle by reading it from its smallest member, so the same
// cycle found from three different starting points is one cycle, while two
// different cycles over the same three decisions stay two.
func canon(cycle []string) string {
	at := 0
	for i, id := range cycle {
		if id < cycle[at] {
			at = i
		}
	}
	return strings.Join(append(append([]string{}, cycle[at:]...), cycle[:at]...), ">")
}

func has(ss []string, s string) bool {
	return slices.Contains(ss, s)
}

// Trace is one causal path and what survives it.
type Trace struct {
	From    string
	To      string
	Path    []string
	Hops    int
	Between []string // the decisions the path passed through
	Decay   float64  // the product of the weights along it
	Weakest Link     // the edge that costs the most confidence
	Weight  float64  // that edge's weight
	Band    Band
	Gloss   string // what the numbers amount to, in words
	Found   bool
}

// Trace follows causality from one decision to another and says what the
// distance means. A hop count alone is not evidence: three strong links and
// three weak ones are both "three hops", and only the decay tells them apart.
func (c *Cause) Trace(from, to string) Trace {
	g := c.graph()
	t := Trace{From: from, To: to, Gloss: "no causal path"}
	if _, ok := g.Node(from); !ok {
		return t
	}
	type walk struct {
		id      string
		path    []string
		decay   float64
		weakest Link
		weight  float64
	}
	queue := []walk{{id: from, path: []string{from}, decay: 1, weight: 1}}
	seen := map[string]bool{from: true}
	for len(queue) > 0 {
		w := queue[0]
		queue = queue[1:]
		if w.id == to {
			t.Found = true
			t.Path = w.path
			t.Hops = len(w.path) - 1
			t.Decay = w.decay
			t.Weakest = w.weakest
			t.Weight = w.weight
			t.Band = Apart(t.Hops)
			for i := 1; i < len(w.path)-1; i++ {
				if n, ok := g.Node(w.path[i]); ok && n.Kind == kindDecision {
					t.Between = append(t.Between, w.path[i])
				}
			}
			t.Gloss = gloss(t.Hops, t.Decay, t.Band)
			return t
		}
		for _, e := range c.edges(w.id, true) {
			if seen[e.To] {
				continue
			}
			seen[e.To] = true
			next := walk{
				id:      e.To,
				path:    append(append([]string{}, w.path...), e.To),
				decay:   w.decay * e.Weight,
				weakest: w.weakest,
				weight:  w.weight,
			}
			if next.weakest == (Link{}) || e.Weight < next.weight {
				next.weakest, next.weight = e.Link, e.Weight
			}
			queue = append(queue, next)
		}
	}
	return t
}

// gloss says in words what a hop count and a surviving confidence amount to,
// so a reader who should not act on 0.31 is told so rather than left to judge
// a number out of context.
func gloss(hops int, decay float64, band Band) string {
	switch band {
	case BandDirect:
		return fmt.Sprintf("direct cause, confidence %.2f", decay)
	case BandNear:
		weight := "weak evidence"
		if decay > 0.4 {
			weight = "moderate evidence"
		}
		return fmt.Sprintf("mediated by %s, confidence %.2f, %s", count(hops-1, "decision", "decisions"), decay, weight)
	}
	return fmt.Sprintf("distal, %s, confidence %.2f, weak evidence", count(hops, "causal step", "causal steps"), decay)
}

// count says how many of a thing there are, in words that read.
func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// Force is how much a decision moved: the decisions it caused or influenced,
// weighed by how sure those were, plus the decisions that followed it as a
// precedent, weighed at half — being cited is weaker evidence of consequence
// than causing something outright. The result runs from 0 to 1.
//
// A decision with no consequence scores 0 rather than dividing by nothing:
// the denominator counts at least one of each, so silence reads as no force
// rather than as an error.
func (c *Cause) Force(id string) float64 {
	made, sure := 0, 0.0
	for _, a := range c.walk(id, 5, true, Caused, Influenced) {
		made++
		sure += a.Decision.Confidence
	}
	cited, csure := 0, 0.0
	for _, a := range c.walk(id, 5, false, Precedes) {
		cited++
		csure += a.Decision.Confidence
	}
	weight := float64(most(made, 1)) + float64(most(cited, 1))*0.5
	if s := (sure + csure*0.5) / weight; s < 1 {
		return s
	}
	return 1
}

func most(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Strength is how strongly one decision bears on another: what the link
// claims, how sure the decision was, and how far apart they are. A claim does
// not survive distance unchanged, so the number falls as the chain lengthens.
func Strength(label string, confidence float64, hops int) float64 {
	weight := 0.6
	switch label {
	case Caused:
		weight = 1
	case Influenced:
		weight = 0.8
	}
	if hops < 0 {
		hops = 0
	}
	return confidence * weight / (1 + float64(hops)*0.1)
}

// Fit is how well a past decision serves as a precedent: how like the case it
// is, raised when it was the same kind of question and reached the same
// choice, lowered when it was neither.
func Fit(score float64, sameTopic, sameChoice bool) float64 {
	if sameTopic {
		score *= 1.1
	} else {
		score *= 0.7
	}
	if sameChoice {
		score *= 1.1
	} else {
		score *= 0.8
	}
	if score > 1 {
		return 1
	}
	return score
}

// Net is the shape of a causal network: how tied together it is, which
// decisions sit at the middle of it, and which parts of it never touch.
type Net struct {
	Decisions []string
	Edges     int
	Density   float64            // edges over every ordered pair that could have one
	Path      float64            // the rough mean distance, 1/density, never above the node count
	Rank      map[string]float64 // degree over the largest degree, from 0 to 1
	Parts     [][]string         // the groups that reach each other and nothing else
}

// Net measures the causal network over the decisions named, or over every
// decision when none are.
func (c *Cause) Net(ids []string) Net {
	g := c.graph()
	if len(ids) == 0 {
		for _, n := range g.Nodes(kindDecision) {
			ids = append(ids, n.ID)
		}
	}
	in := set(ids)
	n := Net{Decisions: ids, Rank: map[string]float64{}}
	degree := map[string]int{}
	parent := map[string]string{}
	for _, id := range ids {
		degree[id] = 0
		parent[id] = id
	}
	var find func(string) string
	find = func(x string) string {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	for _, id := range ids {
		for _, e := range c.edges(id, true) {
			if !in[e.To] {
				continue
			}
			n.Edges++
			degree[e.From]++
			degree[e.To]++
			if a, b := find(e.From), find(e.To); a != b {
				parent[a] = b
			}
		}
	}
	if len(ids) > 1 {
		n.Density = float64(n.Edges) / float64(len(ids)*(len(ids)-1))
	}
	if n.Density > 0 {
		n.Path = 1 / n.Density
		if n.Path > float64(len(ids)) {
			n.Path = float64(len(ids))
		}
	}
	most := 0
	for _, d := range degree {
		if d > most {
			most = d
		}
	}
	if most == 0 {
		most = 1
	}
	for id, d := range degree {
		n.Rank[id] = float64(d) / float64(most)
	}
	parts := map[string][]string{}
	for _, id := range ids {
		root := find(id)
		parts[root] = append(parts[root], id)
	}
	roots := make([]string, 0, len(parts))
	for r := range parts {
		roots = append(roots, r)
	}
	sort.Strings(roots)
	for _, r := range roots {
		n.Parts = append(n.Parts, parts[r])
	}
	return n
}
