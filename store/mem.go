package store

import (
	"context"
	"errors"
	"sort"
	"sync"
)

// What a store refuses to do. Test for these with errors.Is.
var (
	// ErrID means an identifier was blank.
	ErrID = errors.New("store: empty id")
	// ErrTerm means a triple was missing a subject, predicate or object.
	ErrTerm = errors.New("store: empty term")
	// ErrDim means a vector's length differs from the store's.
	ErrDim = errors.New("store: vector dimension")
	// ErrDriver means a driver is named but this build cannot serve it.
	ErrDriver = errors.New("store: driver unavailable")
)

// Space is the metadata key a namespace is written under. A namespace is a
// filter, not a second kind of store: tag a vector with it on the way in and
// ask for it on the way out.
//
//	m.Put(ctx, "a", v, map[string]any{store.Space: "docs"})
//	m.Search(ctx, store.Query{Vec: q, K: 5, Filter: store.Filter{}.Eq(store.Space, "docs")})
const Space = "space"

// Item is an identifier and what is known about it: a vector and its
// metadata, or — with no vector — a graph node and its properties.
type Item struct {
	ID   string
	Vec  []float32
	Meta map[string]any
}

// Snap is everything a store holds. A durable store writes one to make a
// snapshot and reads one back to restore.
type Snap struct {
	Metric Metric
	Items  []Item
	Nodes  []Item
	Links  []Link
	Facts  [][3]string
}

// Link is one edge: which node points at which, under what label.
type Link struct {
	From  string
	To    string
	Label string
}

// Query asks for the nearest vectors that also match a filter.
//
// A nil Vec ranks nothing and returns whatever the filter admits, in the
// order it was written, which is how a metadata-only lookup is asked for. A
// K of zero or less asks for every match rather than none, so a filter
// stands on its own.
type Query struct {
	Vec    []float32
	K      int
	Filter Filter
	// Metric overrides the store's own. Empty uses the store's.
	Metric Metric
}

// Stat counts what a store holds.
type Stat struct {
	Vectors int
	Nodes   int
	Edges   int
	Facts   int
}

// Mem keeps a store in memory and implements all three of Vector, Graph and
// Triple, so a caller that wants one of them and a caller that wants all
// three use the same object. It is the default every other driver is
// measured against, and the store a program uses when it has no database.
//
// The zero value is an empty store, ready to use, ranking by cosine. Every
// method is safe for concurrent use.
type Mem struct {
	mu     sync.RWMutex
	metric Metric
	dim    int
	vec    map[string]Item
	vord   []string

	prop map[string]map[string]any
	nord []string

	link map[Link]bool
	lord []Link
	out  map[string][]string
	in   map[string][]string

	fact map[[3]string]bool
	ford [][3]string
}

// Mem answers all three questions.
var (
	_ Vector = (*Mem)(nil)
	_ Graph  = (*Mem)(nil)
	_ Triple = (*Mem)(nil)
)

// Put writes a vector under an id, replacing whatever was there. The first
// vector fixes the store's dimension; a later one of a different length is
// refused with ErrDim, because a store whose vectors do not share a space
// cannot rank them. A vector of no components names no point and is
// refused for the same reason.
func (m *Mem) Put(ctx context.Context, id string, v []float32, meta map[string]any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if id == "" {
		return ErrID
	}
	if len(v) == 0 {
		return ErrDim
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dim == 0 {
		m.dim = len(v)
	}
	if len(v) != m.dim {
		return ErrDim
	}
	if m.vec == nil {
		m.vec = map[string]Item{}
	}
	if _, ok := m.vec[id]; !ok {
		m.vord = append(m.vord, id)
	}
	m.vec[id] = Item{ID: id, Vec: floats(v), Meta: dup(meta)}
	return nil
}

// Rank sets how nearness is measured. The zero value of a store ranks by
// cosine.
func (m *Mem) Rank(v Metric) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.metric = v
}

// Metric reports how nearness is measured.
func (m *Mem) Metric() Metric {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.metric
}

// Near returns the k nearest vectors to v, nearest first. A k of zero or
// less returns every vector the store holds, ranked.
func (m *Mem) Near(ctx context.Context, v []float32, k int) ([]Match, error) {
	return m.Search(ctx, Query{Vec: v, K: k})
}

