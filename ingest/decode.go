package ingest

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"sort"
	"strings"

	"github.com/hanzoai/semantic"
)

// Decoder turns the bytes of one source into documents. Sources read
// bytes; decoders decide how many documents those bytes are. Every reader in
// this package takes one, so the same JSON or CSV handling serves a file, a
// fetch and standard input.
type Decoder interface {
	Decode(b []byte, o Origin) ([]semantic.Doc, error)
}

// decode returns d, or the decoder that suits the origin's type when d is nil.
func decode(d Decoder, o Origin) Decoder {
	if d != nil {
		return d
	}
	return pick(o.Type)
}

// pick maps a source type to the decoder that reads it. Anything unrecognised
// is read as text.
func pick(typ string) Decoder {
	switch strings.ToLower(typ) {
	case "json":
		return JSON{}
	case "jsonl", "ndjson":
		return Lines{}
	case "csv":
		return Rows{}
	case "tsv", "tab":
		return Rows{Sep: '\t'}
	case "html", "htm", "xhtml":
		return HTML{}
	}
	return Raw{}
}

// Raw reads the bytes as one document of text.
type Raw struct{}

// Decode returns a single document holding the whole source.
func (Raw) Decode(b []byte, o Origin) ([]semantic.Doc, error) {
	return []semantic.Doc{doc(o, -1, text(b))}, nil
}

// JSON reads a JSON document: an array becomes one document per element,
// anything else becomes one document.
type JSON struct {
	// Field names the string member that holds a record's text. When it is
	// empty the whole record is the text, as compact JSON.
	Field string
}

// Decode parses b as JSON and turns it into documents.
func (j JSON) Decode(b []byte, o Origin) ([]semantic.Doc, error) {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("ingest: %s: %w", o.Ref, err)
	}
	if list, ok := v.([]any); ok {
		out := make([]semantic.Doc, 0, len(list))
		for i, e := range list {
			d, err := entry(o, i, e, j.Field)
			if err != nil {
				return nil, fmt.Errorf("ingest: %s: element %d: %w", o.Ref, i, err)
			}
			out = append(out, d)
		}
		return out, nil
	}
	d, err := entry(o, -1, v, j.Field)
	if err != nil {
		return nil, fmt.Errorf("ingest: %s: %w", o.Ref, err)
	}
	return []semantic.Doc{d}, nil
}

// Lines reads JSON Lines: one JSON value per line, one document per value.
// Blank lines are skipped. Each document's offset is the byte offset of its
// line, so a document can be traced back to the exact bytes it came from.
type Lines struct {
	// Field names the string member that holds a record's text, as in JSON.
	Field string
}

// Decode parses b as JSON Lines.
func (l Lines) Decode(b []byte, o Origin) ([]semantic.Doc, error) {
	var out []semantic.Doc
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var at int64
	for n := 0; sc.Scan(); {
		raw := sc.Bytes()
		start := at
		at += int64(len(raw)) + 1
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("ingest: %s: offset %d: %w", o.Ref, start, err)
		}
		p := o
		p.Offset = start
		d, err := entry(p, n, v, l.Field)
		if err != nil {
			return nil, fmt.Errorf("ingest: %s: offset %d: %w", o.Ref, start, err)
		}
		out = append(out, d)
		n++
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("ingest: %s: %w", o.Ref, err)
	}
	return out, nil
}

// Rows reads delimited text — CSV, TSV, anything with one record per line —
// into one document per row.
//
// A source that is not valid UTF-8 is read as Latin-1, as the Python this
// ports does after sniffing the encoding; offsets then refer to the decoded
// text rather than the source bytes.
type Rows struct {
	// Sep is the field delimiter. Zero sniffs the header line for one of
	// comma, semicolon, tab or pipe.
	Sep rune
	// Cols names the columns; a column past the end of Cols, or named by an
	// empty entry, is named by its index. When Cols is empty the first row
	// is the header.
	Cols []string
	// Field names the column that holds a row's text. When it is empty the
	// text is every column as "name: value" lines.
	Field string
}

