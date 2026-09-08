package dedupe

import (
	"context"

	"github.com/hanzoai/semantic"
)

// Unique returns ts with repeated assertions removed, keeping the first
// spelling of each. Two triples repeat when they fold to the same canonical
// key, so "works at" and "WORKS_AT" under a synonym table are one claim.
//
// The kept triple's score is raised to the best any repetition carried: the
// same claim found twice is better evidence than the same claim found once.
func Unique(ts []semantic.Triple, c Canon) []semantic.Triple {
	at := make(map[[3]string]int, len(ts))
	out := make([]semantic.Triple, 0, len(ts))
	for _, t := range ts {
		k := c.Key(t)
		if i, ok := at[k]; ok {
			if t.Score > out[i].Score {
				out[i].Score = t.Score
			}
			continue
		}
		at[k] = len(out)
		out = append(out, t)
	}
	return out
}

// Alike scores two assertions on 0 to 1. Assertions about different subjects
// are not about the same thing and score 0. Otherwise the predicate carries
// most of the weight and the object the rest, because two documents that
// agree on the object while naming the relation differently are usually
// saying one thing, and two that agree on the relation while naming different
// objects are usually not.
func Alike(a, b semantic.Triple, c Canon, m Metric) float64 {
	if fold(a.Subject) != fold(b.Subject) {
		return 0
	}
	x, y := c.Key(a), c.Key(b)
	if x == y {
		return 1
	}
	p, o := 1.0, 1.0
	if x[1] != y[1] {
		p = Text(x[1], y[1], m)
	}
	if x[2] != y[2] {
		o = Text(x[2], y[2], m)
	}
	return p*0.6 + o*0.4
}

// Repeats reports the assertions in ts that say the same thing as another,
// as index pairs into ts, lower index first. A pair is reported when it
// scores at least like.
//
// Indices rather than triples, because the caller knows what each index meant
// and a copy of the triple would lose that.
func Repeats(ctx context.Context, ts []semantic.Triple, c Canon, like float64, m Metric) ([][2]int, error) {
	if like < 0 || like > 1 {
		return nil, ErrOption
	}
	var out [][2]int
	for i := range ts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for j := i + 1; j < len(ts); j++ {
			if Alike(ts[i], ts[j], c, m) >= like {
				out = append(out, [2]int{i, j})
			}
		}
	}
	return out, nil
}
