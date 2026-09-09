// Command split cuts one document nine ways and shows what each splitter is
// for. Every splitter finds the boundaries it is allowed to cut on — runes,
// words, tokens, sentences, paragraphs, headings, declarations — and then
// merges neighbours until one more would pass Size.
//
// The property they share is worth checking rather than believing: with no
// overlap the chunks tile the document, so concatenating them gives the text
// back byte for byte. The run below asserts that for every splitter.
//
//	go run ./examples/split
package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/hanzoai/semantic"
	"github.com/hanzoai/semantic/split"
)

const notes = `# Babbage Engines

The company was founded by Charles Babbage in 1843. It builds difference
engines. Ada Lovelace joined as an analyst in the same year.

## Montréal

The mill was finished in 1849. The office moved to Montréal in 1852, which
is where the assembly floor is today.

## Ledger

Revenue reached $1,200,000 in 1855. Costs were 40% of that.
`

func main() {
	ctx := context.Background()
	d := semantic.Doc{ID: "notes", Source: "notes.md", Text: notes}

	fmt.Printf("document  %d bytes, %d runes\n\n", len(notes), len([]rune(notes)))
	fmt.Printf("%-28s %6s %7s %7s  %s\n", "splitter", "chunks", "longest", "tiles", "first chunk")

	for _, s := range []struct {
		name string
		of   semantic.Splitter
	}{
		// Fixed windows of runes. No opinion about the text: it will cut
		// mid-word, never mid-rune, and it is the only splitter whose chunk
		// count is known before the document is read.
		{"Chars{Size: 200}", split.Chars{Size: 200}},

		// Whole words, then whole sentences, then whole paragraphs. Each
		// refuses to cut inside its own unit.
		{"Words{Size: 40}", split.Words{Size: 40}},
		{"Sentences{Max: 1}", split.Sentences{Max: 1}},
		{"Sentences{Size: 200}", split.Sentences{Size: 200}},
		{"Paragraphs{}", split.Paragraphs{}},

		// Tokens, counted the cheap way at four characters each. A caller
		// with a real tokenizer passes it as Count and the arithmetic is the
		// same.
		{"Tokens{Size: 60}", split.Tokens{Size: 60}},

		// Down the separator list until a piece fits: paragraphs where it
		// can, then lines, sentences, words and finally runes.
		{"Recursive{Size: 200}", split.Recursive{Size: 200}},

		// Cut before every heading, so a section reaches extraction with the
		// heading that says what it is about.
		{"Markdown{}", split.Markdown{}},
	} {
		chunks, err := s.of.Split(ctx, d)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%-28s %6d %7d %7v  %s\n",
			s.name, len(chunks), longest(chunks), tiles(chunks, d.Text), first(chunks))
	}

	overlap(ctx, d)
	tokens(ctx, d)
	code(ctx)
	meaning(ctx, d)
	byName(ctx, d)
}

// overlap repeats trailing units at the head of the next chunk, so a sentence
// split across two chunks is whole in one of them. Overlapping chunks no
// longer tile: that is the trade being made.
func overlap(ctx context.Context, d semantic.Doc) {
	fmt.Println("\noverlap")
	for _, n := range []int{0, 20, 60} {
		chunks, err := split.Chars{Size: 200, Overlap: n}.Split(ctx, d)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("  Chars{Size: 200, Overlap: %2d}  %d chunks, tiles: %v\n",
			n, len(chunks), tiles(chunks, d.Text))
	}
}

// tokens is the seam for a real tokenizer. Estimate guesses four characters to
// a token, which is close on English prose and wrong on code and on any other
// script; a caller that needs the real number passes its own counter.
func tokens(ctx context.Context, d semantic.Doc) {
	fmt.Println("\ntokens")
	fmt.Printf("  Estimate says %d tokens for the document\n", split.Estimate(d.Text))

	// A stand-in for a tokenizer: whitespace-separated words. A real one is
	// the same shape, so swapping it in is one field.
	words := func(s string) int { return len(strings.Fields(s)) }
	a, err := split.Tokens{Size: 25}.Split(ctx, d)
	if err != nil {
		log.Fatal(err)
	}
	b, err := split.Tokens{Size: 25, Count: words}.Split(ctx, d)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  Tokens{Size: 25}: %d chunks with the estimate, %d with the word counter — same code, different unit\n",
		len(a), len(b))
}