// Decode parses b as delimited text.
func (r Rows) Decode(b []byte, o Origin) ([]semantic.Doc, error) {
	sep := r.Sep
	if sep == 0 {
		sep = sniff(b)
	}
	rd := csv.NewReader(strings.NewReader(text(b)))
	rd.Comma = sep
	rd.FieldsPerRecord = -1 // ragged rows are data, not a parse failure
	rd.LazyQuotes = true

	cols := r.Cols
	if len(cols) == 0 {
		head, err := rd.Read()
		if err == io.EOF {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("ingest: %s: header: %w", o.Ref, err)
		}
		cols = head
	}

	var out []semantic.Doc
	for n := 0; ; n++ {
		start := rd.InputOffset()
		row, err := rd.Read()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("ingest: %s: row %d: %w", o.Ref, n, err)
		}
		rec := make(map[string]any, len(row))
		for i, cell := range row {
			rec[name(cols, i)] = cell
		}
		p := o
		p.Offset = start
		d := doc(p, n, cell(rec, r.Field))
		d.Meta[keyRecord] = rec
		out = append(out, d)
	}
}

// HTML reads a page as text: script and style contents are dropped, tags
// become spaces, entities are unescaped, and the title is kept in Doc.Meta.
type HTML struct{}

// Decode strips b of markup.
func (HTML) Decode(b []byte, o Origin) ([]semantic.Doc, error) {
	body, title := strip(text(b))
	d := doc(o, -1, body)
	if title != "" {
		d.Meta[keyTitle] = title
	}
	return []semantic.Doc{d}, nil
}

// Title returns the page title HTML recorded, or "".
func Title(d semantic.Doc) string {
	t, _ := d.Meta[keyTitle].(string)
	return t
}

// entry turns one decoded JSON value into a document. An object keeps its
// fields in Doc.Meta so a later stage can read the record, not just the text.
func entry(o Origin, n int, v any, field string) (semantic.Doc, error) {
	switch t := v.(type) {
	case map[string]any:
		d := doc(o, n, cell(t, field))
		d.Meta[keyRecord] = t
		return d, nil
	case string:
		return doc(o, n, t), nil
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return semantic.Doc{}, err
		}
		return doc(o, n, string(b)), nil
	}
}

// cell renders a record as the text of one document: the named field when it
// holds a string, otherwise every field as "name: value" lines in a stable
// order.
func cell(rec map[string]any, field string) string {
	if field != "" {
		if s, ok := rec[field].(string); ok {
			return s
		}
	}
	keys := make([]string, 0, len(rec))
	for k := range rec {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%s: %v", k, rec[k])
	}
	return b.String()
}

// name is the column name at index i, "N" when the header is short.
func name(cols []string, i int) string {
	if i < len(cols) && cols[i] != "" {
		return cols[i]
	}
	return fmt.Sprint(i)
}

// sniff picks the delimiter that occurs most often in the first line, from
// comma, semicolon, tab and pipe. Comma when nothing separates anything.
func sniff(b []byte) rune {
	line := b
	if before, _, ok := bytes.Cut(b, []byte{'\n'}); ok {
		line = before
	}
	best, top := ',', 0
	for _, c := range []rune{',', ';', '\t', '|'} {
		if n := bytes.Count(line, []byte(string(c))); n > top {
			best, top = c, n
		}
	}
	return best
}

// first is a tag's name: the leading word of what stood between < and >.
func first(tag string) string {
	f := strings.Fields(tag)
	if len(f) == 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSuffix(f[0], "/"))
}

// strip removes markup from an HTML page, returning its text and its title.
func strip(src string) (body, title string) {
	var out, head strings.Builder
	var tag strings.Builder
	inTag, inTitle, space := false, false, false
	skip := "" // element whose contents are dropped: script or style
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case c == '<' && strings.HasPrefix(src[i:], "<!--"):
			end := strings.Index(src[i+4:], "-->")
			if end < 0 {
				return html.UnescapeString(out.String()),
					strings.TrimSpace(html.UnescapeString(head.String()))
			}
			i += 4 + end + 2
			space = true
		case c == '<':
			inTag, space = true, true
			tag.Reset()
		case c == '>' && inTag:
			inTag = false
			t := first(tag.String())
			switch {
			case skip != "" && t == "/"+skip:
				skip = ""
			case skip != "":
			case t == "script" || t == "style":
				skip = t
			case t == "title":
				inTitle = true
			case t == "/title":
				inTitle = false
			}
		case inTag:
			tag.WriteByte(c)
		case skip != "":
		case inTitle:
			head.WriteByte(c)
		default:
			if c == '\n' || c == '\r' || c == '\t' || c == ' ' {
				space = true
				continue
			}
			if space && out.Len() > 0 {
				out.WriteByte(' ')
			}
			space = false
			out.WriteByte(c)
		}
	}
	return html.UnescapeString(out.String()),
		strings.TrimSpace(html.UnescapeString(head.String()))
}
