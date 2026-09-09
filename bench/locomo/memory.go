package main

import "context"

// What the two arms have in common. Both are asked a question and both answer
// with turns of the conversation, best first. Everything downstream — the
// confidence, the reader that writes the answer, the scorer that marks it, the
// recall against the dataset's own evidence — sees only that, so the arms are
// compared on which turns they remember and on nothing else.

// Cite is one turn a memory offers, and how well it matched.
type Cite struct {
	Turn  Turn
	Score float64
	// Say is the phrase the memory matched inside the turn, when it has one.
	// A memory that indexes whole turns has nothing to put here; a memory that
	// indexes claims does, and the answer can be read straight off it.
	Say string
}

// Memory is a conversation, remembered somehow, and asked about.
type Memory interface {
	// Name is what the arm is called in the results.
	Name() string
	// Recall returns up to k turns bearing on the question, best first.
	Recall(ctx context.Context, question string, k int) ([]Cite, error)
}

// Sure is how much of what a question asks about the turns a memory returned
// actually account for, weighted so a rare word counts for more than a common
// one, from nothing to all of it.
//
// It is computed here rather than inside a memory, over the turns themselves
// rather than over whatever a memory happens to index, so that the number
// means the same thing in every column. A memory that indexes short pieces
// would otherwise look less sure of the same evidence than one that indexes
// whole turns, and the report would be measuring text length.
func Sure(w *Words, question string, cites []Cite) float64 {
	focus := w.Focus(question, "")
	best := 0.0
	for _, c := range cites {
		if cover := w.Cover(focus, Terms(c.Turn.Text)); cover > best {
			best = cover
		}
	}
	return best
}

// ids is the dialogue identifiers of a list of cites, in order, which is what
// recall against the dataset's evidence is measured over.
func ids(cites []Cite) []string {
	out := make([]string, 0, len(cites))
	for _, c := range cites {
		out = append(out, c.Turn.ID)
	}
	return out
}
