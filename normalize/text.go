package normalize

import (
	"html"
	"regexp"
	"strings"
	"unicode"
)

// Lines settles line endings on "\n", so text that travelled through Windows
// or a classic Mac editor reads the same as text that did not.
func Lines(s string) string {
	if !strings.ContainsRune(s, '\r') {
		return s
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// Control drops the characters that carry no text: C0 and C1 control codes
// other than tab and newline, the byte order mark wherever it turns up, and the
// soft hyphen, which is a line-breaking hint that survives copy and paste as an
// invisible character inside a word.
func Control(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r == 0x00AD || r == 0xFEFF:
			return -1
		case unicode.Is(unicode.Cc, r):
			return -1
		}
		return r
	}, s)
}

// Space regularizes horizontal space without destroying the shape of the text:
// every kind of space becomes an ordinary one, runs collapse, trailing space
// goes, and a run of blank lines becomes a single blank line. Paragraphs
// survive, which is what a splitter wants.
func Space(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\t' || unicode.Is(unicode.Zs, r) {
			return ' '
		}
		return r
	}, s)

	var b strings.Builder
	b.Grow(len(s))
	blank := 0
	first := true
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(squeeze(line, ' '))
		if line == "" {
			blank++
			continue
		}
		if !first {
			b.WriteByte('\n')
			if blank > 0 {
				b.WriteByte('\n')
			}
		}
		b.WriteString(line)
		blank, first = 0, false
	}
	return b.String()
}

// Flat collapses every run of whitespace, newlines included, to a single space.
// Use it for text that is going into one field or into a comparison key, where
// the line structure is noise.
func Flat(s string) string {
	return strings.TrimSpace(squeeze(strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, s), ' '))
}

func squeeze(s string, c byte) string {
	var b strings.Builder
	b.Grow(len(s))
	prev := false
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			if prev {
				continue
			}
			prev = true
		} else {
			prev = false
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// quote maps typographic punctuation onto the ASCII it stands for. Word
// processors and PDF extraction produce these constantly, and they turn one
// string into several as far as an exact match is concerned.
var quote = strings.NewReplacer(
	"‘", "'", // left single quotation mark
	"’", "'", // right single quotation mark
	"‚", "'", // single low-9 quotation mark
	"‛", "'", // single high-reversed-9 quotation mark
	"′", "'", // prime
	"“", `"`, // left double quotation mark
	"”", `"`, // right double quotation mark
	"„", `"`, // double low-9 quotation mark
	"‟", `"`, // double high-reversed-9 quotation mark
	"″", `"`, // double prime
	"‐", "-", // hyphen
	"‑", "-", // non-breaking hyphen
	"‒", "-", // figure dash
	"–", "-", // en dash
	"−", "-", // minus sign
	"—", "--", // em dash
	"―", "--", // horizontal bar
	"…", "...", // horizontal ellipsis
)

// Quotes folds curly quotes, dashes and ellipses to their ASCII spelling.
func Quotes(s string) string { return quote.Replace(s) }

// Lower returns s in lower case.
func Lower(s string) string { return strings.ToLower(s) }

// Upper returns s in upper case.
func Upper(s string) string { return strings.ToUpper(s) }

// Title capitalizes the first letter of every word and lowers the rest.
func Title(s string) string {
	word := false
	return strings.Map(func(r rune) rune {
		letter := unicode.IsLetter(r)
		start := letter && !word
		word = letter
		if start {
			return unicode.ToTitle(r)
		}
		return unicode.ToLower(r)
	}, s)
}

var (
	script  = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script[^>]*>`)
	style   = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style[^>]*>`)
	remark  = regexp.MustCompile(`(?s)<!--.*?-->`)
	element = regexp.MustCompile(`(?s)<[^>]*>`)
)

// Tags removes HTML markup and decodes character references, leaving a space
// where each element stood so that words do not run together. It leaves the
// spacing itself alone; follow it with Space or Flat.
//
// Script and style elements go with their contents, since their text is code
// rather than prose.
func Tags(s string) string {
	if !strings.ContainsRune(s, '<') {
		return html.UnescapeString(s)
	}
	s = script.ReplaceAllString(s, " ")
	s = style.ReplaceAllString(s, " ")
	s = remark.ReplaceAllString(s, " ")
	s = element.ReplaceAllString(s, " ")
	return html.UnescapeString(s)
}

var digits = regexp.MustCompile(`\d+`)

// Repeats removes the lines that recur across pages — running heads, footers,
// page numbers — and returns the pages without them. A line counts as furniture
// when it shows up on at least share of the pages; share of zero or less means
// three fifths of them.
//
// Lines are compared with their digits blanked, so "Page 1 of 40" and
// "Page 2 of 40" count as the same line and a bare page number matches every
// other bare page number.
func Repeats(pages []string, share float64) []string {
	if len(pages) < 2 {
		return pages
	}
	if share <= 0 {
		share = 0.6
	}
	seen := map[string]int{}
	for _, page := range pages {
		once := map[string]bool{}
		for _, line := range strings.Split(page, "\n") {
			k := key(line)
			if k == "" || once[k] {
				continue
			}
			once[k] = true
			seen[k]++
		}
	}
	floor := int(share*float64(len(pages)) + 0.5)
	if floor < 2 {
		floor = 2
	}
	out := make([]string, len(pages))
	for i, page := range pages {
		var keep []string
		for _, line := range strings.Split(page, "\n") {
			if k := key(line); k != "" && seen[k] >= floor {
				continue
			}
			keep = append(keep, line)
		}
		out[i] = strings.Join(keep, "\n")
	}
	return out
}

func key(line string) string {
	return digits.ReplaceAllString(strings.ToLower(Flat(line)), "#")
}
