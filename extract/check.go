package extract

import (
	"errors"
	"fmt"
	"strings"
)

// Report is what a check found: whether the extraction passed, how much of it
// held up as a share in [0,1], the reasons it failed, milder remarks that did
// not fail it, and the counts behind all of them.
type Report struct {
	OK      bool
	Score   float64
	Errs    []string
	Warns   []string
	Counts  map[string]int
	Unknown []string // labels a Schema did not recognise
}

// Err is nil when the report passed, and otherwise an error naming every
// reason it did not. It wraps ErrInvalid, so a caller can tell a rejected
// extraction from a transport failure.
func (r Report) Err() error {
	if r.OK {
		return nil
	}
	errs := make([]error, 0, len(r.Errs)+1)
	errs = append(errs, ErrInvalid)
	for _, e := range r.Errs {
		errs = append(errs, errors.New(e))
	}
	return errors.Join(errs...)
}

// Test is a check over an extraction. Schema and Floor are the two, and they
// are orthogonal: one asks whether the extraction is in the vocabulary, the
// other whether it is sure enough and structurally sound. A caller that wants
// both runs both, in either order.
type Test interface {
	Check(Set) Report
	Keep(Set) Set
}

var (
	_ Test = Schema{}
	_ Test = Floor(0)
)

// Floor is the least confidence an extraction may carry. It checks the axis a
// Schema does not: how much of the extraction clears the bar, and whether it
// is structurally sound — no entity with empty text, no relation missing an
// endpoint or pointing at itself. The zero Floor admits any score and still
// reports the structural faults.
type Floor float64

// Check scores an extraction on confidence and structure. Entities and
// relations are scored the way the Python validator scores them — a penalty
// for the share below the floor, a penalty for the share that is malformed,
// and a factor for the mean confidence — and an extraction holding both takes
// the mean of the two.
func (f Floor) Check(x Set) Report {
	r := Report{Counts: map[string]int{}}
	min := float64(f)

	lowEnt, empty, sumEnt := 0, 0, 0.0
	high, mid := 0, 0
	texts, labels := map[string]bool{}, map[string]bool{}
	for _, e := range x.Entities {
		sumEnt += e.Score
		switch {
		case e.Score < min:
			lowEnt++
		case e.Score >= 0.8:
			high++
		default:
			mid++
		}
		if strings.TrimSpace(e.Text) == "" {
			empty++
		}
		texts[e.Text] = true
		labels[e.Label] = true
	}
	if lowEnt > 0 {
		r.Warns = append(r.Warns, fmt.Sprintf("%d entities below the confidence floor", lowEnt))
	}
	if empty > 0 {
		r.Errs = append(r.Errs, fmt.Sprintf("%d entities with empty text", empty))
	}
	avgEnt := mean(sumEnt, len(x.Entities))
	r.Counts["entities"] = len(x.Entities)
	r.Counts["high"] = high
	r.Counts["medium"] = mid
	r.Counts["low"] = lowEnt
	r.Counts["distinct"] = len(texts)
	r.Counts["labels"] = len(labels)
	r.Counts["empty"] = empty

	lowRel, torn, sumRel := 0, 0, 0.0
	preds := map[string]bool{}
	for _, rel := range x.Relations {
		sumRel += rel.Score
		if rel.Score < min {
			lowRel++
		}
		if rel.Subject.Text == "" || rel.Object.Text == "" || rel.Subject.Text == rel.Object.Text {
			torn++
		}
		preds[rel.Predicate] = true
	}
	if lowRel > 0 {
		r.Warns = append(r.Warns, fmt.Sprintf("%d relations below the confidence floor", lowRel))
	}
	if torn > 0 {
		r.Errs = append(r.Errs, fmt.Sprintf("%d relations missing an endpoint or joining a thing to itself", torn))
	}
	avgRel := mean(sumRel, len(x.Relations))
	r.Counts["relations"] = len(x.Relations)
	r.Counts["predicates"] = len(preds)
	r.Counts["torn"] = torn

	var scores []float64
	if n := len(x.Entities); n > 0 {
		s := 1 - float64(lowEnt)/float64(n)*0.5
		scores = append(scores, clamp(s*(0.5+avgEnt*0.5)))
	}
	if n := len(x.Relations); n > 0 {
		s := (1 - float64(lowRel)/float64(n)*0.5) * (1 - float64(torn)/float64(n)*0.7)
		scores = append(scores, clamp(s*(0.5+avgRel*0.5)))
	}
	for _, s := range scores {
		r.Score += s
	}
	if len(scores) > 0 {
		r.Score /= float64(len(scores))
	}

	r.OK = len(r.Errs) == 0
	return r
}

// Keep returns the part of an extraction that clears the floor.
func (f Floor) Keep(x Set) Set {
	var out Set
	for _, e := range x.Entities {
		if e.Score >= float64(f) {
			out.Entities = append(out.Entities, e)
		}
	}
	for _, r := range x.Relations {
		if r.Score >= float64(f) {
			out.Relations = append(out.Relations, r)
		}
	}
	return out
}

// share is the fraction of an extraction that passed, counting entities and
// relations together. An empty extraction passes vacuously.
func share(goodEnt, nEnt, goodRel, nRel int) float64 {
	total := nEnt + nRel
	if total == 0 {
		return 1
	}
	return float64(goodEnt+goodRel) / float64(total)
}

func mean(sum float64, n int) float64 {
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}
