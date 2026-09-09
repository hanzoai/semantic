package main

import (
	"context"

	"github.com/hanzoai/semantic/store"
)

// The two arms in bench.go differ in two ways at once. The graph indexes claim
// spans — what one person said in one turn — and answers only out of the spans
// belonging to the person a question names. The baseline indexes whole turns
// and ranks all of them. Which of those two changes carries the result cannot
// be read off two cells, so here are the other two: whole turns scoped by
// person, and claim spans left flat. Together the four are a 2x2 over what is
// indexed against what is reachable, and the four numbers say which factor the
// effect belongs to.
//
//	                 all turns reachable    only the subject's
//	whole turn       vector                 scoped
//	claim span       span                   graph

// Scoped indexes whole turns, filed once under each person spoken of in them,
// and answers only from the turns of the person a question names. It shares
// the graph's reading of who a turn is about and its subject resolution, and
// indexes the baseline's text.
type Scoped struct {
	know *Knowledge
	mem  *store.Mem
	turn map[string]int
}

// NewScoped derives the arm from a graph already built: the graph's pieces say
// which people a turn was read as being about, and that is the whole of what
// this arm needs from it.
func NewScoped(ctx context.Context, k *Knowledge) (*Scoped, error) {
	s := &Scoped{know: k, mem: &store.Mem{}, turn: map[string]int{}}
	s.mem.Rank(store.Inner)
	for id, piece := range k.piece {
		s.turn[id] = piece.Turn
		if err := s.mem.Put(ctx, id, k.words.Weigh(k.turns[piece.Turn].Line()),
			map[string]any{store.Space: piece.Who}); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Name is what this arm is called in the results.
func (s *Scoped) Name() string { return "scoped" }

// Recall ranks the turns of the person the question names.
func (s *Scoped) Recall(ctx context.Context, question string, n int) ([]Cite, error) {
	query := store.Query{Vec: s.know.words.Weigh(question), K: n}
	if subject := s.know.fold(question); subject != "" {
		query.Filter = store.Filter{}.Eq(store.Space, subject)
	}
	near, err := s.mem.Search(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make([]Cite, 0, len(near))
	for _, m := range near {
		if m.Score <= 0 {
			break
		}
		out = append(out, Cite{Turn: s.know.turns[s.turn[m.ID]], Score: m.Score})
	}
	return out, nil
}

// Flat is the graph's claim-span index with the subject filter taken off: the
// same pieces, ranked against every person's at once.
type Flat struct{ *Knowledge }

// Name is what this arm is called in the results.
func (Flat) Name() string { return "span" }

// Recall ranks every piece, whoever it belongs to.
func (f Flat) Recall(ctx context.Context, question string, n int) ([]Cite, error) {
	return f.Knowledge.recall(ctx, question, n, false)
}
