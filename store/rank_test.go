package store

import "testing"

// ids reads the order of a ranked list, which is the part of a fusion that
// has to be right.
func ids(ms []Match) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}

func equal[T comparable](t *testing.T, got, want []T) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestFuse(t *testing.T) {
	a := []Match{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	b := []Match{{ID: "c"}, {ID: "a"}}

	got := Fuse(60, a, b)
	equal(t, ids(got), []string{"a", "c", "b"})

	// 1/61 + 1/62, 1/63 + 1/61, 1/62 — worked out by hand.
	eq(t, got[0].Score, 1.0/61+1.0/62)
	eq(t, got[1].Score, 1.0/63+1.0/61)
	eq(t, got[2].Score, 1.0/62)
}

func TestFuseIgnoresScores(t *testing.T) {
	// Fusion by rank is the point: a source that scores on a wild scale
	// cannot outvote one that scores small, only out-rank it.
	a := []Match{{ID: "a", Score: 1000}, {ID: "b", Score: 999}}
	b := []Match{{ID: "b", Score: 0.001}, {ID: "a", Score: 0.0009}}
	if got := ids(Fuse(60, a, b)); got[0] != "a" {
		t.Errorf("got %v, want a first: it is first in one list and second in the other", got)
	}
	one := Fuse(60, a)
	eq(t, one[0].Score, 1.0/61)
}

func TestFuseConstant(t *testing.T) {
	a := []Match{{ID: "a"}, {ID: "b"}}
	if s := Fuse(0, a)[0].Score; s != 1.0/61 {
		t.Errorf("k of zero gave %v, want the default 60 to give %v", s, 1.0/61)
	}
	if s := Fuse(1, a)[0].Score; s != 1.0/2 {
		t.Errorf("k of one gave %v, want %v", s, 0.5)
	}
}

func TestFuseKeepsMetadata(t *testing.T) {
	a := []Match{{ID: "a", Meta: map[string]any{"lang": "en"}}}
	b := []Match{{ID: "a", Meta: map[string]any{"lang": "fr"}}}
	got := Fuse(60, a, b)
	if len(got) != 1 {
		t.Fatalf("got %d results, want one: the same id is one result", len(got))
	}
	if got[0].Meta["lang"] != "en" {
		t.Errorf("metadata came from %v, want the first list", got[0].Meta)
	}
}

func TestBlend(t *testing.T) {
	a := []Match{{ID: "a", Score: 1.0}, {ID: "b", Score: 0.5}}
	b := []Match{{ID: "b", Score: 0.8}, {ID: "c", Score: 0.2}}

	got := Blend([]float64{0.7, 0.3}, a, b)
	equal(t, ids(got), []string{"a", "b", "c"})
	eq(t, got[0].Score, 0.7)             // 1.0 * 0.7
	eq(t, got[1].Score, 0.5*0.7+0.8*0.3) // 0.59
	eq(t, got[2].Score, 0.2*0.3)         // 0.06
}

func TestBlendWithoutWeights(t *testing.T) {
	a := []Match{{ID: "a", Score: 1.0}, {ID: "b", Score: 0.5}}
	b := []Match{{ID: "b", Score: 0.8}, {ID: "c", Score: 0.2}}

	got := Blend(nil, a, b)
	equal(t, ids(got), []string{"b", "a", "c"})
	eq(t, got[0].Score, 0.5*0.5+0.8*0.5) // 0.65
	eq(t, got[1].Score, 0.5)
	eq(t, got[2].Score, 0.1)
}

func TestMergeEmpty(t *testing.T) {
	if got := Fuse(60); len(got) != 0 {
		t.Errorf("fusing nothing gave %v, want nothing", got)
	}
	if got := Blend(nil); len(got) != 0 {
		t.Errorf("blending nothing gave %v, want nothing", got)
	}
	if got := Fuse(60, nil, []Match{}); len(got) != 0 {
		t.Errorf("fusing empty lists gave %v, want nothing", got)
	}
}

func TestTopOrdersAndTrims(t *testing.T) {
	ms := []Match{{ID: "c", Score: 1}, {ID: "a", Score: 3}, {ID: "b", Score: 2}}
	equal(t, ids(top(append([]Match(nil), ms...), 2)), []string{"a", "b"})
	equal(t, ids(top(append([]Match(nil), ms...), 0)), []string{"a", "b", "c"})
	equal(t, ids(top(append([]Match(nil), ms...), 99)), []string{"a", "b", "c"})
}

func TestTopBreaksTiesByID(t *testing.T) {
	ms := []Match{{ID: "z", Score: 1}, {ID: "a", Score: 1}, {ID: "m", Score: 1}}
	equal(t, ids(top(ms, 3)), []string{"a", "m", "z"})
}