// Search returns the matches a query admits, nearest first when it asks for
// nearness and in insertion order when it does not.
func (m *Mem) Search(ctx context.Context, q Query) ([]Match, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if q.Vec != nil && m.dim != 0 && len(q.Vec) != m.dim {
		return nil, ErrDim
	}
	metric := q.Metric
	if metric == "" {
		metric = m.metric
	}
	out := make([]Match, 0, len(m.vord))
	for _, id := range m.vord {
		it := m.vec[id]
		if !q.Filter.Match(it.Meta) {
			continue
		}
		s := 0.0
		if q.Vec != nil {
			s = metric.Score(q.Vec, it.Vec)
		}
		out = append(out, Match{ID: id, Score: s, Meta: dup(it.Meta)})
	}
	if q.Vec == nil {
		if q.K > 0 && q.K < len(out) {
			out = out[:q.K]
		}
		return out, nil
	}
	return top(out, q.K), nil
}

// Get returns a vector and its metadata, and whether the store holds the id.
func (m *Mem) Get(id string) ([]float32, map[string]any, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	it, ok := m.vec[id]
	if !ok {
		return nil, nil, false
	}
	return floats(it.Vec), dup(it.Meta), true
}

// Scan pages through the stored vectors in the order they were written,
// which is how a caller walks a whole store without ranking it. Scores are
// zero: nothing was asked for, so nothing is near.
func (m *Mem) Scan(offset, limit int) []Match {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if offset < 0 {
		offset = 0
	}
	if offset >= len(m.vord) {
		return nil
	}
	ids := m.vord[offset:]
	if limit > 0 && limit < len(ids) {
		ids = ids[:limit]
	}
	out := make([]Match, 0, len(ids))
	for _, id := range ids {
		out = append(out, Match{ID: id, Meta: dup(m.vec[id].Meta)})
	}
	return out
}

// Drop removes vectors. Ids the store does not hold are ignored.
func (m *Mem) Drop(ctx context.Context, ids ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range ids {
		if _, ok := m.vec[id]; !ok {
			continue
		}
		delete(m.vec, id)
		m.vord = without(m.vord, id)
	}
	if len(m.vec) == 0 {
		m.dim = 0
	}
	return nil
}

// Node writes a node and its properties, creating it if the store has not
// heard of it. Properties given replace those of the same name; the rest are
// left alone, so a caller may set one field without reading the others.
func (m *Mem) Node(ctx context.Context, id string, props map[string]any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if id == "" {
		return ErrID
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.node(id)
	for k, v := range props {
		m.prop[id][k] = v
	}
	return nil
}

// node creates a node if it is new and returns nothing; callers hold the
// lock. It is what makes an edge's endpoints exist.
func (m *Mem) node(id string) {
	if m.prop == nil {
		m.prop = map[string]map[string]any{}
	}
	if _, ok := m.prop[id]; !ok {
		m.prop[id] = map[string]any{}
		m.nord = append(m.nord, id)
	}
}

// Props returns a node's properties and whether the store holds it.
func (m *Mem) Props(id string) (map[string]any, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.prop[id]
	if !ok {
		return nil, false
	}
	return dup(p), true
}

// Nodes lists every node in the order it was first written.
func (m *Mem) Nodes() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]string(nil), m.nord...)
}

// Edge links two nodes under a label, creating either endpoint if it is new.
// The same link asserted twice is one edge.
func (m *Mem) Edge(ctx context.Context, from, to, label string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if from == "" || to == "" {
		return ErrID
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.node(from)
	m.node(to)
	l := Link{From: from, To: to, Label: label}
	if m.link == nil {
		m.link = map[Link]bool{}
	}
	if m.link[l] {
		return nil
	}
	m.link[l] = true
	m.lord = append(m.lord, l)
	if m.out == nil {
		m.out, m.in = map[string][]string{}, map[string][]string{}
	}
	m.out[from] = with(m.out[from], to)
	m.in[to] = with(m.in[to], from)
	return nil
}

// Out lists the nodes this one points at, first linked first.
func (m *Mem) Out(ctx context.Context, id string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]string(nil), m.out[id]...), nil
}