// code cuts source at the top of each declaration and keeps the body with it.
// A declaration longer than Size stays whole: half a function is not a fact
// about the program.
func code(ctx context.Context) {
	const src = `package mill

// Rows is how many rows the mill holds.
const Rows = 40

func Turn(n int) int {
	total := 0
	for i := 0; i < n; i++ {
		total += i
	}
	return total
}

func Reset() {}
`
	chunks, err := split.Code{}.Split(ctx, semantic.Doc{ID: "mill", Source: "mill.go", Text: src})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\ncode  %d chunks, tiles: %v\n", len(chunks), tiles(chunks, src))
	for _, c := range chunks {
		fmt.Printf("  %d  %s\n", c.Index, first([]semantic.Chunk{c}))
	}
}

// meaning keeps consecutive sentences together while they mean similar things
// and cuts where they stop. What similar means comes from Embed, which this
// package never supplies: no splitter here reaches the network on its own.
//
// The embedder below is a stand-in — a bag of words over a fixed vocabulary —
// so this example runs offline. A sentence-transformer goes behind the same
// seam without changing a line of the split.
func meaning(ctx context.Context, d semantic.Doc) {
	chunks, err := split.Semantic{Embed: bag, Threshold: 0.25}.Split(ctx, d)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\nsemantic  %d chunks at a similarity threshold of 0.25, tiles: %v\n",
		len(chunks), tiles(chunks, d.Text))
	for _, c := range chunks {
		fmt.Printf("  %d  %s\n", c.Index, first([]semantic.Chunk{c}))
	}

	// Or is how a splitter that needs a model falls back to one that needs
	// nothing: the first splitter to produce chunks wins.
	fallback := split.Or{split.Semantic{Embed: broken}, split.Sentences{Size: 300}}
	chunks, err = fallback.Split(ctx, d)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  Or{Semantic{broken embedder}, Sentences{Size: 300}} → %d chunks from the fallback\n", len(chunks))
}

// byName builds a splitter from configuration rather than from code, for a
// caller whose choice arrives as a string.
func byName(ctx context.Context, d semantic.Doc) {
	fmt.Printf("\nby name   %v\n", split.Names())
	s, err := split.New("recursive", split.Options{Size: 250, Separators: []string{"\n\n", ". "}})
	if err != nil {
		log.Fatal(err)
	}
	chunks, err := s.Split(ctx, d)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  New(\"recursive\", Options{Size: 250, Separators: …}) → %d chunks\n", len(chunks))
}

// bag is a deterministic embedder: one dimension per vocabulary word, set to
// how often the sentence uses it. It is enough to tell the Montréal paragraph
// from the ledger paragraph and nothing more.
func bag(_ context.Context, texts []string) ([][]float32, error) {
	vocab := []string{"babbage", "engines", "founded", "ada", "lovelace",
		"mill", "montréal", "office", "revenue", "costs", "1855", "%"}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, len(vocab))
		low := strings.ToLower(t)
		for j, w := range vocab {
			v[j] = float32(strings.Count(low, w))
		}
		out[i] = v
	}
	return out, nil
}

// broken is an embedder that cannot answer, which is what Or exists for.
func broken(context.Context, []string) ([][]float32, error) {
	return nil, fmt.Errorf("no model configured")
}

// tiles reports whether the chunks put back together are the document again.
func tiles(chunks []semantic.Chunk, text string) bool {
	var b strings.Builder
	for _, c := range chunks {
		b.WriteString(c.Text)
	}
	return b.String() == text
}

// longest is the size of the biggest chunk, in runes.
func longest(chunks []semantic.Chunk) int {
	n := 0
	for _, c := range chunks {
		if r := len([]rune(c.Text)); r > n {
			n = r
		}
	}
	return n
}

// first renders the head of the first chunk on one line.
func first(chunks []semantic.Chunk) string {
	if len(chunks) == 0 {
		return ""
	}
	s := strings.Join(strings.Fields(chunks[0].Text), " ")
	if len([]rune(s)) > 44 {
		s = string([]rune(s)[:44]) + "…"
	}
	return `"` + s + `"`
}
