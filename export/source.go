package export

import (
	"context"
	"fmt"

	"github.com/hanzoai/semantic"
	"github.com/hanzoai/semantic/store"
)

// Source walks a graph, handing every assertion to yield in order. It returns
// yield's error unchanged, so a format that cannot write stops the walk
// instead of being handed the rest of a graph it has no use for.
//
// A source may be walked more than once: [Triples], [Match] and [Walk] all
// replay, which is what [GEXF] needs, since its grammar puts every node before
// every edge. A source over an io.Reader is the exception and says so.
type Source func(ctx context.Context, yield func(semantic.Triple) error) error

// Triples is the source of a slice already in memory.
func Triples(ts []semantic.Triple) Source {
	return func(ctx context.Context, yield func(semantic.Triple) error) error {
		for _, t := range ts {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := yield(t); err != nil {
				return err
			}
		}
		return nil
	}
}

// Match is the source of what a triple store holds for a pattern. An empty
// field matches anything, so Match(st, "", "", "") is the whole store.
//
// A store answers with the assertion and not with what was known about it, so
// the triples this yields carry no confidence and no source span.
func Match(st store.Triple, s, p, o string) Source {
	return func(ctx context.Context, yield func(semantic.Triple) error) error {
		got, err := st.Match(ctx, s, p, o)
		if err != nil {
			return fmt.Errorf("match %q %q %q: %w", s, p, o, err)
		}
		for _, t := range got {
			if err := yield(semantic.Triple{Subject: t[0], Predicate: t[1], Object: t[2]}); err != nil {
				return err
			}
		}
		return nil
	}
}

// Walk is the source of the edges reachable from roots in a graph store,
// breadth first from each root in turn, every edge yielded once.
//
// A graph store reports a node's neighbours without saying which relation
// leads to them, so the walk cannot recover the predicate and records label in
// its place. Where the predicates matter, export from the triple store.
func Walk(g store.Graph, label string, roots ...string) Source {
	return func(ctx context.Context, yield func(semantic.Triple) error) error {
		queued := map[string]bool{}
		var queue []string
		for _, r := range roots {
			if r != "" && !queued[r] {
				queued[r] = true
				queue = append(queue, r)
			}
		}
		for len(queue) > 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
			from := queue[0]
			queue = queue[1:]
			out, err := g.Out(ctx, from)
			if err != nil {
				return fmt.Errorf("out %q: %w", from, err)
			}
			for _, to := range out {
				if err := yield(semantic.Triple{Subject: from, Predicate: label, Object: to}); err != nil {
					return err
				}
				if !queued[to] {
					queued[to] = true
					queue = append(queue, to)
				}
			}
		}
		return nil
	}
}

// fail is the source that yields nothing and reports err, so a read that
// cannot start reports it where the walk is rather than where the call was.
func fail(err error) Source {
	return func(context.Context, func(semantic.Triple) error) error { return err }
}

// once guards a source over a reader. The bytes are gone after the first
// walk, so a second says so instead of answering with an empty graph.
func once(s Source) Source {
	spent := false
	return func(ctx context.Context, yield func(semantic.Triple) error) error {
		if spent {
			return ErrDrained
		}
		spent = true
		return s(ctx, yield)
	}
}

// whole reports whether an assertion names all three of its terms. The
// formats that identify nodes call it, because a node with no name is a
// document a reader cannot resolve.
func whole(t semantic.Triple, n int) error {
	if t.Subject == "" || t.Predicate == "" || t.Object == "" {
		return fmt.Errorf("assertion %d (%q %q %q): %w", n, t.Subject, t.Predicate, t.Object, ErrTerm)
	}
	return nil
}
