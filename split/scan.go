package split

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// The scanners below all obey one rule: the spans they return tile the text.
// Whitespace and separators belong to the piece they follow, so joining the
// spans reproduces the input and no splitter built on them can lose a byte.

// words finds runs of non-space runes, each carrying the whitespace that
// follows it.
func words(text string) []span {
	var out []span
	start, i := 0, 0
	for i < len(text) {
		for i < len(text) {
			r, w := utf8.DecodeRuneInString(text[i:])
			if !unicode.IsSpace(r) {
				break
			}
			i += w
		}
		if i >= len(text) {
			break
		}
		for i < len(text) {
			r, w := utf8.DecodeRuneInString(text[i:])
			if unicode.IsSpace(r) {
				break
			}
			i += w
		}
		end := i
		for end < len(text) {
			r, w := utf8.DecodeRuneInString(text[end:])
			if !unicode.IsSpace(r) {
				break
			}
			end += w
		}
		out = append(out, span{start, end})
		start, i = end, end
	}
	if start < len(text) {
		out = append(out, span{start, len(text)})
	}
	return out
}

// sentences cuts after a run of terminators followed by whitespace. It knows
// nothing about abbreviations, so "Dr. Smith" is two sentences; the price of
// that is one wrong boundary, the price of a rule list is a wrong boundary in
// every language the list does not cover.
func sentences(text string) []span {
	var out []span
	start, i := 0, 0
	for i < len(text) {
		r, w := utf8.DecodeRuneInString(text[i:])
		i += w
		if !terminator(r) {
			continue
		}
		for i < len(text) {
			r2, w2 := utf8.DecodeRuneInString(text[i:])
			if !terminator(r2) {
				break
			}
			i += w2
		}
		end := i
		for end < len(text) {
			r3, w3 := utf8.DecodeRuneInString(text[end:])
			if !unicode.IsSpace(r3) {
				break
			}
			end += w3
		}
		if end == i {
			continue // a terminator inside a word, as in "3.14"
		}
		out = append(out, span{start, end})
		start, i = end, end
	}
	if start < len(text) {
		out = append(out, span{start, len(text)})
	}
	return out
}

func terminator(r rune) bool { return r == '.' || r == '!' || r == '?' }

// paragraphs cuts on a blank line: a run of whitespace holding two or more
// newlines.
func paragraphs(text string) []span {
	var out []span
	start, i := 0, 0
	for i < len(text) {
		r, w := utf8.DecodeRuneInString(text[i:])
		if r != '\n' {
			i += w
			continue
		}
		end, lines := i, 0
		for end < len(text) {
			r2, w2 := utf8.DecodeRuneInString(text[end:])
			if !unicode.IsSpace(r2) {
				break
			}
			if r2 == '\n' {
				lines++
			}
			end += w2
		}
		if lines >= 2 {
			out = append(out, span{start, end})
			start = end
		}
		i = end
	}
	if start < len(text) {
		out = append(out, span{start, len(text)})
	}
	return out
}

// lines returns each line together with the newline that ends it.
func lines(text string) []span {
	var out []span
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			out = append(out, span{start, i + 1})
			start = i + 1
		}
	}
	if start < len(text) {
		out = append(out, span{start, len(text)})
	}
	return out
}

// sections cuts before every ATX heading, so a heading and the prose under it
// stay together. Headings inside a fenced block are comments, not headings.
func sections(text string) []span {
	var out []span
	start, fence := 0, ""
	for _, ln := range lines(text) {
		body := strings.TrimRight(text[ln.start:ln.end], "\r\n")
		bare := strings.TrimLeft(body, " ")
		indent := len(body) - len(bare)
		switch {
		case fence != "":
			if strings.HasPrefix(bare, fence) {
				fence = ""
			}
		case indent < 4 && fenced(bare):
			fence = bare[:3]
		case indent < 4 && heading(bare) && ln.start > start:
			out = append(out, span{start, ln.start})
			start = ln.start
		}
	}
	if start < len(text) {
		out = append(out, span{start, len(text)})
	}
	return out
}

// heading reports whether a line opens an ATX heading: one to six hashes and
// then a space or nothing.
func heading(line string) bool {
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	if n == 0 || n > 6 {
		return false
	}
	return n == len(line) || line[n] == ' ' || line[n] == '\t'
}

func fenced(line string) bool {
	return strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~")
}

// blocks cuts source code at the top of each declaration: a line in the first
// column that is neither blank nor a closing bracket. Indented lines and blank
// lines belong to the declaration above them, so a signature keeps its body
// and a body keeps its closing brace. A fenced block is never cut into.
func blocks(text string) []span {
	var out []span
	start, fence := 0, ""
	open := func(at int) {
		if at > start {
			out = append(out, span{start, at})
			start = at
		}
	}
	for _, ln := range lines(text) {
		body := strings.TrimRight(text[ln.start:ln.end], "\r\n")
		bare := strings.TrimLeft(body, " \t")
		switch {
		case fence != "":
			if strings.HasPrefix(bare, fence) {
				fence = ""
			}
		case fenced(bare):
			open(ln.start)
			fence = bare[:3]
		case body == "" || body != bare || closer(body):
			// blank, indented, or a bracket closing what came before
		default:
			open(ln.start)
		}
	}
	if start < len(text) {
		out = append(out, span{start, len(text)})
	}
	return out
}

func closer(line string) bool {
	if line == "" {
		return false
	}
	switch line[0] {
	case '}', ')', ']':
		return true
	}
	return false
}
