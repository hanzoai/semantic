package kg

import (
	"context"
	"fmt"
	"sync"

	"github.com/hanzoai/semantic"
	"github.com/hanzoai/semantic/store"
)

// Mem keeps a graph in memory and serves it through store.Graph and
// store.Triple, so a caller with no database still has somewhere to put a
// graph. Writes go through the same merge as Add: the same assertion twice is
// one edge. It is safe for concurrent use, and its zero value is an empty
// store ready to write to.
type Mem struct {
	mu sync.RWMutex
	g  *Graph
}

var (
	_ store.Graph  = (*Mem)(nil)
	_ store.Triple = (*Mem)(nil)
)

// Keep serves an already built graph. Mem takes it over: later writes through
// Mem change it, so a caller that wants the original untouched passes a Sub of
// everything.
func Keep(g *Graph) *Mem { return &Mem{g: g} }

// Graph is the graph Mem holds, for the measures in this package. It is the
// live graph, not a copy, and reading it while another goroutine writes is
// the caller's business to order.
func (m *Mem) Graph() *Graph {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.graph()
}

func (m *Mem) graph() *Graph {
	if m.g == nil {
		m.g = Build(nil)
	}
	return m.g
}

// Node records a node and its properties.
func (m *Mem) Node(ctx context.Context, id string, props map[string]any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.graph().Set(id, props) {
		return fmt.Errorf("kg: node %q has no name", id)
	}
	return nil
}

// Edge records a relation between two nodes, creating either that the graph
// has not heard of.
func (m *Mem) Edge(ctx context.Context, from, to, label string) error {
	return m.Assert(ctx, from, label, to)
}

// Out lists the nodes an edge points at from this one.
func (m *Mem) Out(ctx context.Context, id string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.g == nil {
		return nil, nil
	}
	return m.g.Out(id), nil
}

// Assert records one triple.
func (m *Mem) Assert(ctx context.Context, s, p, o string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.graph().Add(semantic.Triple{Subject: s, Predicate: p, Object: o}) {
		return fmt.Errorf("kg: incomplete triple (%q, %q, %q)", s, p, o)
	}
	return nil
}

// Match returns the asserted triples matching a pattern, first asserted
// first. An empty subject, predicate or object stands for any, so Match with
// three empty strings returns everything.
func (m *Mem) Match(ctx context.Context, s, p, o string) ([][3]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.g == nil {
		return nil, nil
	}
	s, p, o = Fold(s), Fold(p), Fold(o)
	var out [][3]string
	for _, k := range m.g.eord {
		if (s == "" || k.from == s) && (p == "" || k.label == p) && (o == "" || k.to == o) {
			out = append(out, [3]string{k.from, m.g.edge[k].Label, k.to})
		}
	}
	return out, nil
}
