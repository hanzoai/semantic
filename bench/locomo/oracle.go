package main

import (
	"context"
	"sort"
)

// A third memory that cheats, and is in the run for exactly that reason. It
// returns the turns the dataset itself names as the evidence for a question:
// perfect retrieval, nothing else changed. What it scores is the ceiling the
// reader puts on the whole benchmark, so the gap between an arm and the oracle
// is what better remembering could still be worth, and the gap between the
// oracle and 1.0 is what the reader costs everybody equally.
//
// Without it a low number is unreadable — it could mean the memory failed to
// find the turn or that the reader failed to quote it, and those call for
// different work.

// Oracle answers from the dataset's own evidence.
type Oracle struct {
	turns []Turn
	at    map[string]int
	told  map[string][]string // question to the turns that answer it
	words *Words
}

// NewOracle indexes the answers the dataset gives away.
func NewOracle(s Sample, words *Words) *Oracle {
	o := &Oracle{
		turns: s.Turns,
		at:    make(map[string]int, len(s.Turns)),
		told:  make(map[string][]string, len(s.Asks)),
		words: words,
	}
	for i, t := range s.Turns {
		o.at[t.ID] = i
	}
	for _, ask := range s.Asks {
		o.told[ask.Text] = ask.Evidence
	}
	return o
}

// Name is what this arm is called in the results.
func (o *Oracle) Name() string { return "oracle" }

// Recall returns the evidence turns, ranked among themselves by how much of
// the question they cover so that the reader still has to choose.
func (o *Oracle) Recall(ctx context.Context, question string, k int) ([]Cite, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	focus := o.words.Focus(question, "")
	var out []Cite
	for _, id := range o.told[question] {
		i, known := o.at[trim(id)]
		if !known {
			continue
		}
		out = append(out, Cite{
			Turn:  o.turns[i],
			Score: o.words.Cover(focus, Terms(o.turns[i].Text)),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > k {
		out = out[:k]
	}
	return out, nil
}
