package store

import "sort"

// Fuse merges ranked lists by reciprocal rank: an item scores the sum, over
// the lists it appears in, of 1/(k+rank), counting rank from 1. It needs no
// scores, only orders, which is what makes it the way to combine answers
// from sources that score on different scales — a vector store and a text
// index, say.
//
// k damps how much the head of any one list decides the outcome; 60 is the
// constant from the original description and is used when k is not
// positive. Metadata comes from the first list an item appeared in.
func Fuse(k int, lists ...[]Match) []Match {
	if k <= 0 {
		k = 60
	}
	return merge(lists, func(_ int, rank int, _ Match) float64 {
		return 1 / float64(k+rank)
	})
}

// Blend merges ranked lists by weighted sum of the scores they already
// carry, which is the right combination when the sources score on one
// scale. A weight missing for a list counts as an equal share, 1/n.
func Blend(w []float64, lists ...[]Match) []Match {
	share := 0.0
	if len(lists) > 0 {
		share = 1 / float64(len(lists))
	}
	return merge(lists, func(list, _ int, m Match) float64 {
		if list < len(w) {
			return m.Score * w[list]
		}
		return m.Score * share
	})
}

// merge accumulates one score per id across lists and orders the result,
// highest first, ties broken by where the item was first seen so the answer
// is the same on every run.
func merge(lists [][]Match, score func(list, rank int, m Match) float64) []Match {
	sum := map[string]float64{}
	var seen []Match
	at := map[string]bool{}
	for li, l := range lists {
		for i, m := range l {
			if !at[m.ID] {
				at[m.ID] = true
				seen = append(seen, m)
			}
			sum[m.ID] += score(li, i+1, m)
		}
	}
	out := make([]Match, len(seen))
	for i, m := range seen {
		m.Score = sum[m.ID]
		out[i] = m
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

// top orders matches highest first, ties broken by id so the answer never
// depends on map order, and keeps the first k. A k of zero or less keeps
// them all.
func top(ms []Match, k int) []Match {
	sort.Slice(ms, func(i, j int) bool {
		if ms[i].Score != ms[j].Score {
			return ms[i].Score > ms[j].Score
		}
		return ms[i].ID < ms[j].ID
	})
	if k > 0 && k < len(ms) {
		ms = ms[:k]
	}
	return ms
}
