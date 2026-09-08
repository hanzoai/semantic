// Package split cuts a document into chunks.
//
// Every splitter here works the same way. It finds the boundaries it is
// allowed to cut on — runes, words, sentences, paragraphs, headings, code
// blocks — and then merges neighbouring pieces until one more would pass
// Size. The pieces tile the text, so with Overlap zero the chunks of a
// document concatenate back into the document exactly, byte for byte, and a
// chunk boundary never falls inside a rune.
//
// Size is measured in the splitter's own unit: runes for [Chars], words for
// [Words], tokens for [Tokens], runes for the rest. Size and Max both bound a
// chunk; with neither set nothing is merged, so [Sentences] gives a chunk per
// sentence and [Paragraphs] a chunk per paragraph. Chars, Words and Tokens
// have no unit boundary to fall back on, so with Size unset they return the
// whole text as one chunk.
//
// Overlap repeats that many trailing units at the head of the next chunk. A
// chunk always advances by at least one unit, so an Overlap at or above Size
// costs repetition, not progress.
package split

import (
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/hanzoai/semantic"
)

// Every splitter in this package satisfies the pipeline's Split stage.
var (
	_ semantic.Splitter = Chars{}
	_ semantic.Splitter = Words{}
	_ semantic.Splitter = Tokens{}
	_ semantic.Splitter = Sentences{}
	_ semantic.Splitter = Paragraphs{}
	_ semantic.Splitter = Recursive{}
	_ semantic.Splitter = Markdown{}
	_ semantic.Splitter = Code{}
	_ semantic.Splitter = Semantic{}
	_ semantic.Splitter = Or{}
)

// span is a half-open byte range of a document's text. A scanner returns
// spans that tile the text — contiguous, in order, covering every byte — which
// is what makes the round trip hold for anything built on top of them.
type span struct{ start, end int }

// chunks turns source ranges into the pipeline's value, numbering them from
// zero so a chunk is identified by its document and its position in it.
func chunks(d semantic.Doc, spans []span) []semantic.Chunk {
	if len(spans) == 0 {
		return nil
	}
	out := make([]semantic.Chunk, len(spans))
	for i, s := range spans {
		out[i] = semantic.Chunk{DocID: d.ID, Index: i, Text: d.Text[s.start:s.end]}
	}
	return out
}

// pack merges units into chunks. size and max bound a chunk from above and
// cut forces a boundary; none of the three can make a chunk hold less than one
// unit, so a unit larger than size gets a chunk to itself rather than being
// cut somewhere its splitter is not allowed to cut.
type pack struct {
	size    int              // largest chunk, in whatever cost counts; 0 is unbounded
	max     int              // most units per chunk; 0 is unbounded
	overlap int              // trailing units repeated in the next chunk
	cost    func(string) int // size of one unit; nil counts runes
	cut     func(i int) bool // reports a forced boundary before unit i
}

func (p pack) spans(text string, units []span) []span {
	if len(units) == 0 {
		return nil
	}
	cost := p.cost
	if cost == nil {
		cost = utf8.RuneCountInString
	}
	var out []span
	for i := 0; i < len(units); {
		total, j := 0, i
		for j < len(units) {
			c := cost(text[units[j].start:units[j].end])
			if j > i {
				if p.size <= 0 && p.max <= 0 {
					break // nothing asks for merging
				}
				if p.max > 0 && j-i >= p.max {
					break
				}
				if p.cut != nil && p.cut(j) {
					break
				}
				if p.size > 0 && total+c > p.size {
					break
				}
			}
			total += c
			j++
		}
		out = append(out, span{units[i].start, units[j-1].end})
		if j >= len(units) {
			break
		}
		if next := j - p.overlap; next > i {
			i = next
		} else {
			i++
		}
	}
	return out
}

// advance returns the byte offset n runes past i, stopping at the end of text.
func advance(text string, i, n int) int {
	for ; n > 0 && i < len(text); n-- {
		_, w := utf8.DecodeRuneInString(text[i:])
		i += w
	}
	return i
}

// one is the cost of a unit counted by unit.
func one(string) int { return 1 }

// Or tries each splitter in turn and returns the first result that is neither
// an error nor empty. It is how a splitter that needs a model falls back to
// one that needs nothing: Or{Semantic{Embed: e}, Sentences{Size: 1000}}.
type Or []semantic.Splitter

// Split returns the chunks of the first splitter that produces any.
func (o Or) Split(ctx context.Context, d semantic.Doc) ([]semantic.Chunk, error) {
	var last error
	for _, s := range o {
		if s == nil {
			continue
		}
		c, err := s.Split(ctx, d)
		switch {
		case err == nil && len(c) > 0:
			return c, nil
		case err == nil:
			continue
		case ctx.Err() != nil:
			return nil, err
		default:
			last = err
		}
	}
	if last != nil {
		return nil, fmt.Errorf("split: no splitter produced chunks: %w", last)
	}
	return nil, nil
}
