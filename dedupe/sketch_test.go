package dedupe

import (
	"math"
	"testing"
)

func TestHashIsStableAndFolds(t *testing.T) {
	if Hash("Ada Lovelace") != Hash("ada  lovelace") {
		t.Error("the same words spelled differently hashed differently")
	}
	if Hash("Ada Lovelace") != Hash("Lovelace Ada") {
		t.Error("a word set hashed differently in another order")
	}
	if Hash("Ada Lovelace") == Hash("Charles Babbage") {
		t.Error("two unrelated texts hashed alike")
	}
	if Near(Hash("Ada"), Hash("Ada")) != 1 {
		t.Error("a text is not near itself")
	}
}

func TestNearFallsAsTextsDiverge(t *testing.T) {
	base := Hash("the quick brown fox jumps over the lazy dog")
	one := Hash("the quick brown fox jumps over the lazy cat")
	far := Hash("entirely unrelated words about shipping containers")
	if !(Near(base, one) > Near(base, far)) {
		t.Errorf("a one-word change scored %v, an unrelated text %v", Near(base, one), Near(base, far))
	}
	if Near(base, one) < 0.7 {
		t.Errorf("a one-word change scored %v, want most bits still agreeing", Near(base, one))
	}
}

func TestSignEstimatesTheOverlapOfWords(t *testing.T) {
	for _, c := range []struct {
		name string
		a, b string
		want float64
	}{
		{"the same words", "ada lovelace byron", "byron ada lovelace", 1},
		{"two words of three shared", "ada lovelace byron", "ada lovelace babbage", 0.5},
		{"nothing shared", "ada lovelace", "shipping containers", 0},
		{"both empty", "", "", 1},
	} {
		// 1024 hashes, so the estimate is within a few percent of the truth.
		got := Sign(c.a, 1024).Like(Sign(c.b, 1024))
		if math.Abs(got-c.want) > 0.06 {
			t.Errorf("%s: estimate %.3f, want %.3f", c.name, got, c.want)
		}
	}
}

func TestSignAgreesWithTheJaccardItEstimates(t *testing.T) {
	a := "ada augusta byron king countess of lovelace"
	b := "augusta ada king lovelace countess"
	exact := Text(a, b, Token)
	got := Sign(a, 2048).Like(Sign(b, 2048))
	if math.Abs(got-exact) > 0.04 {
		t.Errorf("sketch estimate %.3f against the exact overlap %.3f", got, exact)
	}
}

func TestSignWidth(t *testing.T) {
	if got := len(Sign("ada", 0)); got != 64 {
		t.Errorf("width 0 gave %d hashes, want the default 64", got)
	}
	if got := len(Sign("ada", -5)); got != 64 {
		t.Errorf("a negative width gave %d hashes, want the default 64", got)
	}
	if got := len(Sign("ada", 8)); got != 8 {
		t.Errorf("width 8 gave %d hashes", got)
	}
	if got := Sign("ada", 8).Like(Sign("ada", 16)); got != 0 {
		t.Errorf("sketches of different widths scored %v, want 0", got)
	}
	if got := Sketch(nil).Like(Sketch(nil)); got != 0 {
		t.Errorf("an absent sketch scored %v, want 0", got)
	}
}

func TestSignIsRepeatable(t *testing.T) {
	first := Sign("ada lovelace byron", 32)
	for range 5 {
		next := Sign("ada  LOVELACE   byron", 32)
		for j := range first {
			if first[j] != next[j] {
				t.Fatalf("hash %d moved between runs: %d then %d", j, first[j], next[j])
			}
		}
	}
}
