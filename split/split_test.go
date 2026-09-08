package split

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/hanzoai/semantic"
)

const (
	prose = "First sentence about knowledge graphs. Second sentence covers entity extraction. " +
		"Third sentence discusses relation awareness. Fourth sentence wraps up the example."

	markdown = "# Intro\n\nIntro paragraph with enough text to matter.\n\n" +
		"# Body\n\nBody paragraph under a distinct heading.\n"

	source = "package main\n\nimport \"fmt\"\n\nfunc hello() {\n\tfmt.Println(\"hi\")\n}\n\n" +
		"func bye() {\n\tfmt.Println(\"bye\")\n}\n"

	unicodeText = "αβγδε ζηθικ λμνξο. 日本語のテキストです。 Emoji: 🙂🙃🙂🙃 end."
)

var texts = map[string]string{
	"prose":     prose,
	"markdown":  markdown,
	"source":    source,
	"unicode":   unicodeText,
	"paras":     "Para A content.\n\nPara B content.\n\n\nPara C content.\n",
	"crlf":      "Line one.\r\n\r\nLine two.\r\n",
	"oneword":   "supercalifragilistic",
	"spaces":    "   \n\t  ",
	"noending":  "a sentence that never ends",
	"numbers":   "Pi is 3.14 and e is 2.71. That is all.",
	"unbroken":  strings.Repeat("x", 250),
	"trailing":  "text with trailing whitespace   \n\n   ",
	"headfence": "# Real\n\n```\n# not a heading\n```\n\nText after.\n",
}

// tiling lists every splitter with overlap off, where the chunks of a document
// must join back into the document.
func tiling() map[string]semantic.Splitter {
	return map[string]semantic.Splitter{
		"Chars":            Chars{Size: 40},
		"Chars/big":        Chars{Size: 10000},
		"Chars/unset":      Chars{},
		"Words":            Words{Size: 5},
		"Words/unset":      Words{},
		"Tokens":           Tokens{Size: 8},
		"Sentences":        Sentences{Size: 60},
		"Sentences/max":    Sentences{Max: 2},
		"Sentences/unset":  Sentences{},
		"Paragraphs":       Paragraphs{Size: 40},
		"Paragraphs/unset": Paragraphs{},
		"Recursive":        Recursive{Size: 40},
		"Recursive/small":  Recursive{Size: 7},
		"Markdown":         Markdown{Size: 60},
		"Markdown/unset":   Markdown{},
		"Code":             Code{Size: 40},
		"Code/unset":       Code{},
		"Semantic":         Semantic{Embed: constant, Threshold: 0.5, Size: 50},
	}
}

// constant embeds every sentence identically, so similarity never forces a cut
// and only Size decides.
func constant(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = []float32{1, 0}
	}
	return out, nil
}

func doc(text string) semantic.Doc {
	return semantic.Doc{ID: "d1", Source: "test", Text: text}
}

func join(cs []semantic.Chunk) string {
	var b strings.Builder
	for _, c := range cs {
		b.WriteString(c.Text)
	}
	return b.String()
}

func split(t *testing.T, s semantic.Splitter, text string) []semantic.Chunk {
	t.Helper()
	cs, err := s.Split(context.Background(), doc(text))
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	return cs
}

func TestTile(t *testing.T) {
	for name, s := range tiling() {
		for label, text := range texts {
			t.Run(name+"/"+label, func(t *testing.T) {
				cs := split(t, s, text)
				if got := join(cs); got != text {
					t.Fatalf("chunks do not rejoin\n got %q\nwant %q", got, text)
				}
				if text != "" && len(cs) == 0 {
					t.Fatal("no chunks for non-empty text")
				}
				for i, c := range cs {
					if c.Index != i {
						t.Errorf("chunk %d has Index %d", i, c.Index)
					}
					if c.DocID != "d1" {
						t.Errorf("chunk %d has DocID %q, want d1", i, c.DocID)
					}
					if c.Text == "" {
						t.Errorf("chunk %d is empty", i)
					}
					if !utf8.ValidString(c.Text) {
						t.Errorf("chunk %d splits a rune: %q", i, c.Text)
					}
				}
			})
		}
	}
}

