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
//
// Code is left exactly as written, because in code the spacing is the
// meaning: a CommonMark fenced block, from its opening fence through its
// closing one (or to the end, when it never closes), and an indented block —
// lines indented four columns or more that do not continue a paragraph — keep
// their indentation, their inner spacing, their trailing space and their blank
// lines. Line endings are settled first, as Lines settles them.
func Space(s string) string {
	s = Lines(s)
	var (
		b     strings.Builder
		fence string   // the open fence, "" outside a fenced block
		code  bool     // inside an indented block
		para  bool     // the last line was prose, which an indented line continues
		held  []string // blank lines not yet written
		first = true
	)
	b.Grow(len(s))
	write := func(line string) {
		if !first {
			b.WriteByte('\n')
		}
		b.WriteString(line)
		first = false
	}
	// settle writes the blank lines held before a line: all of them, as they
	// were, between two lines of one indented block, and otherwise one.
	settle := func(verbatim bool) {
		switch {
		case verbatim:
			for _, h := range held {
				write(h)
			}
		case len(held) > 0 && !first:
			write("")
		}
		held = held[:0]
	}

	for line := range strings.SplitSeq(s, "\n") {
		if fence != "" {
			write(line)
			if closes(line, fence) {
				fence = ""
			}
			continue
		}
		if strings.TrimFunc(line, space) == "" {
			held = append(held, line)
			para = false
			continue
		}
		width, rest := indent(line)
		switch {
		case width >= 4 && (code || !para):
			settle(code)
			write(line)
			code = true
			continue
		case width < 4 && opens(rest) != "":
			settle(false)
			write(line)
			fence, code, para = opens(rest), false, false
			continue
		}
		settle(false)
		code = false
		write(strings.TrimSpace(squeeze(strings.Map(func(r rune) rune {
			if r == '\t' || unicode.Is(unicode.Zs, r) {
				return ' '
			}
			return r
		}, line), ' ')))
		para = width >= 4 || !atx(rest)
	}
	return b.String()
}

// space reports a rune that is only space: a tab or a space separator.
func space(r rune) bool { return r == '\t' || unicode.Is(unicode.Zs, r) }

// indent measures a line's indentation in columns, a tab advancing to the next
// multiple of four as CommonMark counts it, and returns what follows it.
func indent(line string) (int, string) {
	width := 0
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case ' ':
			width++
		case '\t':
			width += 4 - width%4
		default:
			return width, line[i:]
		}
	}
	return width, ""
}

// opens returns the fence a line opens — three or more backticks or tildes —
// or "" when it opens none. A backtick fence's info string cannot itself hold
// a backtick, which is how CommonMark tells a fence from inline code.
func opens(rest string) string {
	for _, c := range []byte{'`', '~'} {
		n := 0
		for n < len(rest) && rest[n] == c {
			n++
		}
		if n >= 3 && (c == '~' || !strings.ContainsRune(rest[n:], '`')) {
			return rest[:n]
		}
	}
	return ""
}

// closes reports whether a line closes the fence: indented under four columns,
// the same character at least as many times, and nothing after but space.
func closes(line, fence string) bool {
	width, rest := indent(line)
	if width >= 4 {
		return false
	}
	n := 0
	for n < len(rest) && rest[n] == fence[0] {
		n++
	}
	return n >= len(fence) && strings.Trim(rest[n:], " \t") == ""
}

// atx reports whether a line is an ATX heading, which ends a paragraph: one to
// six # and then a space, a tab, or nothing.
func atx(rest string) bool {
	n := 0
	for n < len(rest) && rest[n] == '#' {
		n++
	}
	return n >= 1 && n <= 6 && (n == len(rest) || rest[n] == ' ' || rest[n] == '\t')
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
		for line := range strings.SplitSeq(page, "\n") {
			k := key(line)
			if k == "" || once[k] {
				continue
			}
			once[k] = true
			seen[k]++
		}
	}
	floor := max(int(share*float64(len(pages))+0.5), 2)
	out := make([]string, len(pages))
	for i, page := range pages {
		var keep []string
		for line := range strings.SplitSeq(page, "\n") {
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
