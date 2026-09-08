package dedupe

import (
	"math"
	"slices"
	"testing"

	"github.com/hanzoai/semantic"
)

func near(t *testing.T, got, want, tol float64, what string) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %.4f, want %.4f ±%g", what, got, want, tol)
	}
}

func TestTextMetrics(t *testing.T) {
	for _, c := range []struct {
		a, b string
		m    Metric
		want float64
		tol  float64
	}{
		// Equality folds case and surrounding space, whatever the metric.
		{"Apple", "apple", Exact, 1, 0},
		{" Apple ", "apple", Edit, 1, 0},
		{"Apple", "Apple", Jaro, 1, 0},
		// Exact says nothing about anything else.
		{"Apple", "Apple Inc.", Exact, 0, 0},
		// Levenshtein: 5 edits over 10 characters.
		{"apple", "apple inc.", Edit, 0.5, 0.0001},
		{"kitten", "sitting", Edit, 1 - 3.0/7, 0.0001},
		// Jaro-Winkler on the textbook pair.
		{"martha", "marhta", Jaro, 0.961, 0.001},
		{"dwayne", "duane", Jaro, 0.84, 0.01},
		// Bigrams: "ab" and "ba" share none.
		{"ab", "ba", Gram, 0, 0},
		{"abc", "abd", Gram, 1.0 / 3, 0.0001},
		// Words, in any order.
		{"works at", "at works", Token, 1, 0},
		{"ada lovelace", "ada byron lovelace", Token, 2.0 / 3, 0.0001},
		{"works_at", "works at", Token, 1, 0},
		// An empty string is like nothing, including another empty string.
		{"", "apple", Jaro, 0, 0},
		{"apple", "", Edit, 0, 0},
		{"", "", Gram, 0, 0},
	} {
		near(t, Text(c.a, c.b, c.m), c.want, c.tol, "Text("+c.a+","+c.b+","+string(c.m)+")")
	}
}

func TestTextDefaultsToJaro(t *testing.T) {
	if got, want := Text("martha", "marhta", ""), Text("martha", "marhta", Jaro); got != want {
		t.Errorf("zero metric = %v, want the Jaro score %v", got, want)
	}
}

func TestCosine(t *testing.T) {
	for _, c := range []struct {
		name string
		a, b []float32
		want float64
	}{
		{"same direction scores 1", []float32{1, 0}, []float32{2, 0}, 1},
		{"opposed scores 0", []float32{1, 0}, []float32{-1, 0}, 0},
		{"right angle scores a half", []float32{1, 0}, []float32{0, 1}, 0.5},
		{"different lengths score 0", []float32{1, 0}, []float32{1}, 0},
		{"no vector scores 0", nil, nil, 0},
		{"a zero vector scores 0", []float32{0, 0}, []float32{1, 1}, 0},
	} {
		if got := Cosine(c.a, c.b); math.Abs(got-c.want) > 0.0001 {
			t.Errorf("%s: Cosine = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestLikenessWeighsOnlyTheEvidenceThatExists(t *testing.T) {
	a := Entity{Name: "Apple Inc."}
	b := Entity{Name: "Apple"}
	s := Likeness(a, b, Weights{}, Jaro)
	if _, ok := s.Parts["vector"]; ok {
		t.Error("records with no vectors got a vector part")
	}
	for _, p := range [...]string{"name", "props", "edges"} {
		if _, ok := s.Parts[p]; !ok {
			t.Errorf("missing %q part", p)
		}
	}
	if s.Total <= 0 || s.Total > 1 {
		t.Errorf("total %v outside 0 to 1", s.Total)
	}
}

func TestLikenessCountsVectorsWhenBothCarryOne(t *testing.T) {
	w := Weights{Name: 0.5, Vector: 0.5}
	a := Entity{Name: "Apple", Vector: []float32{1, 0}}
	b := Entity{Name: "Microsoft", Vector: []float32{1, 0}}
	with := Likeness(a, b, w, Jaro)
	if _, ok := with.Parts["vector"]; !ok {
		t.Fatal("vectors present but not weighed")
	}
	b.Vector = nil
	without := Likeness(a, b, w, Jaro)
	if with.Total <= without.Total {
		t.Errorf("matching vectors did not raise likeness: %v then %v", with.Total, without.Total)
	}
}

func TestLikenessPartsUnstatedEvidenceIsNeutral(t *testing.T) {
	// Properties: one record stating a property the other does not is neither
	// agreement nor disagreement, and scores a half.
	a := Entity{Props: map[string]any{"x": 1}}
	b := Entity{Props: map[string]any{}}
	near(t, props(a, b, Jaro), 0.5, 0.0001, "props one-sided")
	near(t, props(Entity{}, Entity{}, Jaro), 1, 0, "props both empty")
	near(t, props(a, a, Jaro), 1, 0, "props identical")

	// Edges: no edges at all is neutral, edges against none is not.
	e := Entity{Edges: []semantic.Triple{{Subject: "a", Predicate: "p", Object: "o"}}}
	near(t, edges(Entity{}, Entity{}), 0.5, 0, "edges both empty")
	near(t, edges(e, Entity{}), 0, 0, "edges one-sided")
	near(t, edges(e, e), 1, 0, "edges identical")
}

func TestCanonFoldsPredicateAndObject(t *testing.T) {
	c := Canon{Synonyms: map[string]string{"employed by": "works at"}, Fold: true}
	x := c.Key(semantic.Triple{Subject: "Ada", Predicate: "Employed By", Object: "  ACME  Corp "})
	y := c.Key(semantic.Triple{Subject: "Ada", Predicate: "WORKS AT", Object: "acme corp"})
	if x != y {
		t.Errorf("synonyms did not fold together: %v then %v", x, y)
	}
	var plain Canon
	if k := plain.Key(semantic.Triple{Subject: "Ada", Predicate: "WORKS AT", Object: "ACME"}); k[2] != "ACME" {
		t.Errorf("the zero Canon folded the object to %q", k[2])
	}
}

func TestBlockGroupsNamesThatCouldBeTheSame(t *testing.T) {
	shared := func(a, b string) bool {
		x, y := block(Entity{Name: a}), block(Entity{Name: b})
		for _, k := range x {
			if slices.Contains(y, k) {
				return true
			}
		}
		return false
	}
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"Apple Inc.", "Apple", true},
		{"Apple", "apple", true},
		{"Ada Lovelace", "Lovelace Ada", true},
		{"Apple Inc.", "Microsoft Corp", false},
		{"", "", true}, // nameless records are compared with each other
	} {
		if got := shared(c.a, c.b); got != c.want {
			t.Errorf("blocks of %q and %q meet = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
