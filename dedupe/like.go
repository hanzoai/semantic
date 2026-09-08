package dedupe

import (
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/hanzoai/semantic"
)

// Metric picks how two strings are compared.
type Metric string

const (
	// Exact scores 1 for strings equal after case and space folding, 0 otherwise.
	Exact Metric = "exact"
	// Edit scores the Levenshtein distance against the longer string.
	Edit Metric = "edit"
	// Jaro scores Jaro-Winkler similarity, with the prefix bonus. It is the default.
	Jaro Metric = "jaro"
	// Gram scores the Jaccard overlap of character bigrams.
	Gram Metric = "gram"
	// Token scores the Jaccard overlap of whole words, which is what to use
	// when word order varies but the words themselves do not.
	Token Metric = "token"
)

// Text scores how alike two strings are, on 0 to 1. An empty string scores 0
// against anything, equal strings score 1, and the metric decides the rest.
// The zero Metric is Jaro.
func Text(a, b string, m Metric) float64 {
	if a == "" || b == "" {
		return 0
	}
	x, y := fold(a), fold(b)
	if x == y {
		return 1
	}
	switch m {
	case Exact:
		return 0
	case Edit:
		return edit(x, y)
	case Gram:
		return gram(x, y)
	case Token:
		return overlap(words(x), words(y))
	default:
		return jaro(x, y)
	}
}

// words splits a string into the set of words it is compared by. Underscores
// and hyphens are word breaks, so works_at and "works at" are one word set.
func words(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(s, split) {
		out[w] = true
	}
	return out
}

func split(r rune) bool { return r == ' ' || r == '_' || r == '-' || r == '\t' || r == '\n' }

// overlap is the Jaccard index of two sets: what they share over what they
// hold between them. Two empty sets are alike, since neither says anything
// the other contradicts.
func overlap(a, b map[string]bool) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	hit := 0
	for k := range a {
		if b[k] {
			hit++
		}
	}
	union := len(a) + len(b) - hit
	if union == 0 {
		return 0
	}
	return float64(hit) / float64(union)
}

// Cosine scores two vectors on 0 to 1: the cosine of the angle between them,
// mapped from -1..1 so it composes with the string scores. Vectors of
// different lengths, and vectors of length zero, score 0.
func Cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return (dot/(math.Sqrt(na)*math.Sqrt(nb)) + 1) / 2
}

// edit is 1 minus the Levenshtein distance over the longer length.
func edit(a, b string) float64 {
	x, y := []rune(a), []rune(b)
	n := max(len(x), len(y))
	if n == 0 {
		return 0
	}
	return 1 - float64(dist(x, y))/float64(n)
}