// In lists the nodes that point at this one, first linked first.
func (m *Mem) In(ctx context.Context, id string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]string(nil), m.in[id]...), nil
}

// Edges lists the links that match a pattern, first written first. An empty
// from, to or label matches any.
func (m *Mem) Edges(from, to, label string) []Link {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []Link
	for _, l := range m.lord {
		if (from == "" || l.From == from) &&
			(to == "" || l.To == to) &&
			(label == "" || l.Label == label) {
			out = append(out, l)
		}
	}
	return out
}

// Cut removes a node and every edge that touched it, because an edge to a
// node that is gone is not a fact about anything.
func (m *Mem) Cut(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.prop[id]; !ok {
		return nil
	}
	delete(m.prop, id)
	m.nord = without(m.nord, id)
	m.unlink(func(l Link) bool { return l.From == id || l.To == id })
	return nil
}

// Unlink removes the edges that match a pattern. An empty from, to or label
// matches any, so Unlink(ctx, "a", "", "") cuts everything leaving a.
func (m *Mem) Unlink(ctx context.Context, from, to, label string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.unlink(func(l Link) bool {
		return (from == "" || l.From == from) &&
			(to == "" || l.To == to) &&
			(label == "" || l.Label == label)
	})
	return nil
}

// unlink drops the edges a predicate picks and rebuilds the adjacency from
// what is left; callers hold the lock.
func (m *Mem) unlink(gone func(Link) bool) {
	kept := m.lord[:0]
	for _, l := range m.lord {
		if gone(l) {
			delete(m.link, l)
			continue
		}
		kept = append(kept, l)
	}
	m.lord = kept
	m.out, m.in = map[string][]string{}, map[string][]string{}
	for _, l := range m.lord {
		m.out[l.From] = with(m.out[l.From], l.To)
		m.in[l.To] = with(m.in[l.To], l.From)
	}
}

// Path returns a shortest path from one node to another, both ends
// included, following edges in their own direction. It is nil when there is
// no path; a node to itself is a path of one.
func (m *Mem) Path(ctx context.Context, from, to string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.prop[from]; !ok {
		return nil, nil
	}
	if _, ok := m.prop[to]; !ok {
		return nil, nil
	}
	if from == to {
		return []string{from}, nil
	}
	back := map[string]string{from: ""}
	for queue := []string{from}; len(queue) > 0; {
		at := queue[0]
		queue = queue[1:]
		for _, next := range m.out[at] {
			if _, seen := back[next]; seen {
				continue
			}
			back[next] = at
			if next == to {
				return trace(back, from, to), nil
			}
			queue = append(queue, next)
		}
	}
	return nil, nil
}

// trace walks a breadth-first search's parents back to the start.
func trace(back map[string]string, from, to string) []string {
	var path []string
	for at := to; at != ""; at = back[at] {
		path = append(path, at)
		if at == from {
			break
		}
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

// Reach lists the nodes within hops steps of one, in either direction and
// excluding the node itself, nearest first. Zero hops reaches nothing.
func (m *Mem) Reach(ctx context.Context, id string, hops int) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.prop[id]; !ok {
		return nil, nil
	}
	seen := map[string]bool{id: true}
	var out []string
	front := []string{id}
	for i := 0; i < hops && len(front) > 0; i++ {
		var next []string
		for _, at := range front {
			for _, n := range append(append([]string{}, m.out[at]...), m.in[at]...) {
				if seen[n] {
					continue
				}
				seen[n] = true
				out = append(out, n)
				next = append(next, n)
			}
		}
		front = next
	}
	return out, nil
}

// Degree counts the edges into and out of a node, the simplest measure of
// how central it is.
func (m *Mem) Degree(id string) (in, out int) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.in[id]), len(m.out[id])
}

