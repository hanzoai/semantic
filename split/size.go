package split

import (
	"context"
	"unicode/utf8"

	"github.com/hanzoai/semantic"
)

// Chars cuts fixed windows of runes. It is the splitter with no opinion about
// the text: it will cut mid-word and mid-sentence, but never mid-rune, and it
// is the only one whose chunk count is known before reading the document.
type Chars struct {
	Size    int // runes per chunk; zero or less returns the whole text
	Overlap int // runes repeated from the end of the previous chunk
}

// Split cuts d into windows of Size runes.
func (c Chars) Split(ctx context.Context, d semantic.Doc) ([]semantic.Chunk, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return chunks(d, c.spans(d.Text)), nil
}

func (c Chars) spans(text string) []span {
	if text == "" {
		return nil
	}
	if c.Size <= 0 {
		return []span{{0, len(text)}}
	}
	step := max(c.Size-c.Overlap, 1)
	var out []span
	for start := 0; start < len(text); {
		end := advance(text, start, c.Size)
		out = append(out, span{start, end})
		if end >= len(text) {
			break
		}
		start = advance(text, start, step)
	}
	return out
}

// Words cuts on whitespace, counting words. A word carries the space after it,
// so the chunks still join back into the document.
type Words struct {
	Size    int // words per chunk; zero or less returns the whole text
	Overlap int // words repeated from the end of the previous chunk
}

// Split cuts d into runs of Size words.
func (w Words) Split(ctx context.Context, d semantic.Doc) ([]semantic.Chunk, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return chunks(d, w.spans(d.Text)), nil
}

func (w Words) spans(text string) []span {
	if text == "" {
		return nil
	}
	if w.Size <= 0 {
		return []span{{0, len(text)}}
	}
	return pack{size: w.Size, overlap: w.Overlap, cost: one}.spans(text, words(text))
}

// Tokens fills chunks to a token budget. Counting tokens needs the tokenizer
// of whatever model the chunks are headed for, which is not something this
// package can know, so the count is a parameter; the default is an estimate.
// Words stay whole either way, so a chunk holds a few tokens fewer than Size
// rather than a fraction of a word.
type Tokens struct {
	Size    int              // tokens per chunk; zero or less returns the whole text
	Overlap int              // words repeated from the end of the previous chunk
	Count   func(string) int // tokens in a string; nil uses Estimate
}

// Split cuts d into runs of whole words worth about Size tokens.
func (t Tokens) Split(ctx context.Context, d semantic.Doc) ([]semantic.Chunk, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return chunks(d, t.spans(d.Text)), nil
}

func (t Tokens) spans(text string) []span {
	if text == "" {
		return nil
	}
	if t.Size <= 0 {
		return []span{{0, len(text)}}
	}
	count := t.Count
	if count == nil {
		count = Estimate
	}
	return pack{size: t.Size, overlap: t.Overlap, cost: count}.spans(text, words(text))
}

// Estimate guesses a token count at four characters per token, the ratio
// byte-pair tokenizers average over English prose. It is a stand-in for a real
// tokenizer, not a substitute: on code, on other scripts, and on any single
// short word it is wrong, and a caller who needs the real number should pass
// its tokenizer as Tokens.Count.
func Estimate(s string) int {
	n := utf8.RuneCountInString(s)
	if n == 0 {
		return 0
	}
	return (n + 3) / 4
}