func TestEmpty(t *testing.T) {
	for name, s := range tiling() {
		t.Run(name, func(t *testing.T) {
			cs, err := s.Split(context.Background(), doc(""))
			if err != nil {
				t.Fatalf("Split: %v", err)
			}
			if len(cs) != 0 {
				t.Fatalf("got %d chunks for an empty document", len(cs))
			}
		})
	}
}

func TestShorterThanSize(t *testing.T) {
	const text = "one short line."
	for name, s := range map[string]semantic.Splitter{
		"Chars":      Chars{Size: 1000},
		"Words":      Words{Size: 1000},
		"Tokens":     Tokens{Size: 1000},
		"Sentences":  Sentences{Size: 1000},
		"Paragraphs": Paragraphs{Size: 1000},
		"Recursive":  Recursive{Size: 1000},
		"Markdown":   Markdown{Size: 1000},
		"Code":       Code{Size: 1000},
	} {
		t.Run(name, func(t *testing.T) {
			cs := split(t, s, text)
			if len(cs) != 1 || cs[0].Text != text {
				t.Fatalf("got %d chunks %q, want one chunk %q", len(cs), join(cs), text)
			}
		})
	}
}

func TestCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for name, s := range tiling() {
		t.Run(name, func(t *testing.T) {
			if _, err := s.Split(ctx, doc(prose)); !errors.Is(err, context.Canceled) {
				t.Fatalf("got %v, want context.Canceled", err)
			}
		})
	}
}

