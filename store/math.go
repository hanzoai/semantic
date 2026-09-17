package store

import "math"

// Metric names how nearness is measured. The zero value is Cosine, so a
// store that states no metric compares directions.
type Metric string

const (
	// Cosine is the angle between two vectors: 1 when they point the same
	// way, 0 when they are orthogonal, -1 when opposed. Length is ignored.
	Cosine Metric = "cosine"
	// Inner is the dot product, so length counts as well as direction.
	Inner Metric = "inner"
	// Euclid is straight-line distance, negated so that larger still means
	// nearer and one comparison orders every metric.
	Euclid Metric = "euclid"
)

// Score reports how near b is to a. Larger is nearer under every metric, so
// callers rank the same way whichever one is in use. An unknown metric
// scores as Cosine.
func (m Metric) Score(a, b []float32) float64 {
	switch m {
	case Inner:
		return Dot(a, b)
	case Euclid:
		return -Dist(a, b)
	default:
		return Cos(a, b)
	}
}

// Vectors of different lengths are compared as though the shorter were
// padded with zeros, which leaves Dot and Norm unchanged and keeps every
// function here total. A store that wants lengths to agree says so itself;
// Mem does, and reports ErrDim.

// Dot is the inner product of two vectors.
func Dot(a, b []float32) float64 {
	n := min(len(a), len(b))
	var s float64
	for i := range n {
		s += float64(a[i]) * float64(b[i])
	}
	return s
}

// Norm is the Euclidean length of a vector.
func Norm(v []float32) float64 {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	return math.Sqrt(s)
}

// Unit returns v scaled to length 1, as a new slice. A zero vector has no
// direction, so it is returned unchanged rather than turned into NaNs.
func Unit(v []float32) []float32 {
	n := Norm(v)
	out := make([]float32, len(v))
	if n == 0 {
		copy(out, v)
		return out
	}
	for i, x := range v {
		out[i] = float32(float64(x) / n)
	}
	return out
}

// Cos is the cosine of the angle between two vectors. A zero vector points
// nowhere, so it scores 0 against everything rather than dividing by zero.
func Cos(a, b []float32) float64 {
	na, nb := Norm(a), Norm(b)
	if na == 0 || nb == 0 {
		return 0
	}
	return Dot(a, b) / (na * nb)
}

// Dist is the Euclidean distance between two vectors.
func Dist(a, b []float32) float64 {
	n := min(len(a), len(b))
	var s float64
	for i := range n {
		d := float64(a[i]) - float64(b[i])
		s += d * d
	}
	for _, x := range a[n:] {
		s += float64(x) * float64(x)
	}
	for _, x := range b[n:] {
		s += float64(x) * float64(x)
	}
	return math.Sqrt(s)
}
