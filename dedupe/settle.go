package dedupe

import "fmt"

// Rule picks how a disagreement is settled.
type Rule string

const (
	// Vote takes the value the most documents state.
	Vote Rule = "vote"
	// Credible takes the value whose documents are most trusted.
	Credible Rule = "credible"
	// Newest takes the value stated latest, or the one stated last where no
	// document is dated.
	Newest Rule = "newest"
	// Oldest takes the value stated earliest, or the one stated first where no
	// document is dated.
	Oldest Rule = "oldest"
	// Sure takes the value from the document most confident of itself.
	Sure Rule = "sure"
	// Flag settles nothing and says so. An unsettled disagreement is a
	// result: it stays in the graph, visible, until a person decides.
	Flag Rule = "flag"
)

// Trust scores documents on how far each is believed. A document it does not
// name is believed halfway, which is the benefit of the doubt an unrated
// document gets and the value a nil Trust gives everything.
type Trust map[string]float64

// Of returns the credit given to a document.
func (t Trust) Of(doc string) float64 {
	if v, ok := t[doc]; ok {
		return v
	}
	return 0.5
}

// Pick is what settling a disagreement chose, and why. A Pick that is not
// Done chose nothing: the disagreement stands, and the caller can see both
// that it stands and what was weighed.
type Pick struct {
	Of    string // the conflict settled
	Done  bool
	Value any
	By    Rule
	Score float64  // confidence in the choice
	From  []string // documents the choice rests on
	Why   string
}

// Settle applies one rule to a conflict.
func (r Rule) Settle(c Conflict, t Trust) Pick {
	p := Pick{Of: c.ID, By: r}
	if len(c.Values) == 0 {
		p.Why = "nothing was stated"
		return p
	}
	switch r {
	case Flag:
		p.Why = "left for a person to decide"
		return p
	case Vote:
		return vote(p, c)
	case Credible:
		return weigh(p, c, t)
	case Newest:
		return latest(p, c, true)
	case Oldest:
		return latest(p, c, false)
	case Sure:
		return surest(p, c)
	}
	p.Why = fmt.Sprintf("no rule named %q", string(r))
	return p
}

// Settle applies one rule to every conflict, returning one Pick each. Nothing
// is dropped: a conflict the rule cannot settle comes back unsettled.
func Settle(cs []Conflict, r Rule, t Trust) []Pick {
	out := make([]Pick, 0, len(cs))
	for _, c := range cs {
		out = append(out, r.Settle(c, t))
	}
	return out
}

// vote takes the value stated most often. Ties go to the value stated first,
// so the choice does not move between runs.
func vote(p Pick, c Conflict) Pick {
	count := map[string]int{}
	for _, v := range c.Values {
		count[fmt.Sprint(v.Value)]++
	}
	best, votes := "", 0
	for _, v := range c.Values {
		if k := fmt.Sprint(v.Value); count[k] > votes {
			best, votes = k, count[k]
		}
	}
	p.Done = true
	p.Value = first(c, best)
	p.Score = float64(votes) / float64(len(c.Values))
	p.From = docs(c, best)
	p.Why = fmt.Sprintf("%d of %d documents state it", votes, len(c.Values))
	return p
}

// weigh takes the value whose documents carry the most credit: each document's
// own confidence times how far it is trusted, summed over the documents that
// state the value.
func weigh(p Pick, c Conflict, t Trust) Pick {
	weight := map[string]float64{}
	var total float64
	for _, v := range c.Values {
		w := sure(v.From) * t.Of(v.From.Doc)
		weight[fmt.Sprint(v.Value)] += w
		total += w
	}
	best, most := "", -1.0
	for _, v := range c.Values {
		if k := fmt.Sprint(v.Value); weight[k] > most {
			best, most = k, weight[k]
		}
	}
	if total == 0 {
		p.Why = "no document carries any credit"
		return p
	}
	p.Done = true
	p.Value = first(c, best)
	p.Score = most / total
	p.From = docs(c, best)
	p.Why = fmt.Sprintf("its documents carry %.2f of %.2f credit", most, total)
	return p
}

// latest takes the value stated at the extreme of time. Where no document is
// dated it falls back to the order the claims were made in, which is the only
// order there is.
func latest(p Pick, c Conflict, newest bool) Pick {
	best, dated := 0, false
	for i, v := range c.Values {
		at := v.From.At
		if at.IsZero() {
			continue
		}
		if !dated {
			best, dated = i, true
			continue
		}
		if cur := c.Values[best].From.At; newest && at.After(cur) || !newest && at.Before(cur) {
			best = i
		}
	}
	why := "stated earliest"
	if !dated {
		best = 0
		why = "no document is dated; taking the one stated first"
		if newest {
			best = len(c.Values) - 1
			why = "no document is dated; taking the one stated last"
		}
	} else if newest {
		why = "stated latest"
	}
	p.Done = true
	p.Value = c.Values[best].Value
	p.Score = sure(c.Values[best].From)
	p.From = []string{c.Values[best].From.Doc}
	p.Why = why
	return p
}

// surest takes the value from the document most confident of itself. Ties go
// to the document that stated it first.
func surest(p Pick, c Conflict) Pick {
	best := 0
	for i, v := range c.Values {
		if sure(v.From) > sure(c.Values[best].From) {
			best = i
		}
	}
	p.Done = true
	p.Value = c.Values[best].Value
	p.Score = sure(c.Values[best].From)
	p.From = []string{c.Values[best].From.Doc}
	p.Why = fmt.Sprintf("its document is %.2f sure", p.Score)
	return p
}

// first returns the value behind a chosen text, as it was originally stated,
// so that a number settled by voting comes back a number.
func first(c Conflict, key string) any {
	for _, v := range c.Values {
		if fmt.Sprint(v.Value) == key {
			return v.Value
		}
	}
	return nil
}

// docs names the documents that stated a value, each once, in order.
func docs(c Conflict, key string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range c.Values {
		if fmt.Sprint(v.Value) != key || v.From.Doc == "" || seen[v.From.Doc] {
			continue
		}
		seen[v.From.Doc] = true
		out = append(out, v.From.Doc)
	}
	return out
}
