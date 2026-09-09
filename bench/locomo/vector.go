package main

import (
	"context"

	"github.com/hanzoai/semantic/store"
)

// The baseline: remember a conversation as a heap of turns and find the ones
// whose words sit closest to the question's. This is the approach the
// benchmark is usually attempted with, so it is built to be as strong as it
// honestly can be rather than as a foil — the retrieved unit is one turn with
// its date and speaker attached, which is what the official RAG harness
// embeds, and the terms are stemmed and weighted by how rare they are before
// the cosine is taken.
//
// The weighting is lexical, not a learned embedding: nothing here reaches a
// network, and on this data lexical retrieval is the harder baseline, because
// LoCoMo questions are written from the turns they are about and repeat their
// words. A dense retriever generalises across wording; it does not fix the
// failure this benchmark turns on, which is that a turn near the question's
// words is not the same thing as a turn about the person the question names.

// Vector is a conversation kept as vectors, one per turn.
type Vector struct {
	turns []Turn
	at    map[string]int
	words *Words
	mem   *store.Mem
}

// NewVector indexes every turn.
func NewVector(ctx context.Context, turns []Turn, words *Words) (*Vector, error) {
	v := &Vector{
		turns: turns,
		at:    make(map[string]int, len(turns)),
		words: words,
		mem:   &store.Mem{},
	}
	// Weigh returns unit vectors, so the inner product is the cosine and the
	// two lengths do not have to be computed for every comparison.
	v.mem.Rank(store.Inner)
	for i, t := range turns {
		v.at[t.ID] = i
		if err := v.mem.Put(ctx, t.ID, words.Weigh(t.Line()), nil); err != nil {
			return nil, err
		}
	}
	return v, nil
}

// Name is what this arm is called in the results.
func (v *Vector) Name() string { return "vector" }

// Recall returns the k turns nearest the question.
func (v *Vector) Recall(ctx context.Context, question string, k int) ([]Cite, error) {
	near, err := v.mem.Near(ctx, v.words.Weigh(question), k)
	if err != nil {
		return nil, err
	}
	out := make([]Cite, 0, len(near))
	for _, m := range near {
		if m.Score <= 0 {
			break // a turn with no word in common with the question is not evidence
		}
		out = append(out, Cite{Turn: v.turns[v.at[m.ID]], Score: m.Score})
	}
	return out, nil
}
