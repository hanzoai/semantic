// Package resonance adds experimental phase-aware scoring to semantic search.
// Existing Vector, Graph, and Triple stores are unchanged. The implementation
// uses only the standard library and is independent of ResonanceDB's source.
//
// Converting a real embedding to complex coordinates adds no semantic polarity:
// callers must supply evidence-grounded operator metadata. This package does
// not infer negation, truth, scope, or time from embeddings.
package resonance

import (
    "errors"
    "math"
    "sort"
)

var (
    ErrDim = errors.New("resonance: vector dimensions differ or are empty")
    ErrFinite = errors.New("resonance: invalid non-finite number")
    ErrAmplitude = errors.New("resonance: negative amplitude")
    ErrWeight = errors.New("resonance: weight must be in [0, 1]")
    ErrCosine = errors.New("resonance: baseline cosine must be in [-1, 1]")
)

// Wave is a complex feature vector. Inputs are copied so callers cannot
// mutate the waveform after it has been validated.
type Wave struct {
    real, imag []float32
    energy float64
}

// Embed wraps an existing real embedding, with zero imaginary components.
// Without independently meaningful phase data, this adds no reasoning ability.
func Embed(v []float32) (Wave, error) {
    return Cartesian(v, make([]float32, len(v)))
}

// Cartesian constructs a wave from explicit real and imaginary features.
func Cartesian(real, imag []float32) (Wave, error) {
    if len(real) == 0 || len(real) != len(imag) { return Wave{}, ErrDim }
    w := Wave{real: append([]float32(nil), real...), imag: append([]float32(nil), imag...)}
    for i, v := range w.real {
        im := w.imag[i]
        if !finite(float64(v)) || !finite(float64(im)) { return Wave{}, ErrFinite }
        w.energy += float64(v)*float64(v) + float64(im)*float64(im)
    }
    return w, nil
}

// Polar constructs complex features from nonnegative magnitudes and
// explicitly supplied phases (radians).
func Polar(magnitude, phase []float32) (Wave, error) {
    if len(magnitude) == 0 || len(magnitude) != len(phase) { return Wave{}, ErrDim }
    re, im := make([]float32, len(magnitude)), make([]float32, len(magnitude))
    for i, a := range magnitude {
        theta := phase[i]
        if !finite(float64(a)) || !finite(float64(theta)) { return Wave{}, ErrFinite }
        if a < 0 { return Wave{}, ErrAmplitude }
        re[i], im[i] = float32(float64(a)*math.Cos(float64(theta))), float32(float64(a)*math.Sin(float64(theta)))
    }
    return Cartesian(re, im)
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// Dim reports the number of complex coordinates.
func (w Wave) Dim() int { return len(w.real) }

// Energy reports the sum of squared magnitudes.
func (w Wave) Energy() float64 { return w.energy }

// Negate applies a pi phase shift to all features. It is appropriate only
// when the caller has established the same proposition and scope with
// logically opposing polarity, never for arbitrary paragraphs.
func (w Wave) Negate() Wave {
    re, im := make([]float32, len(w.real)), make([]float32, len(w.imag))
    for i := range re { re[i], im[i] = -w.real[i], -w.imag[i] }
    return Wave{real: re, imag: im, energy: w.energy}
}

// Rotate applies a common phase rotation in radians.
func (w Wave) Rotate(angle float32) (Wave, error) {
    if !finite(float64(angle)) { return Wave{}, ErrFinite }
    s, c := math.Sincos(float64(angle))
    re, im := make([]float32, len(w.real)), make([]float32, len(w.imag))
    for i := range re {
        re[i] = float32(float64(w.real[i])*c - float64(w.imag[i])*s)
        im[i] = float32(float64(w.real[i])*s + float64(w.imag[i])*c)
    }
    return Cartesian(re, im)
}

// Score computes normalized interference in [0,1]. It accounts for energy
// balance and costs O(dimensions) per comparison. Equal-norm real vectors
// satisfy Score = (1 + cosine)/2. It does not replace approximate-nearest
// neighbour indexing or perform symbolic inference.
func Score(a, b Wave) (float64, error) {
    if a.Dim() == 0 || a.Dim() != b.Dim() { return 0, ErrDim }
    if a.energy == 0 || b.energy == 0 { return 0, nil }
    dot := 0.0
    for i := range a.real {
        dot += float64(a.real[i])*float64(b.real[i]) + float64(a.imag[i])*float64(b.imag[i])
    }
    total := a.energy + b.energy
    interference := (total + 2*dot)/total
    balance := 2*math.Sqrt(a.energy*b.energy)/total
    return math.Max(0, math.Min(1, 0.5*interference*balance)), nil
}

// Candidate is an existing retrieval result together with its explicit phase
// representation. Cosine is the original embedding similarity, not a
// comparison of the phase-transformed pattern.
type Candidate struct {
    ID string
    Wave Wave
    Cosine float64
}

// Hit preserves both component scores for tracing and controlled ablations.
type Hit struct {
    ID string
    Score float64
    Resonance float64
    Cosine float64
}

// Rank fuses normalized cosine with resonance over an already retrieved
// shortlist. ACL, subject, namespace and time filters must run BEFORE Rank;
// reranking cannot retrieve items the original shortlist missed.
func Rank(query Wave, candidates []Candidate, weight float64, k int) ([]Hit, error) {
    if !finite(weight) || weight < 0 || weight > 1 { return nil, ErrWeight }
    hits := make([]Hit, 0, len(candidates))
    for _, c := range candidates {
        if !finite(c.Cosine) || c.Cosine < -1 || c.Cosine > 1 { return nil, ErrCosine }
        resonance, err := Score(query, c.Wave)
        if err != nil { return nil, err }
        base := (c.Cosine + 1)/2
        hits = append(hits, Hit{ID:c.ID, Score:weight*resonance+(1-weight)*base, Resonance:resonance, Cosine:c.Cosine})
    }
    sort.Slice(hits, func(i,j int) bool {
        if hits[i].Score != hits[j].Score { return hits[i].Score > hits[j].Score }
        return hits[i].ID < hits[j].ID
    })
    if k > 0 && k < len(hits) { hits = hits[:k] }
    return hits, nil
}
