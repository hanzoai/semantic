package split

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/hanzoai/semantic"
)

// Sentences fills chunks with whole sentences. With neither Size nor Max set
// each sentence becomes a chunk.
type Sentences struct {
	Size    int // runes per chunk; zero or less is unbounded
	Max     int // sentences per chunk; zero or less is unbounded
	Overlap int // sentences repeated from the end of the previous chunk
}

// Split cuts d on sentence boundaries.
func (s Sentences) Split(ctx context.Context, d semantic.Doc) ([]semantic.Chunk, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.Text == "" {
		return nil, nil
	}
	p := pack{size: s.Size, max: s.Max, overlap: s.Overlap}
	return chunks(d, p.spans(d.Text, sentences(d.Text))), nil
}

// Paragraphs fills chunks with whole paragraphs, a paragraph being what lies
// between two blank lines. With Size unset each paragraph becomes a chunk.
type Paragraphs struct {
	Size    int // runes per chunk; zero or less is unbounded
	Overlap int // paragraphs repeated from the end of the previous chunk
}

// Split cuts d on blank lines.
func (p Paragraphs) Split(ctx context.Context, d semantic.Doc) ([]semantic.Chunk, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.Text == "" {
		return nil, nil
	}
	return chunks(d, pack{size: p.Size, overlap: p.Overlap}.spans(d.Text, paragraphs(d.Text))), nil
}

// Recursive cuts on the first separator that gets a piece under Size and works
// down the list for pieces that are still too big, so a document breaks at
// paragraphs where it can, at lines where it cannot, and at sentences, words
// and finally runes as the text resists. The resulting pieces are then merged
// back up to Size, which is what keeps a run of short paragraphs from becoming
// a run of tiny chunks.
type Recursive struct {
	Size       int      // runes per chunk; zero or less returns the whole text
	Overlap    int      // pieces repeated from the end of the previous chunk
	Separators []string // tried in order; nil means blank line, line, sentence, word, rune
}

// Split cuts d at the coarsest separator that fits.
func (r Recursive) Split(ctx context.Context, d semantic.Doc) ([]semantic.Chunk, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.Text == "" {
		return nil, nil
	}
	if r.Size <= 0 {
		return chunks(d, []span{{0, len(d.Text)}}), nil
	}
	seps := r.Separators
	if seps == nil {
		seps = separators()
	}
	pieces := divide(d.Text, span{0, len(d.Text)}, seps, r.Size)
	return chunks(d, pack{size: r.Size, overlap: r.Overlap}.spans(d.Text, pieces)), nil
}

// separators returns the default hierarchy, freshly, so that a caller who
// keeps the slice and edits it cannot change what the next call does.
func separators() []string { return []string{"\n\n", "\n", ". ", " ", ""} }

// divide cuts s down until every piece fits in size or the separators run out.
// The separator stays on the piece it ends, so the pieces still tile s.
func divide(text string, s span, seps []string, size int) []span {
	if utf8.RuneCountInString(text[s.start:s.end]) <= size || len(seps) == 0 {
		return []span{s}
	}
	sep, rest := seps[0], seps[1:]
	if sep == "" {
		var out []span
		for i := s.start; i < s.end; {
			j := advance(text, i, size)
			if j > s.end {
				j = s.end
			}
			out = append(out, span{i, j})
			i = j
		}
		return out
	}
	var pieces []span
	for i := s.start; i < s.end; {
		k := strings.Index(text[i:s.end], sep)
		if k < 0 {
			pieces = append(pieces, span{i, s.end})
			break
		}
		end := i + k + len(sep)
		pieces = append(pieces, span{i, end})
		i = end
	}
	if len(pieces) <= 1 {
		return divide(text, s, rest, size)
	}
	var out []span
	for _, p := range pieces {
		out = append(out, divide(text, p, rest, size)...)
	}
	return out
}
