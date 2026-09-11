package main

import (
	"strings"
	"testing"
)

// What an extractive reader could get at best.
//
// The report's ceiling is the oracle column: the deterministic reader, handed
// the turns the dataset itself names as the evidence. That number is low, and
// two different things could make it low. The reader may be quoting the wrong
// words out of evidence that contains the right ones, in which case a better
// reader lifts it. Or the reference answer may not be in the evidence at all —
// LoCoMo's answers are written by an annotator, not copied out of the turns —
// in which case no reader that quotes can ever reach it and only one that
// writes its own words can.
//
// This separates the two. For every answerable question it takes the stems of
// the reference answer and asks how many of them appear anywhere in the turns
// the dataset named. That share is the recall an ideal extractor would have,
// and it is an upper bound on the recall half of the F1 the metric marks.
func TestAnswersAreInTheEvidence(t *testing.T) {
	samples, err := Load("data/locomo10.json")
	if err != nil {
		t.Skipf("dataset: %v", err)
	}

	type count struct {
		n           int
		have, whole float64
	}
	by := map[int]*count{}
	for _, s := range samples {
		at := make(map[string]Turn, len(s.Turns))
		for _, turn := range s.Turns {
			at[turn.ID] = turn
		}
		for _, ask := range s.Asks {
			if ask.Kind == 5 {
				continue
			}
			var said []string
			for _, id := range ask.Evidence {
				if turn, known := at[trim(id)]; known {
					said = append(said, turn.Text)
				}
			}
			held := map[string]int{}
			for _, w := range stems(strings.Join(said, " ")) {
				held[w]++
			}
			want := stems(ask.Answer)
			if len(want) == 0 {
				continue
			}
			same := 0
			for _, w := range want {
				if held[w] > 0 {
					held[w]--
					same++
				}
			}
			if by[ask.Kind] == nil {
				by[ask.Kind] = &count{}
			}
			c := by[ask.Kind]
			c.n++
			c.have += float64(same) / float64(len(want))
			if same == len(want) {
				c.whole++
			}
		}
	}

	t.Log("share of the reference answer's words that appear in the evidence the dataset names")
	all := &count{}
	for _, k := range Kinds {
		c := by[k.Kind]
		if c == nil {
			continue
		}
		t.Logf("%-12s n=%4d  words in evidence %.3f  answers wholly in evidence %.3f",
			k.Name, c.n, c.have/float64(c.n), c.whole/float64(c.n))
		all.n += c.n
		all.have += c.have
		all.whole += c.whole
	}
	t.Logf("%-12s n=%4d  words in evidence %.3f  answers wholly in evidence %.3f",
		"answerable", all.n, all.have/float64(all.n), all.whole/float64(all.n))
	if all.n == 0 {
		t.Fatal("no answerable questions were measured")
	}
}
