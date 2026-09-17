package parse

import (
	"context"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"

	"github.com/hanzoai/semantic"
)

// CSV reads delimited text. The header row names the fields, the rows become
// the decoded value, and the text becomes one "field: value" line per cell —
// which is what gives a splitter and an extractor something to work on, since
// a grid of bare cells says nothing about what the cells mean.
//
// One reader covers the family. Sep set to a tab reads TSV; Sep left at zero
// takes the separator from the document, which is what a file named .csv but
// written with semicolons needs.
//
// Rows come back rectangular: every field the header named is present in
// every row, empty where the row had no cell. Empty cells contribute no text,
// an empty cell being nothing to assert, so a row of them is no section.
type CSV struct {
	// Sep is the field separator. Zero takes it from the document.
	Sep rune
	// Bare says the document has no header row, so its fields are numbered
	// from one.
	Bare bool
}

// Parse reads the rows and renders their cells as "field: value" lines.
func (c CSV) Parse(_ context.Context, d semantic.Doc) (semantic.Doc, error) {
	src := lines(d.Text)
	comma := c.Sep
	if comma == 0 {
		comma = sep(src)
	}
	if comma == 0 {
		comma = ','
	}

	r := csv.NewReader(strings.NewReader(src))
	r.Comma = comma
	r.FieldsPerRecord = -1 // rows of unequal width are common and still readable
	r.LazyQuotes = true    // a lone quote is a typo, not a reason to read nothing
	recs, err := r.ReadAll()
	if err != nil {
		return d, fmt.Errorf("parse csv: %w", err)
	}

	kv := map[string]any{"format": "csv"}
	if comma == '\t' {
		kv["format"] = "tsv"
	}
	if len(recs) == 0 {
		return with(d, "", kv), nil
	}

	var fields []string
	if c.Bare {
		fields = numbers(wide(recs))
	} else {
		fields, recs = head(recs[0]), recs[1:]
	}

	var (
		b    buf
		secs []Section
		rows = make([]map[string]string, 0, len(recs))
	)
	for _, rec := range recs {
		row := make(map[string]string, len(fields))
		for _, f := range fields {
			row[f] = ""
		}
		start := b.len()
		for i, v := range rec {
			f := field(fields, i)
			row[f] = v
			if strings.TrimSpace(v) == "" {
				continue
			}
			b.put(f)
			b.raw(": ")
			b.put(v)
			b.nl()
		}
		rows = append(rows, row)
		if b.len() > start {
			secs = append(secs, Section{Start: start, End: b.len()})
		}
	}
	b.trim()

	kv["fields"] = fields
	kv["data"] = rows
	if secs = tile(secs, b.len()); len(secs) > 0 {
		kv["sections"] = secs
	}
	return with(d, b.text(), kv), nil
}

// head names the columns from the header row, dropping the byte order mark a
// spreadsheet leaves on the first cell and numbering the cells left blank.
func head(rec []string) []string {
	out := make([]string, len(rec))
	for i, s := range rec {
		out[i] = strings.TrimSpace(strings.TrimPrefix(s, "\ufeff"))
		if out[i] == "" {
			out[i] = strconv.Itoa(i + 1)
		}
	}
	return out
}

// field names column i, numbering the columns the header did not reach.
func field(fields []string, i int) string {
	if i < len(fields) {
		return fields[i]
	}
	return strconv.Itoa(i + 1)
}

// numbers names n columns by position, which is all a document without a
// header row says about them.
func numbers(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = strconv.Itoa(i + 1)
	}
	return out
}

// wide is the width of the widest row, which is how many columns a document
// without a header has.
func wide(recs [][]string) int {
	n := 0
	for _, r := range recs {
		n = max(n, len(r))
	}
	return n
}

// sep reports the separator of delimited text: the candidate that occurs the
// same number of times, at least once, on each of the first two lines. It
// returns zero when none does, which is what prose looks like.
func sep(s string) rune {
	before, after, ok := strings.Cut(s, "\n")
	if !ok {
		return 0
	}
	one, rest := before, after
	two := rest
	if before, _, ok := strings.Cut(rest, "\n"); ok {
		two = before
	}
	if strings.TrimSpace(two) == "" {
		return 0
	}
	for _, c := range []rune{',', '\t', ';'} {
		if n := strings.Count(one, string(c)); n > 0 && n == strings.Count(two, string(c)) {
			return c
		}
	}
	return 0
}
