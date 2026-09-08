package split

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/hanzoai/semantic"
)

// Embed turns texts into vectors. Semantic calls it once per document with
// every sentence in it, so an implementation is free to batch, and the context
// is the one the split was started with, so a slow model is cancellable.
type Embed func(ctx context.Context, texts []string) ([][]float32, error)

// Semantic keeps consecutive sentences together while they mean similar
// things and cuts where they stop. What counts as similar is the cosine of the
// angle between adjacent sentence vectors; Threshold is where the cut falls.
// The vectors come from Embed, which this package never supplies: no splitter
// here reaches the network on its own.
type Semantic struct {
	Embed     Embed   // required
	Threshold float64 // cut when adjacent similarity falls below this
	Size      int     // runes per chunk; zero or less lets similarity alone decide
}

// Split cuts d where the meaning of its sentences shifts. It returns an error
// if Embed is missing, fails, or answers with the wrong number of vectors.
func (s Semantic) Split(ctx context.Context, d semantic.Doc) ([]semantic.Chunk, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.Text == "" {
		return nil, nil
	}
	if s.Embed == nil {
		return nil, errors.New("split: Semantic needs an Embed")
	}
	units := sentences(d.Text)
	if len(units) < 2 {
		return chunks(d, units), nil
	}
	texts := make([]string, len(units))
	for i, u := range units {
		texts[i] = strings.TrimSpace(d.Text[u.start:u.end])
	}
	vectors, err := s.Embed(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("split: embedding %d sentences: %w", len(texts), err)
	}
	if len(vectors) != len(units) {
		return nil, fmt.Errorf("split: got %d vectors for %d sentences", len(vectors), len(units))
	}
	size := s.Size
	if size <= 0 {
		size = utf8.RuneCountInString(d.Text) + 1
	}
	p := pack{
		size: size,
		cut:  func(i int) bool { return cosine(vectors[i-1], vectors[i]) < s.Threshold },
	}
	return chunks(d, p.spans(d.Text, units)), nil
}

// cosine measures the angle between two vectors, on a scale where 1 is the
// same direction and 0 is unrelated. Vectors of different width, and vectors
// with no length, have no angle between them, and count as unrelated.
func cosine(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
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
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
