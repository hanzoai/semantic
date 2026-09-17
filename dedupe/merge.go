package dedupe

import (
	"context"
	"maps"
	"slices"

	"github.com/hanzoai/semantic"
)

// Keep says which of several records about one thing survives a merge, and
// which value wins where two of them disagree.
type Keep string

const (
	// First keeps the record given first.
	First Keep = "first"
	// Last keeps the record given last.
	Last Keep = "last"
	// Fullest keeps the record carrying the most properties and edges.
	Fullest Keep = "fullest"
	// Surest keeps the record with the highest confidence.
	Surest Keep = "surest"
	// All keeps every value: a property two records disagree on becomes the
	// list of what each said, and nothing is chosen between them.
	All Keep = "all"
)

// Merged is one merge, and the records it was made from. It is the merged
// record's provenance: Kept names every record folded in, and Clash names
// every property they disagreed on and what the merge did about it. A graph
// that has forgotten this cannot say what it was built from.
type Merged struct {
	Entity Entity
	From   []Entity
	Kept   []string
	Keep   Keep
	Clash  []Clash
}

// Clash is one property two records gave different values, and the value the
// merge took.
type Clash struct {
	Prop   string
	Values []any
	Took   any
	How    Keep
}

// Merge folds records about one thing into a single record. The survivor is
// chosen by k; every property the others state and the survivor does not is
// added; every property they state differently is a Clash, settled by k and
// recorded either way.
//
// Merge copies what it reads. The records given are left as they were.
func Merge(es []Entity, k Keep) (Merged, error) {
	if len(es) == 0 {
		return Merged{}, ErrEmpty
	}
	base := pick(es, k)
	out := clone(es[base])
	m := Merged{From: es, Keep: k, Kept: ids(es)}
	for i, e := range es {
		if i == base {
			continue
		}
		if out.Name == "" {
			out.Name = e.Name
		}
		if out.Kind == "" {
			out.Kind = e.Kind
		}
		for _, p := range slices.Sorted(maps.Keys(e.Props)) {
			v := e.Props[p]
			have, ok := out.Props[p]
			switch {
			case !ok:
				out.Props[p] = v
			case same(have, v):
			default:
				took := settle(have, v, k)
				m.Clash = append(m.Clash, Clash{Prop: p, Values: []any{have, v}, Took: took, How: k})
				out.Props[p] = took
			}
		}
		out.Edges = join(out.Edges, e.Edges)
		for p, v := range e.Meta {
			if _, ok := out.Meta[p]; !ok {
				out.Meta[p] = v
			}
		}
	}
	out.Meta["merged"] = m.Kept
	out.Meta["keep"] = string(k)
	m.Entity = out
	return m, nil
}

// Fuse finds the duplicates in es and merges each group it finds, returning
// one Merged per group. Records that matched nothing are not in the result;
// Loose reports them.
func Fuse(ctx context.Context, es []Entity, o Opt, k Keep) ([]Merged, error) {
	ps, err := Find(ctx, es, o)
	if err != nil {
		return nil, err
	}
	var out []Merged
	for _, g := range Groups(ps) {
		m, err := Merge(g.Of, k)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

// pick returns the index of the record that survives.
func pick(es []Entity, k Keep) int {
	switch k {
	case Last:
		return len(es) - 1
	case Fullest:
		best := 0
		for i, e := range es {
			if e.size() > es[best].size() {
				best = i
			}
		}
		return best
	case Surest:
		best := 0
		for i, e := range es {
			if e.Score > es[best].Score {
				best = i
			}
		}
		return best
	default:
		return 0
	}
}

// settle chooses between two values for one property. Every Keep but Last and
// All holds what the survivor already said, since that is what choosing a
// survivor meant.
func settle(have, v any, k Keep) any {
	switch k {
	case Last:
		return v
	case All:
		if list, ok := have.([]any); ok {
			for _, x := range list {
				if same(x, v) {
					return list
				}
			}
			return append(slices.Clone(list), v)
		}
		return []any{have, v}
	default:
		return have
	}
}

// clone copies a record deeply enough that merging into it cannot reach back
// into what the caller gave.
func clone(e Entity) Entity {
	out := e
	out.Props = make(map[string]any, len(e.Props))
	maps.Copy(out.Props, e.Props)
	out.Meta = make(map[string]any, len(e.Meta)+2)
	maps.Copy(out.Meta, e.Meta)
	out.Edges = slices.Clone(e.Edges)
	out.Vector = slices.Clone(e.Vector)
	return out
}

// join appends the edges of b that a does not already assert.
func join(a, b []semantic.Triple) []semantic.Triple {
	var c Canon
	have := make(map[[3]string]bool, len(a))
	for _, t := range a {
		have[c.Key(t)] = true
	}
	for _, t := range b {
		k := c.Key(t)
		if have[k] {
			continue
		}
		have[k] = true
		a = append(a, t)
	}
	return a
}

// ids names the records a merge folded together, in the order given.
func ids(es []Entity) []string {
	out := make([]string, 0, len(es))
	for _, e := range es {
		out = append(out, e.key())
	}
	return out
}