// dist is the Levenshtein distance, computed one row at a time.
func dist(a, b []rune) int {
	if len(a) < len(b) {
		a, b = b, a
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			sub := prev[j-1]
			if a[i-1] != b[j-1] {
				sub++
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, sub)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// jaro is Jaro-Winkler: Jaro similarity plus a bonus for a shared prefix of
// up to four characters.
func jaro(a, b string) float64 {
	j := plain(a, b)
	x, y := []rune(a), []rune(b)
	n := 0
	for n < min(len(x), len(y)) && x[n] == y[n] {
		n++
	}
	return j + float64(min(n, 4))*0.1*(1-j)
}

// plain is the Jaro similarity: matched characters within half the longer
// length, discounted by half the transpositions between them.
func plain(a, b string) float64 {
	x, y := []rune(a), []rune(b)
	if len(x) == 0 || len(y) == 0 {
		return 0
	}
	window := max(len(x), len(y))/2 - 1
	if window < 0 {
		window = 0
	}
	hitX := make([]bool, len(x))
	hitY := make([]bool, len(y))
	hits := 0
	for i := range x {
		lo := max(0, i-window)
		hi := min(i+window+1, len(y))
		for j := lo; j < hi; j++ {
			if hitY[j] || x[i] != y[j] {
				continue
			}
			hitX[i], hitY[j] = true, true
			hits++
			break
		}
	}
	if hits == 0 {
		return 0
	}
	swaps, k := 0, 0
	for i := range x {
		if !hitX[i] {
			continue
		}
		for !hitY[k] {
			k++
		}
		if x[i] != y[k] {
			swaps++
		}
		k++
	}
	h := float64(hits)
	return (h/float64(len(x)) + h/float64(len(y)) + (h-float64(swaps)/2)/h) / 3
}

// gram is the Jaccard overlap of character bigrams. Two strings too short to
// have a bigram are alike only if both are.
func gram(a, b string) float64 {
	x, y := bigrams(a), bigrams(b)
	if len(x) == 0 && len(y) == 0 {
		return 1
	}
	hit := 0
	for g := range x {
		if y[g] {
			hit++
		}
	}
	union := len(x) + len(y) - hit
	if union == 0 {
		return 0
	}
	return float64(hit) / float64(union)
}

func bigrams(s string) map[string]bool {
	r := []rune(s)
	out := make(map[string]bool, max(0, len(r)-1))
	for i := 0; i+1 < len(r); i++ {
		out[string(r[i:i+2])] = true
	}
	return out
}

// Weights say how much each kind of evidence counts toward likeness. The
// zero value counts the name most, properties and edges equally after it,
// and ignores vectors, which is the balance to use where the records carry
// names worth trusting.
type Weights struct{ Name, Props, Edges, Vector float64 }

func (w Weights) or() Weights {
	if w == (Weights{}) {
		return Weights{Name: 0.6, Props: 0.2, Edges: 0.2}
	}
	return w
}

// Score is a likeness and the parts it was made of. Parts holds only the
// evidence that existed, so a pair with no vectors has no "vector" part.
type Score struct {
	Total float64
	Parts map[string]float64
}

// Likeness scores how alike two entities are: their names, the values of the
// properties they share, the edges they have in common and, when both carry
// one, their vectors. Only the parts that exist are weighed, so missing
// evidence neither helps nor hurts.
//
// The parts are weighed in a fixed order. Floating-point addition is not
// associative, so summing them in map order would score the same comparison
// differently between runs, and a ranking built on those scores would not be
// reproducible.
func Likeness(a, b Entity, w Weights, m Metric) Score {
	type part struct {
		name         string
		value, share float64
	}
	w = w.or()
	parts := []part{
		{"name", Text(a.Name, b.Name, m), w.Name},
		{"props", props(a, b, m), w.Props},
		{"edges", edges(a, b), w.Edges},
	}
	if len(a.Vector) > 0 && len(b.Vector) > 0 {
		parts = append(parts, part{"vector", Cosine(a.Vector, b.Vector), w.Vector})
	}
	s := Score{Parts: make(map[string]float64, len(parts))}
	var total float64
	for _, p := range parts {
		s.Parts[p.name] = p.value
		total += p.share
	}
	if total == 0 {
		return s
	}
	for _, p := range parts {
		s.Total += p.value * p.share / total
	}
	return s
}

// props scores the property values two entities share. A property only one
// of them states is neither agreement nor disagreement, so it scores half.
func props(a, b Entity, m Metric) float64 {
	if len(a.Props) == 0 && len(b.Props) == 0 {
		return 1
	}
	seen := map[string]bool{}
	for k := range a.Props {
		seen[k] = true
	}
	for k := range b.Props {
		seen[k] = true
	}
	if len(seen) == 0 {
		return 0
	}
	keys := slices.Sorted(maps.Keys(seen))
	var sum float64
	for _, k := range keys {
		x, okx := a.Props[k]
		y, oky := b.Props[k]
		sx, isx := x.(string)
		sy, isy := y.(string)
		switch {
		case !okx || !oky || x == nil || y == nil:
			sum += 0.5
		case isx && isy:
			sum += Text(sx, sy, m)
		case same(x, y):
			sum++
		default:
			sum += 0.5
		}
	}
	return sum / float64(len(keys))
}

// edges scores the overlap of two entities' edges. Two entities with no
// edges at all are neither alike nor unalike, so they score half; one with
// edges against one without scores zero.
func edges(a, b Entity) float64 {
	x, y := keySet(a.Edges), keySet(b.Edges)
	if len(x) == 0 && len(y) == 0 {
		return 0.5
	}
	if len(x) == 0 || len(y) == 0 {
		return 0
	}
	hit := 0
	for k := range x {
		if y[k] {
			hit++
		}
	}
	return float64(hit) / float64(len(x)+len(y)-hit)
}

func keySet(ts []semantic.Triple) map[[3]string]bool {
	out := make(map[[3]string]bool, len(ts))
	var c Canon
	for _, t := range ts {
		out[c.Key(t)] = true
	}
	return out
}

// block returns the keys an entity is compared under. Two entities are only
// scored against each other when they share one, which is what keeps the
// comparison from being every pair.
func block(e Entity) []string {
	name := fold(e.Name)
	if name == "" {
		return []string{"-"}
	}
	var keys []string
	for _, t := range strings.FieldsFunc(name, split) {
		if len([]rune(t)) <= 2 {
			continue
		}
		keys = append(keys, "t:"+string([]rune(t)[:min(4, len([]rune(t)))]))
	}
	if len(keys) == 0 {
		return []string{"n:" + name}
	}
	slices.Sort(keys)
	return slices.Compact(keys)
}