// Components groups the nodes into connected components, ignoring edge
// direction. Each group is sorted, and the groups are ordered by their first
// node, so the answer is the same on every run.
func (m *Mem) Components() [][]string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	seen := map[string]bool{}
	var out [][]string
	for _, id := range m.nord {
		if seen[id] {
			continue
		}
		var group []string
		for queue := []string{id}; len(queue) > 0; {
			at := queue[0]
			queue = queue[1:]
			if seen[at] {
				continue
			}
			seen[at] = true
			group = append(group, at)
			queue = append(queue, m.out[at]...)
			queue = append(queue, m.in[at]...)
		}
		sort.Strings(group)
		out = append(out, group)
	}
	return out
}

// Assert records one triple. The same triple asserted twice is one fact.
func (m *Mem) Assert(ctx context.Context, s, p, o string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == "" || p == "" || o == "" {
		return ErrTerm
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fact == nil {
		m.fact = map[[3]string]bool{}
	}
	f := [3]string{s, p, o}
	if m.fact[f] {
		return nil
	}
	m.fact[f] = true
	m.ford = append(m.ford, f)
	return nil
}

// Match returns the triples that fit a pattern, first asserted first. An
// empty subject, predicate or object matches any, so Match(ctx, "", "", "")
// returns everything.
func (m *Mem) Match(ctx context.Context, s, p, o string) ([][3]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out [][3]string
	for _, f := range m.ford {
		if fits(f, s, p, o) {
			out = append(out, f)
		}
	}
	return out, nil
}

// Retract removes the triples that fit a pattern, on the same wildcards as
// Match.
func (m *Mem) Retract(ctx context.Context, s, p, o string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.ford[:0]
	for _, f := range m.ford {
		if fits(f, s, p, o) {
			delete(m.fact, f)
			continue
		}
		kept = append(kept, f)
	}
	m.ford = kept
	return nil
}

// Stat counts what the store holds.
func (m *Mem) Stat() Stat {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return Stat{Vectors: len(m.vec), Nodes: len(m.nord), Edges: len(m.lord), Facts: len(m.ford)}
}

// Dump returns everything the store holds, in the order it was written.
func (m *Mem) Dump() Snap {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := Snap{Metric: m.metric}
	for _, id := range m.vord {
		it := m.vec[id]
		s.Items = append(s.Items, Item{ID: id, Vec: floats(it.Vec), Meta: dup(it.Meta)})
	}
	for _, id := range m.nord {
		s.Nodes = append(s.Nodes, Item{ID: id, Meta: dup(m.prop[id])})
	}
	s.Links = append(s.Links, m.lord...)
	s.Facts = append(s.Facts, m.ford...)
	return s
}

// Load replaces everything the store holds with a snapshot. It is the other
// half of Dump: what one writes, the other reads back.
func (m *Mem) Load(ctx context.Context, s Snap) error {
	fresh := &Mem{metric: s.Metric}
	for _, it := range s.Items {
		if err := fresh.Put(ctx, it.ID, it.Vec, it.Meta); err != nil {
			return err
		}
	}
	for _, n := range s.Nodes {
		if err := fresh.Node(ctx, n.ID, n.Meta); err != nil {
			return err
		}
	}
	for _, l := range s.Links {
		if err := fresh.Edge(ctx, l.From, l.To, l.Label); err != nil {
			return err
		}
	}
	for _, f := range s.Facts {
		if err := fresh.Assert(ctx, f[0], f[1], f[2]); err != nil {
			return err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.metric = fresh.metric
	m.dim, m.vec, m.vord = fresh.dim, fresh.vec, fresh.vord
	m.prop, m.nord = fresh.prop, fresh.nord
	m.link, m.lord, m.out, m.in = fresh.link, fresh.lord, fresh.out, fresh.in
	m.fact, m.ford = fresh.fact, fresh.ford
	return nil
}

func fits(f [3]string, s, p, o string) bool {
	return (s == "" || f[0] == s) && (p == "" || f[1] == p) && (o == "" || f[2] == o)
}

// with appends v unless it is already there, keeping adjacency a set with an
// order.
func with(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

func without(list []string, v string) []string {
	out := list[:0]
	for _, x := range list {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

// floats copies a vector so a caller cannot change what the store holds.
func floats(v []float32) []float32 {
	if v == nil {
		return nil
	}
	return append([]float32(nil), v...)
}

// dup copies metadata for the same reason, one level deep: a nested value
// is shared, which is the usual bargain for a map of any.
func dup(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
