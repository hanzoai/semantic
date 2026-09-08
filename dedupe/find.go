package dedupe

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Pair is two records judged to be the same thing, with the evidence for the
// judgement. Like is how alike they look; Score is how sure the judgement is
// after the evidence in Why is counted.
type Pair struct {
	A, B  Entity
	Like  float64
	Score float64
	Why   []string
}

// Opt tunes a search. The zero value compares everything and keeps
// everything: it is Find with no opinion. Default carries thresholds that
// hold up on ordinary text, and is the value to start from.
//
// Like and Score are floors applied while pairs are made. Floor, Per and Max
// shape what survives afterwards: Floor drops the least alike, Per caps how
// many pairs any one record may appear in, Max caps the whole result. Zero
// means "no cap" for Per and Max.
type Opt struct {
	Like    float64 // minimum likeness for a pair to be made
	Score   float64 // minimum confidence for a pair to be kept
	Floor   float64 // drop pairs less alike than this
	Per     int     // most pairs one record may appear in
	Max     int     // most pairs in the result
	Alike   bool    // rank by likeness rather than by confidence
	Weights Weights
	Metric  Metric
}

// Default asks a pair to look 0.7 alike and to end 0.6 sure. It is strict
// enough that what it returns is worth merging without review.
var Default = Opt{Like: 0.7, Score: 0.6}

// check reports options outside their range. Every threshold is a
// probability, and no cap can be negative.
func (o Opt) check() error {
	for _, f := range [...]struct {
		name string
		v    float64
	}{{"like", o.Like}, {"score", o.Score}, {"floor", o.Floor}} {
		if f.v < 0 || f.v > 1 {
			return fmt.Errorf("%w: %s is %v, want 0 to 1", ErrOption, f.name, f.v)
		}
	}
	for _, f := range [...]struct {
		name string
		v    int
	}{{"per", o.Per}, {"max", o.Max}} {
		if f.v < 0 {
			return fmt.Errorf("%w: %s is %d, want 0 or more", ErrOption, f.name, f.v)
		}
	}
	return nil
}

// Find returns the pairs of records in es that describe the same thing.
//
// Records are only compared when they share a blocking key, so the cost is
// far below every pair, and two records whose kinds are stated and differ are
// never a pair whatever they score — a person and a company that share a name
// are two things, not one.
func Find(ctx context.Context, es []Entity, o Opt) ([]Pair, error) {
	if err := o.check(); err != nil {
		return nil, err
	}
	var out []Pair
	for _, c := range candidates(es) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if p, ok := pair(es[c[0]], es[c[1]], o); ok {
			out = append(out, p)
		}
	}
	return limit(out, o), nil
}

// Add compares fresh records against ones already held, and against nothing
// else. It is what to call when a graph already exists and a document has
// just arrived: the cost is the product of the two sets, not the square of
// their sum. Each pair reads fresh first, known second.
func Add(ctx context.Context, fresh, known []Entity, o Opt) ([]Pair, error) {
	if err := o.check(); err != nil {
		return nil, err
	}
	index := map[string][]int{}
	for i, e := range known {
		for _, k := range block(e) {
			index[k] = append(index[k], i)
		}
	}
	var out []Pair
	for _, f := range fresh {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		seen := map[int]bool{}
		for _, k := range block(f) {
			for _, j := range index[k] {
				if seen[j] {
					continue
				}
				seen[j] = true
				if p, ok := pair(f, known[j], o); ok {
					out = append(out, p)
				}
			}
		}
	}
	return limit(out, o), nil
}

