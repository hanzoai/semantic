package split

import (
	"context"

	"github.com/hanzoai/semantic"
)

// Markdown cuts before every heading, so a section arrives at extraction with
// the heading that says what it is about. Sections shorter than Size are
// merged; a section longer than Size is left whole rather than cut somewhere
// the document did not offer a break.
type Markdown struct {
	Size int // runes per chunk; zero or less gives one chunk per section
}

// Split cuts d at its headings.
func (m Markdown) Split(ctx context.Context, d semantic.Doc) ([]semantic.Chunk, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.Text == "" {
		return nil, nil
	}
	return chunks(d, pack{size: m.Size}.spans(d.Text, sections(d.Text))), nil
}

// Code cuts source at the top of each declaration and keeps its body with it.
// A declaration longer than Size stays whole: half a function is not a fact
// about the program.
type Code struct {
	Size int // runes per chunk; zero or less gives one chunk per declaration
}

// Split cuts d at its top-level declarations.
func (c Code) Split(ctx context.Context, d semantic.Doc) ([]semantic.Chunk, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.Text == "" {
		return nil, nil
	}
	return chunks(d, pack{size: c.Size}.spans(d.Text, blocks(d.Text))), nil
}
