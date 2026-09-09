package main

import (
	"math"
	"sort"
	"strings"
)

// One vocabulary, weighed once, shared by both arms and by the reader. A term
// that appears in every turn separates nothing and a term that appears in one
// separates it completely, which is what inverse document frequency measures.
// Keeping the weighing in one place is what makes the comparison a comparison:
// when the two arms disagree it is because they index different things, not
// because they count words differently.

// Words is the vocabulary of one conversation.
type Words struct {
	weight map[string]float64 // term to its inverse document frequency
	at     map[string]int     // term to its position in a vector
	turns  int
}

// Vocabulary reads every turn and weighs every term in them.
func Vocabulary(turns []Turn) *Words {
	w := &Words{weight: map[string]float64{}, at: map[string]int{}, turns: len(turns)}
	seen := map[string]int{}
	for _, t := range turns {
		for term := range unique(stems(t.Text)) {
			seen[term]++
		}
	}
	terms := make([]string, 0, len(seen))
	for term := range seen {
		terms = append(terms, term)
	}
	sort.Strings(terms)
	for i, term := range terms {
		w.at[term] = i
		w.weight[term] = math.Log(float64(len(turns)+1) / float64(seen[term]+1))
	}
	return w
}

// Size is how many terms the vocabulary holds, which is the width of a vector.
func (w *Words) Size() int { return len(w.at) }

// Weight is what a term is worth. A term the conversation never used is worth
// as much as one used once: nothing said it, so nothing can cover it.
func (w *Words) Weight(term string) float64 {
	if v, ok := w.weight[term]; ok {
		return v
	}
	return math.Log(float64(w.turns + 1))
}

// Weigh turns a text into a unit-length vector over the vocabulary: term
// frequency times weight, normalised so that a long turn is not nearer to
// everything than a short one.
func (w *Words) Weigh(text string) []float32 {
	v := make([]float32, len(w.at))
	for term, n := range count(stems(text)) {
		i, known := w.at[term]
		if !known {
			continue
		}
		v[i] = float32(float64(n) * w.Weight(term))
	}
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return v
	}
	scale := float32(1 / math.Sqrt(sum))
	for i := range v {
		v[i] *= scale
	}
	return v
}

// Focus is what a question is asking about: its terms, less the stop words it
// is phrased with and less the name of whoever it is about, because that name
// is the question's subject and not part of what is being asked.
func (w *Words) Focus(question, subject string) []string {
	drop := unique(stems(subject))
	var out []string
	for _, term := range stems(question) {
		if stop[term] || asked[term] || drop[term] {
			continue
		}
		out = append(out, term)
	}
	return out
}

// Cover is how much of a question's focus a piece of evidence accounts for,
// weighted, from nothing to all of it. Sure applies it to the turns a memory
// returned, the reader applies it to the sentences of one of them, and the
// graph applies it to a claim: one measure, three scales of evidence.
//
// The evidence arrives already reduced to its terms, because a memory reduces
// what it holds once when it is built and a question is asked of it thousands
// of times.
func (w *Words) Cover(focus []string, have map[string]bool) float64 {
	if len(focus) == 0 {
		return 0
	}
	var hit, all float64
	for _, term := range focus {
		weight := w.Weight(term)
		all += weight
		if have[term] {
			hit += weight
		}
	}
	if all == 0 {
		return 0
	}
	return hit / all
}

// Terms is the reduced form of a text: what it is made of, once, as a set.
func Terms(text string) map[string]bool { return unique(stems(text)) }

// unique is the set of terms in a list.
func unique(terms []string) map[string]bool {
	out := make(map[string]bool, len(terms))
	for _, t := range terms {
		out[t] = true
	}
	return out
}

// count is how many times each term appears.
func count(terms []string) map[string]int {
	out := make(map[string]int, len(terms))
	for _, t := range terms {
		out[t]++
	}
	return out
}

// asked are the words a question is built from rather than about, in the
// stemmed form Focus sees them in.
var asked = stemmed("what when where who whom which why how did does do is are " +
	"was were has have had would could will can according mentioned many much " +
	"name names")

// stemmed is a lookup of the stems of a space-separated list.
func stemmed(words string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.Fields(words) {
		out[stem(w)] = true
	}
	return out
}
