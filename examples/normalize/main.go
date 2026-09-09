// Command normalize is the cleanup between parsing a document and splitting
// it. The same sentence written with a non-breaking space, a curly apostrophe
// or a decomposed accent has to reach the extractor as one string rather than
// three, or a gazetteer of names matches none of them.
//
// Every transform is a func(string) string, so they compose into a Chain and a
// caller assembles the pipeline its corpus needs. Text and Clean are the two
// chains worth having by default.
//
//	go run ./examples/normalize
package main

import (
	"fmt"
	"strings"

	"github.com/hanzoai/semantic/normalize"
)

func main() {
	forms()
	bytes()
	shape()
	defaults()
	furniture()
	chain()
}

// forms are the four Unicode normalization forms. The canonical pair settles
// how a character is spelled; the compatibility pair also folds characters
// that merely look like others, which is what a search key wants and what a
// faithful transcription does not.
func forms() {
	fmt.Println("unicode forms")
	fmt.Printf("  %-10s %-14s %-14s %-14s %s\n", "input", "NFC", "NFD", "NFKC", "NFKD")
	for _, s := range []string{
		"Montréal", // e + combining acute
		"Montréal",  // é, composed
		"ﬁle",       // the ﬁ ligature
		"Ångström",
	} {
		fmt.Printf("  %-10s %-14s %-14s %-14s %s\n",
			show(s), show(normalize.NFC(s)), show(normalize.NFD(s)),
			show(normalize.NFKC(s)), show(normalize.NFKD(s)))
	}
	fmt.Printf("  NFD then NFC is the identity: %v\n",
		normalize.NFC(normalize.NFD("Montréal")) == "Montréal")
	fmt.Printf("  Marks drops them entirely: %q\n", normalize.Marks("Crème Brûlée"))
}

// bytes is what a reader hands over before any of the above can run: an
// encoding to guess, a byte order mark to skip, and mojibake to undo.
func bytes() {
	fmt.Println("\nbytes")
	latin1 := []byte{'M', 'o', 'n', 't', 'r', 0xe9, 'a', 'l'}
	utf8 := []byte("Montréal")
	utf16 := []byte{0xff, 0xfe, 'M', 0, 'o', 0, 'n', 0, 't', 0, 'r', 0, 0xe9, 0, 'a', 0, 'l', 0}
	for _, b := range [][]byte{utf8, latin1, utf16} {
		enc, sure := normalize.Guess(b)
		fmt.Printf("  %-12s confidence %.2f  bom %d bytes  → %q\n",
			enc, sure, normalize.BOM(b), normalize.Decode(b))
	}

	// Mojibake: text that was UTF-8, read as windows-1252, and stored that
	// way. Repair re-encodes and only keeps the result if it decodes cleanly,
	// so text that was never damaged comes back untouched.
	for _, s := range []string{"MontrÃ©al", "Montréal"} {
		fmt.Printf("  repair %-12q → %q\n", s, normalize.Repair(s))
	}
}

// shape is the punctuation and whitespace layer: the characters that make two
// spellings of one sentence look identical and compare unequal.
func shape() {
	fmt.Println("\nshape")
	steps := []struct {
		name string
		f    normalize.Step
		in   string
	}{
		{"Space", normalize.Space, "Ada Lovelace   works for  Babbage.   "},
		{"Quotes", normalize.Quotes, "“the engine’s mill” — 1843…"},
		{"Control", normalize.Control, "Bab\u00adbage Engines\ufeff"},
		{"Lines", normalize.Lines, "one\r\ntwo\rthree"},
		{"Tags", normalize.Tags, "<p>Founded by <b>Charles</b>&nbsp;Babbage</p><script>x()</script>"},
		{"Flat", normalize.Flat, "one\n\ntwo\nthree"},
	}
	for _, s := range steps {
		fmt.Printf("  %-8s %-52q → %q\n", s.name, s.in, s.f(s.in))
	}
}

// defaults are the two chains most callers want. Text keeps the shape of the
// prose, so paragraphs survive and a splitter can still see them. Clean goes
// further and flattens the line structure, which is what a similarity key or a
// single field wants and what a document headed for a splitter does not.
func defaults() {
	const raw = "<h1>Field notes</h1>\r\n\r\n" +
		"<p>Ada’s engine — built in Montréal.</p>\r\n\r\n\r\n" +
		"<p>It works.</p>\r\n"
	fmt.Println("\ndefaults")
	fmt.Printf("  raw    %q\n", raw)
	fmt.Printf("  Text   %q\n", normalize.Text(raw))
	fmt.Printf("  Clean  %q\n", normalize.Clean(raw))
}

// furniture is the running heads, footers and page numbers a page-based
// document carries on every page. Lines are compared with their digits
// blanked, so "Page 1 of 3" and "Page 2 of 3" count as the same line.
func furniture() {
	pages := []string{
		"BABBAGE ENGINES — CONFIDENTIAL\nAda Lovelace joined in 1843.\nPage 1 of 3",
		"BABBAGE ENGINES — CONFIDENTIAL\nThe mill was finished in 1849.\nPage 2 of 3",
		"BABBAGE ENGINES — CONFIDENTIAL\nMontréal opened in 1852.\nPage 3 of 3",
	}
	fmt.Println("\nrunning heads")
	for i, p := range normalize.Repeats(pages, 0) {
		fmt.Printf("  page %d  %q\n", i+1, strings.ReplaceAll(p, "\n", " ⏎ "))
	}
}

// chain is the point of every transform being one function: a corpus that
// arrives as scraped HTML in a legacy encoding needs a different order than
// one that arrives as clean Markdown, and the caller writes that order down
// rather than configuring it.
func chain() {
	scraped := normalize.Chain{
		normalize.Repair,  // undo the mojibake the scrape introduced
		normalize.Tags,    // markup out
		normalize.Control, // invisible characters out
		normalize.NFC,     // one spelling per character
		normalize.Quotes,  // typographic punctuation to ASCII
		normalize.Flat,    // one line, for a comparison key
		strings.ToLower,
	}
	// One document at a time, which is how a scrape arrives and what Repair
	// needs: it re-encodes the whole string and keeps the result only if all
	// of it decodes, so a string that is half damaged is left alone.
	fmt.Println("\nchain")
	var out []string
	for _, in := range []string{
		"<p>Ada\u00e2\u20ac\u2122s office is in Montr\u00c3\u00a9al.</p>",
		"<p>Ada\u2019s office is in Montre\u0301al.</p>",
	} {
		got := scraped.Run(in)
		out = append(out, got)
		fmt.Printf("  %-44q → %q\n", in, got)
	}

	// The whole reason for the chain: two documents a reader cannot tell
	// apart now compare equal.
	fmt.Printf("  equal: %v\n", out[0] == out[1])
}

// show renders a string with its rune count, since two spellings of one word
// are exactly what a terminal hides: NFC and NFD print identically and differ
// in length.
func show(s string) string { return fmt.Sprintf("%s·%d", s, len([]rune(s))) }