// candidates returns the index pairs worth comparing, in index order so that
// the result of a search does not depend on map iteration.
func candidates(es []Entity) [][2]int {
	index := map[string][]int{}
	for i, e := range es {
		for _, k := range block(e) {
			index[k] = append(index[k], i)
		}
	}
	seen := map[[2]int]bool{}
	var out [][2]int
	for _, idx := range index {
		for i := 0; i < len(idx); i++ {
			for j := i + 1; j < len(idx); j++ {
				c := [2]int{min(idx[i], idx[j]), max(idx[i], idx[j])}
				if seen[c] {
					continue
				}
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out
}

// pair judges one comparison. Likeness decides whether the records look the
// same; the evidence after it decides how sure that is. An exact name, each
// property whose value agrees, and an agreeing kind each add to the
// confidence, which never passes 1.
func pair(a, b Entity, o Opt) (Pair, bool) {
	if a.Kind != "" && b.Kind != "" && !strings.EqualFold(a.Kind, b.Kind) {
		return Pair{}, false
	}
	s := Likeness(a, b, o.Weights, o.Metric)
	if s.Total < o.Like {
		return Pair{}, false
	}
	score := s.Total
	var why []string
	if n := fold(a.Name); n != "" && n == fold(b.Name) {
		why = append(why, "name")
		score += 0.1
	}
	if n := agree(a.Props, b.Props); n > 0 {
		why = append(why, fmt.Sprintf("%d properties", n))
		score += 0.05 * float64(n)
	}
	if a.Kind != "" && b.Kind != "" {
		why = append(why, "kind")
		score += 0.05
	}
	if score > 1 {
		score = 1
	}
	if score < o.Score {
		return Pair{}, false
	}
	return Pair{A: a, B: b, Like: s.Total, Score: score, Why: why}, true
}

// agree counts the properties both records state and state alike.
func agree(a, b map[string]any) int {
	n := 0
	for k, v := range a {
		if w, ok := b[k]; ok && same(v, w) {
			n++
		}
	}
	return n
}

// limit ranks and trims a result: the least alike go first, then the ranking,
// then the per-record cap, then the overall cap. The order matters — a cap
// applied before the ranking would keep the wrong pairs.
func limit(ps []Pair, o Opt) []Pair {
	if o.Floor > 0 {
		kept := ps[:0]
		for _, p := range ps {
			if p.Like >= o.Floor {
				kept = append(kept, p)
			}
		}
		ps = kept
	}
	sort.SliceStable(ps, func(i, j int) bool {
		if o.Alike {
			return ps[i].Like > ps[j].Like
		}
		return ps[i].Score > ps[j].Score
	})
	if o.Per > 0 {
		count := map[string]int{}
		kept := ps[:0]
		for _, p := range ps {
			x, y := p.A.key(), p.B.key()
			if count[x] < o.Per || count[y] < o.Per {
				kept = append(kept, p)
				count[x]++
				count[y]++
			}
		}
		ps = kept
	}
	if o.Max > 0 && len(ps) > o.Max {
		ps = ps[:o.Max]
	}
	return ps
}

// Group is a set of records found to describe one thing, and how sure that
// finding is. Head is the record carrying the most, which is the one to show
// when only one can be shown.
type Group struct {
	Of    []Entity
	Like  map[[2]string]float64 // likeness of each pair that built the group
	Head  Entity
	Score float64
}

// Groups joins pairs that share a record into one group each. A pairs with B
// and B with C makes one group of three, because sameness is transitive even
// when the evidence for it is not.
func Groups(ps []Pair) []Group {
	var out []*Group
	at := map[string]*Group{}
	for _, p := range ps {
		x, y := p.A.key(), p.B.key()
		gx, gy := at[x], at[y]
		k := [2]string{x, y}
		switch {
		case gx == nil && gy == nil:
			g := &Group{Of: []Entity{p.A, p.B}, Like: map[[2]string]float64{k: p.Like}}
			out = append(out, g)
			at[x], at[y] = g, g
		case gx != nil && gy == nil:
			gx.Of = append(gx.Of, p.B)
			gx.Like[k] = p.Like
			at[y] = gx
		case gx == nil && gy != nil:
			gy.Of = append(gy.Of, p.A)
			gy.Like[k] = p.Like
			at[x] = gy
		case gx != gy:
			gx.Of = append(gx.Of, gy.Of...)
			for kk, v := range gy.Like {
				gx.Like[kk] = v
			}
			gx.Like[k] = p.Like
			for _, e := range gy.Of {
				at[e.key()] = gx
			}
			out = drop(out, gy)
		default:
			gx.Like[k] = p.Like
		}
	}
	gs := make([]Group, 0, len(out))
	for _, g := range out {
		g.Head = head(g.Of)
		g.Score = confidence(*g)
		gs = append(gs, *g)
	}
	return gs
}

// Loose returns the records no group claimed, in the order they were given.
// They are not leftovers: a record that matched nothing is a record about
// something only one document mentions.
func Loose(es []Entity, gs []Group) []Entity {
	in := map[string]bool{}
	for _, g := range gs {
		for _, e := range g.Of {
			in[e.key()] = true
		}
	}
	var out []Entity
	for _, e := range es {
		if !in[e.key()] {
			out = append(out, e)
		}
	}
	return out
}

// Tight is the mean likeness of the pairs that built the group: how closely
// its records actually resemble one another, as against how sure Groups is
// that they are one thing.
func (g Group) Tight() float64 {
	if len(g.Like) == 0 {
		return 0
	}
	var sum float64
	for _, v := range g.Like {
		sum += v
	}
	return sum / float64(len(g.Like))
}

// confidence is the mean likeness of a group's pairs, raised a little as the
// group grows: five records agreeing is better evidence than two.
func confidence(g Group) float64 {
	t := g.Tight()
	if t == 0 {
		return 0
	}
	size := float64(len(g.Of)) / 5
	if size > 1 {
		size = 1
	}
	return t * (0.8 + 0.2*size)
}

// head is the record carrying the most properties and edges. Ties go to the
// one given first, so the choice does not move between runs.
func head(es []Entity) Entity {
	best := es[0]
	for _, e := range es[1:] {
		if e.size() > best.size() {
			best = e
		}
	}
	return best
}

func drop(gs []*Group, g *Group) []*Group {
	for i, x := range gs {
		if x == g {
			return append(gs[:i], gs[i+1:]...)
		}
	}
	return gs
}
