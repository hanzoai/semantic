package store

import (
	"math"
	"testing"
)

const tol = 1e-9

func eq(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("got %.17g, want %.17g", got, want)
	}
}

func TestDot(t *testing.T) {
	for _, c := range []struct {
		name string
		a, b []float32
		want float64
	}{
		{"orthogonal", []float32{1, 0}, []float32{0, 1}, 0},
		{"same", []float32{1, 2, 3}, []float32{4, 5, 6}, 32},
		{"opposed", []float32{1, 2}, []float32{-1, -2}, -5},
		{"empty", nil, nil, 0},
		{"short second is zero padded", []float32{1, 2}, []float32{3}, 3},
		{"short first is zero padded", []float32{3}, []float32{1, 2}, 3},
	} {
		t.Run(c.name, func(t *testing.T) { eq(t, Dot(c.a, c.b), c.want) })
	}
}

func TestNorm(t *testing.T) {
	for _, c := range []struct {
		name string
		v    []float32
		want float64
	}{
		{"three four five", []float32{3, 4}, 5},
		{"unit", []float32{1, 0, 0}, 1},
		{"zero", []float32{0, 0}, 0},
		{"empty", nil, 0},
		{"negative", []float32{-3, -4}, 5},
	} {
		t.Run(c.name, func(t *testing.T) { eq(t, Norm(c.v), c.want) })
	}
}

func TestUnit(t *testing.T) {
	got := Unit([]float32{3, 4})
	want := []float32{0.6, 0.8}
	for i := range want {
		eq(t, float64(got[i]), float64(want[i]))
	}
	// A unit vector held in float32 is unit to float32 precision.
	if n := Norm(got); math.Abs(n-1) > 1e-6 {
		t.Errorf("length is %v, want 1", n)
	}

	zero := Unit([]float32{0, 0})
	if len(zero) != 2 || zero[0] != 0 || zero[1] != 0 {
		t.Errorf("zero vector became %v, want it left alone", zero)
	}

	v := []float32{3, 4}
	Unit(v)
	if v[0] != 3 || v[1] != 4 {
		t.Errorf("Unit changed its argument: %v", v)
	}
}

func TestCos(t *testing.T) {
	// 32 / (sqrt(14) * sqrt(77)), computed by hand.
	const skew = 0.9746318461970762
	for _, c := range []struct {
		name string
		a, b []float32
		want float64
	}{
		{"identical", []float32{1, 0}, []float32{1, 0}, 1},
		{"orthogonal", []float32{1, 0}, []float32{0, 1}, 0},
		{"opposed", []float32{1, 0}, []float32{-1, 0}, -1},
		{"length ignored", []float32{1, 1}, []float32{5, 5}, 1},
		{"skew", []float32{1, 2, 3}, []float32{4, 5, 6}, skew},
		{"zero has no direction", []float32{0, 0}, []float32{1, 1}, 0},
	} {
		t.Run(c.name, func(t *testing.T) { eq(t, Cos(c.a, c.b), c.want) })
	}
}

func TestDist(t *testing.T) {
	for _, c := range []struct {
		name string
		a, b []float32
		want float64
	}{
		{"same point", []float32{1, 2}, []float32{1, 2}, 0},
		{"three four five", []float32{1, 2}, []float32{4, 6}, 5},
		{"one axis", []float32{0}, []float32{2}, 2},
		{"short is zero padded", []float32{3}, []float32{0, 4}, 5},
	} {
		t.Run(c.name, func(t *testing.T) { eq(t, Dist(c.a, c.b), c.want) })
	}
}

func TestMetricScore(t *testing.T) {
	a, b := []float32{1, 2}, []float32{4, 6}
	for _, c := range []struct {
		metric Metric
		want   float64
	}{
		{Cosine, Cos(a, b)},
		{Inner, 16},
		{Euclid, -5},
		{"", Cos(a, b)},
		{"nonsense", Cos(a, b)},
	} {
		t.Run(string(c.metric), func(t *testing.T) { eq(t, c.metric.Score(a, b), c.want) })
	}
}

func TestMetricOrdersNearestFirst(t *testing.T) {
	q := []float32{1, 0}
	near, far := []float32{1, 0.1}, []float32{0, 1}
	for _, m := range []Metric{Cosine, Inner, Euclid} {
		if m.Score(q, near) <= m.Score(q, far) {
			t.Errorf("%s scored the far vector at least as near", m)
		}
	}
}