func TestChars(t *testing.T) {
	for _, tc := range []struct {
		name          string
		text          string
		size, overlap int
		want          []string
	}{
		{"exact", "abcdefghij", 5, 0, []string{"abcde", "fghij"}},
		{"remainder", "abcdefg", 3, 0, []string{"abc", "def", "g"}},
		{"overlap", "abcdefghij", 5, 2, []string{"abcde", "defgh", "ghij"}},
		// An overlap at or over Size leaves no stride, so the window walks one
		// rune at a time. It still stops at the first chunk that reaches the
		// end: every window after that is a suffix of it and carries no text
		// the reader has not already seen.
		{"overlap equals size", "abcdef", 3, 3, []string{"abc", "bcd", "cde", "def"}},
		{"overlap over size", "abcd", 2, 9, []string{"ab", "bc", "cd"}},
		{"size unset", "abcdef", 0, 0, []string{"abcdef"}},
		{"single rune", "abc", 1, 0, []string{"a", "b", "c"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cs := split(t, Chars{Size: tc.size, Overlap: tc.overlap}, tc.text)
			got := make([]string, len(cs))
			for i, c := range cs {
				got[i] = c.Text
			}
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// The overlap invariant the Python suite pins down: the tail of one chunk is
// the head of the next, and each chunk starts one stride after the last.
func TestCharsOverlapRepeats(t *testing.T) {
	const size, overlap = 30, 10
	text := strings.Repeat("abcdefghijklmnopqrstuvwxyz0123456789", 3)
	cs := split(t, Chars{Size: size, Overlap: overlap}, text)
	if len(cs) < 2 {
		t.Fatalf("got %d chunks, want at least 2", len(cs))
	}
	for i := 0; i < len(cs)-1; i++ {
		n := min(overlap, len(cs[i].Text), len(cs[i+1].Text))
		tail, head := cs[i].Text[len(cs[i].Text)-n:], cs[i+1].Text[:n]
		if tail != head {
			t.Errorf("chunk %d tail %q is not chunk %d head %q", i, tail, i+1, head)
		}
	}
	at := 0
	for i, c := range cs {
		found := strings.Index(text[at:], c.Text)
		if found != 0 {
			t.Fatalf("chunk %d does not start at offset %d", i, at)
		}
		at += size - overlap
	}
}

func TestCharsRunes(t *testing.T) {
	const size = 4
	text := "αβγδε日本語🙂🙃ok"
	cs := split(t, Chars{Size: size}, text)
	for i, c := range cs {
		n := utf8.RuneCountInString(c.Text)
		if i < len(cs)-1 && n != size {
			t.Errorf("chunk %d holds %d runes, want %d", i, n, size)
		}
		if n > size {
			t.Errorf("chunk %d holds %d runes, over the limit of %d", i, n, size)
		}
		if strings.ContainsRune(c.Text, utf8.RuneError) {
			t.Errorf("chunk %d holds a broken rune: %q", i, c.Text)
		}
	}
	if got := join(cs); got != text {
		t.Fatalf("got %q, want %q", got, text)
	}
}

func TestWords(t *testing.T) {
	const text = "one two three four five six seven"
	cs := split(t, Words{Size: 3}, text)
	want := []string{"one two three ", "four five six ", "seven"}
	for i, c := range cs {
		if i >= len(want) || c.Text != want[i] {
			t.Fatalf("chunk %d is %q, want %q", i, c.Text, want)
		}
		if n := len(strings.Fields(c.Text)); n > 3 {
			t.Errorf("chunk %d holds %d words, over the limit of 3", i, n)
		}
	}
	if len(cs) != len(want) {
		t.Fatalf("got %d chunks, want %d", len(cs), len(want))
	}
}

func TestWordsOverlap(t *testing.T) {
	cs := split(t, Words{Size: 3, Overlap: 1}, "a b c d e")
	want := []string{"a b c ", "c d e"}
	for i, c := range cs {
		if i >= len(want) || c.Text != want[i] {
			t.Fatalf("chunk %d is %q, want %q", i, c.Text, want)
		}
	}
	if len(cs) != len(want) {
		t.Fatalf("got %d chunks, want %d", len(cs), len(want))
	}
}

func TestTokens(t *testing.T) {
	t.Run("counted", func(t *testing.T) {
		// One token per word makes the budget exact and the result checkable.
		cs := split(t, Tokens{Size: 2, Count: func(string) int { return 1 }}, "a b c d e")
		want := []string{"a b ", "c d ", "e"}
		for i, c := range cs {
			if i >= len(want) || c.Text != want[i] {
				t.Fatalf("chunk %d is %q, want %q", i, c.Text, want)
			}
		}
		if len(cs) != len(want) {
			t.Fatalf("got %d chunks, want %d", len(cs), len(want))
		}
	})
	t.Run("estimated", func(t *testing.T) {
		cs := split(t, Tokens{Size: 4}, "aaaa bbbb cccc")
		want := []string{"aaaa bbbb ", "cccc"}
		for i, c := range cs {
			if i >= len(want) || c.Text != want[i] {
				t.Fatalf("chunk %d is %q, want %q", i, c.Text, want)
			}
		}
		if len(cs) != len(want) {
			t.Fatalf("got %d chunks, want %d", len(cs), len(want))
		}
	})
	t.Run("estimate", func(t *testing.T) {
		for text, want := range map[string]int{"": 0, "a": 1, "abcd": 1, "abcde": 2, "日本語": 1} {
			if got := Estimate(text); got != want {
				t.Errorf("Estimate(%q) = %d, want %d", text, got, want)
			}
		}
	})
}

func TestSentences(t *testing.T) {
	const text = "One. Two! Three? Four."
	t.Run("one each", func(t *testing.T) {
		cs := split(t, Sentences{}, text)
		want := []string{"One. ", "Two! ", "Three? ", "Four."}
		for i, c := range cs {
			if i >= len(want) || c.Text != want[i] {
				t.Fatalf("chunk %d is %q, want %q", i, c.Text, want)
			}
		}
		if len(cs) != len(want) {
			t.Fatalf("got %d chunks, want %d", len(cs), len(want))
		}
	})
	t.Run("max", func(t *testing.T) {
		cs := split(t, Sentences{Max: 2}, text)
		want := []string{"One. Two! ", "Three? Four."}
		for i, c := range cs {
			if i >= len(want) || c.Text != want[i] {
				t.Fatalf("chunk %d is %q, want %q", i, c.Text, want)
			}
		}
		if len(cs) != len(want) {
			t.Fatalf("got %d chunks, want %d", len(cs), len(want))
		}
	})
	t.Run("size", func(t *testing.T) {
		cs := split(t, Sentences{Size: 12}, text)
		for i, c := range cs {
			if n := utf8.RuneCountInString(c.Text); n > 12 {
				t.Errorf("chunk %d holds %d runes, over the limit of 12", i, n)
			}
		}
		if got := join(cs); got != text {
			t.Fatalf("got %q, want %q", got, text)
		}
	})
	t.Run("overlap", func(t *testing.T) {
		cs := split(t, Sentences{Max: 2, Overlap: 1}, text)
		want := []string{"One. Two! ", "Two! Three? ", "Three? Four."}
		for i, c := range cs {
			if i >= len(want) || c.Text != want[i] {
				t.Fatalf("chunk %d is %q, want %q", i, c.Text, want)
			}
		}
		if len(cs) != len(want) {
			t.Fatalf("got %d chunks, want %d", len(cs), len(want))
		}
	})
	t.Run("overlap over max", func(t *testing.T) {
		// An overlap wider than the chunk must still advance.
		cs := split(t, Sentences{Max: 2, Overlap: 9}, text)
		if len(cs) == 0 {
			t.Fatal("no chunks")
		}
		if last := cs[len(cs)-1].Text; !strings.HasSuffix(last, "Four.") {
			t.Fatalf("last chunk is %q, want it to reach the end", last)
		}
	})
	t.Run("decimal point", func(t *testing.T) {
		cs := split(t, Sentences{}, "Pi is 3.14 and e is 2.71. That is all.")
		want := []string{"Pi is 3.14 and e is 2.71. ", "That is all."}
		for i, c := range cs {
			if i >= len(want) || c.Text != want[i] {
				t.Fatalf("chunk %d is %q, want %q", i, c.Text, want)
			}
		}
		if len(cs) != len(want) {
			t.Fatalf("got %d chunks, want %d", len(cs), len(want))
		}
	})
}

func TestParagraphs(t *testing.T) {
	const text = "Para A.\n\nPara B.\n\n\nPara C.\n"
	t.Run("one each", func(t *testing.T) {
		cs := split(t, Paragraphs{}, text)
		want := []string{"Para A.\n\n", "Para B.\n\n\n", "Para C.\n"}
		for i, c := range cs {
			if i >= len(want) || c.Text != want[i] {
				t.Fatalf("chunk %d is %q, want %q", i, c.Text, want)
			}
		}
		if len(cs) != len(want) {
			t.Fatalf("got %d chunks, want %d", len(cs), len(want))
		}
	})
	t.Run("packed", func(t *testing.T) {
		cs := split(t, Paragraphs{Size: 20}, text)
		want := []string{"Para A.\n\nPara B.\n\n\n", "Para C.\n"}
		for i, c := range cs {
			if i >= len(want) || c.Text != want[i] {
				t.Fatalf("chunk %d is %q, want %q", i, c.Text, want)
			}
		}
		if len(cs) != len(want) {
			t.Fatalf("got %d chunks, want %d", len(cs), len(want))
		}
	})
	t.Run("single line breaks stay", func(t *testing.T) {
		cs := split(t, Paragraphs{}, "one\ntwo\nthree")
		if len(cs) != 1 {
			t.Fatalf("got %d chunks, want 1", len(cs))
		}
	})
}

func TestRecursive(t *testing.T) {
	t.Run("prefers blank lines", func(t *testing.T) {
		cs := split(t, Recursive{Size: 12}, "Alpha one.\n\nBeta two.\n\nGamma three.")
		want := []string{"Alpha one.\n\n", "Beta two.\n\n", "Gamma three."}
		for i, c := range cs {
			if i >= len(want) || c.Text != want[i] {
				t.Fatalf("chunk %d is %q, want %q", i, c.Text, want)
			}
		}
		if len(cs) != len(want) {
			t.Fatalf("got %d chunks, want %d", len(cs), len(want))
		}
	})
	t.Run("falls through to runes", func(t *testing.T) {
		cs := split(t, Recursive{Size: 10}, strings.Repeat("x", 25))
		want := []string{strings.Repeat("x", 10), strings.Repeat("x", 10), strings.Repeat("x", 5)}
		for i, c := range cs {
			if i >= len(want) || c.Text != want[i] {
				t.Fatalf("chunk %d is %q, want %q", i, c.Text, want)
			}
		}
		if len(cs) != len(want) {
			t.Fatalf("got %d chunks, want %d", len(cs), len(want))
		}
	})
	t.Run("merges small pieces back up", func(t *testing.T) {
		cs := split(t, Recursive{Size: 30}, "a\n\nb\n\nc\n\nd\n\ne")
		if len(cs) != 1 {
			t.Fatalf("got %d chunks, want 1: %q", len(cs), join(cs))
		}
	})
	t.Run("own separators", func(t *testing.T) {
		cs := split(t, Recursive{Size: 4, Separators: []string{"|"}}, "abc|def|ghi")
		want := []string{"abc|", "def|", "ghi"}
		for i, c := range cs {
			if i >= len(want) || c.Text != want[i] {
				t.Fatalf("chunk %d is %q, want %q", i, c.Text, want)
			}
		}
		if len(cs) != len(want) {
			t.Fatalf("got %d chunks, want %d", len(cs), len(want))
		}
	})
	t.Run("size bounds every chunk", func(t *testing.T) {
		cs := split(t, Recursive{Size: 25}, prose)
		for i, c := range cs {
			if n := utf8.RuneCountInString(c.Text); n > 25 {
				t.Errorf("chunk %d holds %d runes, over the limit of 25", i, n)
			}
		}
	})
	t.Run("default separators are fresh", func(t *testing.T) {
		s := separators()
		s[0] = "MUTATED"
		if separators()[0] != "\n\n" {
			t.Fatal("editing the returned defaults changed the next call")
		}
	})
}

func TestMarkdown(t *testing.T) {
	const two = "# Alpha\n\nContent about alpha.\n\n# Beta\n\nContent about beta.\n"
	t.Run("headings separate sections", func(t *testing.T) {
		for _, size := range []int{0, 30} {
			cs := split(t, Markdown{Size: size}, two)
			if len(cs) != 2 {
				t.Fatalf("size %d: got %d chunks, want 2", size, len(cs))
			}
			for _, c := range cs {
				if strings.Contains(c.Text, "about alpha") && strings.Contains(c.Text, "about beta") {
					t.Errorf("size %d: sections merged across a heading: %q", size, c.Text)
				}
			}
			if !strings.HasPrefix(cs[0].Text, "# Alpha") || !strings.HasPrefix(cs[1].Text, "# Beta") {
				t.Errorf("size %d: chunks do not start at their headings: %q", size, join(cs))
			}
		}
	})
	t.Run("small sections merge", func(t *testing.T) {
		cs := split(t, Markdown{Size: 1000}, two)
		if len(cs) != 1 {
			t.Fatalf("got %d chunks, want 1", len(cs))
		}
	})
	t.Run("hash in a fence is not a heading", func(t *testing.T) {
		cs := split(t, Markdown{}, texts["headfence"])
		if len(cs) != 1 {
			t.Fatalf("got %d chunks, want 1: %q", len(cs), join(cs))
		}
		if !strings.Contains(cs[0].Text, "# not a heading") {
			t.Fatal("the fenced text was cut out")
		}
	})
	t.Run("deep headings also cut", func(t *testing.T) {
		cs := split(t, Markdown{}, "# One\n\ntext\n\n### Three\n\nmore\n")
		if len(cs) != 2 {
			t.Fatalf("got %d chunks, want 2", len(cs))
		}
	})
}

func TestCode(t *testing.T) {
	cs := split(t, Code{}, source)
	if len(cs) != 4 {
		t.Fatalf("got %d chunks, want 4: %q", len(cs), join(cs))
	}
	hello := cs[2].Text
	for _, want := range []string{"func hello()", "fmt.Println(\"hi\")", "}"} {
		if !strings.Contains(hello, want) {
			t.Errorf("the declaration chunk %q is missing %q", hello, want)
		}
	}
	if strings.Contains(hello, "func bye") {
		t.Errorf("two declarations landed in one chunk: %q", hello)
	}
	t.Run("fence stays whole", func(t *testing.T) {
		text := "intro\n\n```\nnot code\nstill not\n```\n\noutro\n"
		cs := split(t, Code{}, text)
		fence := ""
		for _, c := range cs {
			if strings.Contains(c.Text, "```") {
				fence = c.Text
			}
		}
		if !strings.Contains(fence, "not code") || !strings.Contains(fence, "still not") {
			t.Fatalf("the fenced block was cut: %q", fence)
		}
	})
	t.Run("packed", func(t *testing.T) {
		cs := split(t, Code{Size: 1000}, source)
		if len(cs) != 1 {
			t.Fatalf("got %d chunks, want 1", len(cs))
		}
	})
}

// byTopic embeds a sentence by the animal it is about, so similarity drops
// exactly where the subject changes.
func byTopic(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, s := range texts {
		switch {
		case strings.Contains(s, "Cats"):
			out[i] = []float32{1, 0}
		case strings.Contains(s, "Dogs"):
			out[i] = []float32{0, 1}
		default:
			out[i] = []float32{0, 0}
		}
	}
	return out, nil
}

func TestSemantic(t *testing.T) {
	const text = "Cats purr. Cats nap. Dogs bark. Dogs run."
	t.Run("cuts where the topic changes", func(t *testing.T) {
		cs := split(t, Semantic{Embed: byTopic, Threshold: 0.5}, text)
		want := []string{"Cats purr. Cats nap. ", "Dogs bark. Dogs run."}
		for i, c := range cs {
			if i >= len(want) || c.Text != want[i] {
				t.Fatalf("chunk %d is %q, want %q", i, c.Text, want)
			}
		}
		if len(cs) != len(want) {
			t.Fatalf("got %d chunks, want %d", len(cs), len(want))
		}
	})
	t.Run("threshold zero keeps everything together", func(t *testing.T) {
		cs := split(t, Semantic{Embed: byTopic, Threshold: 0}, text)
		if len(cs) != 1 {
			t.Fatalf("got %d chunks, want 1: %q", len(cs), join(cs))
		}
	})
	t.Run("size still bounds a chunk", func(t *testing.T) {
		cs := split(t, Semantic{Embed: constant, Threshold: 0.5, Size: 11}, text)
		if len(cs) != 4 {
			t.Fatalf("got %d chunks, want 4: %q", len(cs), join(cs))
		}
	})
	t.Run("one sentence needs no vectors", func(t *testing.T) {
		called := false
		e := func(ctx context.Context, ts []string) ([][]float32, error) {
			called = true
			return nil, errors.New("should not be called")
		}
		cs := split(t, Semantic{Embed: e, Threshold: 0.5}, "Only one sentence here.")
		if called {
			t.Fatal("embedded a document with a single sentence")
		}
		if len(cs) != 1 {
			t.Fatalf("got %d chunks, want 1", len(cs))
		}
	})
	t.Run("missing embed", func(t *testing.T) {
		_, err := Semantic{Threshold: 0.5}.Split(context.Background(), doc(text))
		if err == nil {
			t.Fatal("want an error without an Embed")
		}
	})
	t.Run("embed failure is wrapped", func(t *testing.T) {
		fail := errors.New("model down")
		e := func(context.Context, []string) ([][]float32, error) { return nil, fail }
		_, err := Semantic{Embed: e, Threshold: 0.5}.Split(context.Background(), doc(text))
		if !errors.Is(err, fail) {
			t.Fatalf("got %v, want it to wrap %v", err, fail)
		}
	})
	t.Run("wrong vector count", func(t *testing.T) {
		e := func(context.Context, []string) ([][]float32, error) { return [][]float32{{1}}, nil }
		_, err := Semantic{Embed: e, Threshold: 0.5}.Split(context.Background(), doc(text))
		if err == nil {
			t.Fatal("want an error when the vectors do not match the sentences")
		}
	})
}

func TestCosine(t *testing.T) {
	for _, tc := range []struct {
		name string
		a, b []float32
		want float64
	}{
		{"same", []float32{1, 0}, []float32{1, 0}, 1},
		{"orthogonal", []float32{1, 0}, []float32{0, 1}, 0},
		{"opposite", []float32{1, 0}, []float32{-1, 0}, -1},
		{"zero length", []float32{0, 0}, []float32{1, 0}, 0},
		{"different width", []float32{1, 0}, []float32{1, 0, 0}, 0},
		{"empty", nil, nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := cosine(tc.a, tc.b); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

type failing struct{ err error }

func (f failing) Split(context.Context, semantic.Doc) ([]semantic.Chunk, error) {
	return nil, f.err
}

type nothing struct{}

func (nothing) Split(context.Context, semantic.Doc) ([]semantic.Chunk, error) { return nil, nil }

func TestOr(t *testing.T) {
	fail := errors.New("no model")
	t.Run("falls back", func(t *testing.T) {
		cs := split(t, Or{failing{fail}, Sentences{}}, "One. Two.")
		if len(cs) != 2 {
			t.Fatalf("got %d chunks, want 2", len(cs))
		}
	})
	t.Run("skips an empty result", func(t *testing.T) {
		cs := split(t, Or{nothing{}, Chars{Size: 4}}, "abcdefgh")
		if len(cs) != 2 {
			t.Fatalf("got %d chunks, want 2", len(cs))
		}
	})
	t.Run("keeps the last failure", func(t *testing.T) {
		_, err := Or{failing{errors.New("first")}, failing{fail}}.Split(context.Background(), doc("x"))
		if !errors.Is(err, fail) {
			t.Fatalf("got %v, want it to wrap %v", err, fail)
		}
	})
	t.Run("stops on cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		tried := 0
		count := func(context.Context, semantic.Doc) ([]semantic.Chunk, error) { tried++; return nil, nil }
		_, err := Or{Chars{Size: 4}, splitFunc(count)}.Split(ctx, doc("abcd"))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context.Canceled", err)
		}
		if tried != 0 {
			t.Fatalf("tried %d more splitters after cancellation", tried)
		}
	})
	t.Run("empty document", func(t *testing.T) {
		cs, err := Or{Sentences{}}.Split(context.Background(), doc(""))
		if err != nil || len(cs) != 0 {
			t.Fatalf("got %d chunks and %v, want none and no error", len(cs), err)
		}
	})
}

type splitFunc func(context.Context, semantic.Doc) ([]semantic.Chunk, error)

func (f splitFunc) Split(ctx context.Context, d semantic.Doc) ([]semantic.Chunk, error) {
	return f(ctx, d)
}

func TestNew(t *testing.T) {
	t.Run("every name builds", func(t *testing.T) {
		o := Options{Size: 50, Overlap: 5, Max: 3, Threshold: 0.5, Embed: constant}
		for _, name := range Names() {
			s, err := New(name, o)
			if err != nil {
				t.Fatalf("New(%q): %v", name, err)
			}
			cs, err := s.Split(context.Background(), doc(prose))
			if err != nil {
				t.Fatalf("%s.Split: %v", name, err)
			}
			if len(cs) == 0 {
				t.Errorf("%s produced no chunks", name)
			}
		}
	})
	t.Run("names are sorted and complete", func(t *testing.T) {
		want := []string{"chars", "code", "markdown", "paragraphs", "recursive", "semantic", "sentences", "tokens", "words"}
		if got := Names(); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})
	t.Run("unknown name", func(t *testing.T) {
		if _, err := New("wishful", Options{}); err == nil {
			t.Fatal("want an error for a name nothing is registered under")
		}
	})
	t.Run("semantic needs an embed", func(t *testing.T) {
		if _, err := New("semantic", Options{Threshold: 0.5}); err == nil {
			t.Fatal("want an error without an Embed")
		}
	})
	t.Run("options reach the splitter", func(t *testing.T) {
		s, err := New("chars", Options{Size: 4})
		if err != nil {
			t.Fatal(err)
		}
		if cs := split(t, s, "abcdefgh"); len(cs) != 2 {
			t.Fatalf("got %d chunks, want 2", len(cs))
		}
	})
	t.Run("register", func(t *testing.T) {
		Register("half", func(o Options) (semantic.Splitter, error) {
			return Chars{Size: o.Size / 2}, nil
		})
		s, err := New("half", Options{Size: 8})
		if err != nil {
			t.Fatal(err)
		}
		if cs := split(t, s, "abcdefgh"); len(cs) != 2 {
			t.Fatalf("got %d chunks, want 2", len(cs))
		}
	})
}

func TestScan(t *testing.T) {
	for _, tc := range []struct {
		name string
		scan func(string) []span
		text string
		want []string
	}{
		{"words", words, "  alpha beta  gamma ", []string{"  alpha ", "beta  ", "gamma "}},
		{"words all space", words, "   ", []string{"   "}},
		{"sentences", sentences, "A. B! C? D", []string{"A. ", "B! ", "C? ", "D"}},
		{"sentences run on", sentences, "Wait... Now.", []string{"Wait... ", "Now."}},
		{"paragraphs", paragraphs, "a\n\nb\n \nc", []string{"a\n\n", "b\n \n", "c"}},
		{"lines", lines, "a\nb\n", []string{"a\n", "b\n"}},
		{"sections", sections, "intro\n# One\nbody\n", []string{"intro\n", "# One\nbody\n"}},
		{"blocks", blocks, "a\n  b\n}\nc\n", []string{"a\n  b\n}\n", "c\n"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spans := tc.scan(tc.text)
			got := make([]string, len(spans))
			at := 0
			for i, s := range spans {
				if s.start != at {
					t.Fatalf("span %d starts at %d, want %d: the spans do not tile", i, s.start, at)
				}
				at = s.end
				got[i] = tc.text[s.start:s.end]
			}
			if at != len(tc.text) {
				t.Fatalf("the spans stop at %d of %d bytes", at, len(tc.text))
			}
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestScanTiles(t *testing.T) {
	for _, scan := range []struct {
		name string
		fn   func(string) []span
	}{
		{"words", words}, {"sentences", sentences}, {"paragraphs", paragraphs},
		{"lines", lines}, {"sections", sections}, {"blocks", blocks},
	} {
		for label, text := range texts {
			t.Run(scan.name+"/"+label, func(t *testing.T) {
				at := 0
				for i, s := range scan.fn(text) {
					if s.start != at || s.end <= s.start {
						t.Fatalf("span %d is %v, want it to start at %d and be non-empty", i, s, at)
					}
					at = s.end
				}
				if at != len(text) {
					t.Fatalf("the spans cover %d of %d bytes", at, len(text))
				}
			})
		}
	}
}

func TestPipeline(t *testing.T) {
	// The splitters are the pipeline's Split stage, which is the point of them.
	p := semantic.Pipeline{Ingest: source1{}, Split: Sentences{Max: 2}}
	got, err := p.Run(context.Background(), "ref")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != nil {
		t.Fatalf("got %v triples, want none without an extractor", got)
	}
	cs := split(t, Sentences{Max: 2}, prose)
	if len(cs) != 2 {
		t.Fatalf("got %d chunks, want 2", len(cs))
	}
}

type source1 struct{}

func (source1) Ingest(context.Context, string) ([]semantic.Doc, error) {
	return []semantic.Doc{doc(prose)}, nil
}

func min(ns ...int) int {
	m := ns[0]
	for _, n := range ns[1:] {
		if n < m {
			m = n
		}
	}
	return m
}
