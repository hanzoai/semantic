package agent

import (
	"strings"
	"unicode"
)

// How alike two pieces of text are, which is how a precedent is found before
// anyone has embeddings to hand. Word overlap is the measure for scripts that
// separate words; where it cannot work — a script written without spaces, or a
// query that is one token — character bigrams stand in for it.

// words splits text into lowercase words, keeping the order and dropping
// repeats, so the count of query words is the count of distinct things asked
// for.
func words(s string) []string {
	seen := map[string]bool{}
	var out []string
	for w := range strings.FieldsSeq(strings.ToLower(s)) {
		if !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}

func set(ss []string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}

// share is the Jaccard index of two sets: what they hold in common over
// everything they hold between them.
func share(a, b map[string]bool) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	both := 0
	for k := range a {
		if b[k] {
			both++
		}
	}
	return float64(both) / float64(len(a)+len(b)-both)
}

// bigrams are the two-character sequences of text with whitespace removed,
// which is what makes a script written without spaces comparable at all.
func bigrams(s string) map[string]bool {
	r := []rune(strings.Join(strings.Fields(strings.ToLower(s)), ""))
	out := map[string]bool{}
	for i := 0; i+1 < len(r); i++ {
		out[string(r[i:i+2])] = true
	}
	return out
}

// spaced reports whether text is written in a script that separates words.
func spaced(s string) bool {
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Han, r), unicode.Is(unicode.Hiragana, r),
			unicode.Is(unicode.Katakana, r), unicode.Is(unicode.Hangul, r):
			return false
		}
	}
	return true
}

// alike scores how much of q the text carries, between 0 and 1.
//
// Word overlap is the measure. Bigrams are tried only when word overlap
// cannot work — q is written without word spaces, or is a single token — so
// that the incidental bigram overlap between two unrelated English sentences
// never inflates an ordinary score. Three bigrams are required before the
// bigram side counts, which keeps one- and two-character queries from
// matching everything while leaving a three-character phrase workable.
func alike(q, text string) float64 {
	qw, tw := words(q), words(text)
	by := share(set(qw), set(tw))
	if spaced(q) && len(qw) > 1 {
		return by
	}
	qb := bigrams(q)
	if len(qb) < 3 {
		return by
	}
	if b := share(qb, bigrams(text)); b > by {
		return b
	}
	return by
}

// shape is how alike two nodes are by their place in the graph rather than
// their words: nodes of different kinds are not comparable, and nodes of the
// same kind are as alike as their edge counts are close. The caller holds the
// read lock.
func (g *Graph) shape(a, b *Node) float64 {
	if a.Kind != b.Kind {
		return 0
	}
	x, y := float64(len(g.out[a.ID])), float64(len(g.out[b.ID]))
	most := x
	if y > most {
		most = y
	}
	if most < 1 {
		most = 1
	}
	d := x - y
	if d < 0 {
		d = -d
	}
	return 1 - d/most
}

// Like finds the nodes most like one already in the graph, best first. Words
// are the measure by default; by shape when byShape is set, which asks about
// position in the graph instead of content.
func (g *Graph) Like(id string, byShape bool, n int) []Hit {
	g.mu.RLock()
	defer g.mu.RUnlock()
	ref, ok := g.nodes[id]
	if !ok {
		return nil
	}
	var hits []Hit
	for _, other := range g.order {
		if other == id {
			continue
		}
		o := g.nodes[other]
		if o == nil {
			continue
		}
		score := alike(ref.Text, o.Text)
		if byShape {
			score = g.shape(ref, o)
		}
		hits = append(hits, Hit{Node: *o, Score: score})
	}
	sortHits(hits)
	if n > 0 && len(hits) > n {
		hits = hits[:n]
	}
	return hits
}
