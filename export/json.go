package export

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/hanzoai/semantic"
)

// record is an assertion as the formats that carry everything write it: the
// claim, the confidence it was given, and the span it was read from. A field
// left out is a field that was empty, so reading a record back gives the
// assertion that was written.
type record struct {
	Subject   string  `json:"subject"`
	Predicate string  `json:"predicate"`
	Object    string  `json:"object"`
	Score     float64 `json:"score,omitempty"`
	Doc       string  `json:"doc,omitempty"`
	Chunk     int     `json:"chunk,omitempty"`
	Text      string  `json:"text,omitempty"`
}

func rec(t semantic.Triple) record {
	return record{
		Subject:   t.Subject,
		Predicate: t.Predicate,
		Object:    t.Object,
		Score:     t.Score,
		Doc:       t.From.DocID,
		Chunk:     t.From.Index,
		Text:      t.From.Text,
	}
}

func (r record) triple() semantic.Triple {
	return semantic.Triple{
		Subject:   r.Subject,
		Predicate: r.Predicate,
		Object:    r.Object,
		Score:     r.Score,
		From:      semantic.Chunk{DocID: r.Doc, Index: r.Chunk, Text: r.Text},
	}
}

// JSON writes the graph as one array of assertions, one to a line so the
// output diffs, and carries every field.
//
// Indent pretty-prints each assertion by that much; unset, an assertion is one
// line. The array brackets are written as the walk starts and ends, so the
// document streams whichever way it is set.
type JSON struct{ Indent string }

// Write serializes src to w as a JSON array.
func (j JSON) Write(ctx context.Context, w io.Writer, src Source) error {
	p := ink(w)
	p.put("[")
	first := true
	err := src(ctx, func(t semantic.Triple) error {
		if p.err != nil {
			return p.err
		}
		if first {
			p.put("\n")
		} else {
			p.put(",\n")
		}
		first = false
		b, err := marshal(rec(t), j.Indent)
		if err != nil {
			return err
		}
		p.put(j.Indent, string(b))
		return p.err
	})
	if err != nil {
		return err
	}
	if !first {
		p.put("\n")
	}
	p.put("]\n")
	return p.done()
}

// marshal writes v as JSON, leaving <, > and & as they are: these bytes are
// data, and the escaping Go does by default is for embedding JSON in a page.
func marshal(v any, indent string) ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if indent != "" {
		e.SetIndent(indent, indent)
	}
	if err := e.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// Read parses a JSON array of assertions. It decodes one element at a time,
// so a file larger than memory reads as well as it writes.
func (j JSON) Read(r io.Reader) Source {
	return once(func(ctx context.Context, yield func(semantic.Triple) error) error {
		d := json.NewDecoder(r)
		open, err := d.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("json: %w", err)
		}
		if open != json.Delim('[') {
			return fmt.Errorf("json: want an array, got %v", open)
		}
		for d.More() {
			if err := ctx.Err(); err != nil {
				return err
			}
			var rd record
			if err := d.Decode(&rd); err != nil {
				return fmt.Errorf("json: %w", err)
			}
			if err := yield(rd.triple()); err != nil {
				return err
			}
		}
		if _, err := d.Token(); err != nil {
			return fmt.Errorf("json: %w", err)
		}
		return nil
	})
}

// NDJSON writes one assertion per line, each a complete JSON object. It is
// the format to reach for when the graph is large or the reader is a pipe:
// nothing has to be held, and a line is a whole record.
type NDJSON struct{}

// Write serializes src to w, one JSON object per line.
func (NDJSON) Write(ctx context.Context, w io.Writer, src Source) error {
	p := ink(w)
	err := src(ctx, func(t semantic.Triple) error {
		if p.err != nil {
			return p.err
		}
		b, err := marshal(rec(t), "")
		if err != nil {
			return err
		}
		p.put(string(b), "\n")
		return p.err
	})
	if err != nil {
		return err
	}
	return p.done()
}

// Read parses one assertion per line. A blank line is skipped, so a file that
// ends in a newline reads as the records it holds.
func (NDJSON) Read(r io.Reader) Source {
	return once(func(ctx context.Context, yield func(semantic.Triple) error) error {
		s := bufio.NewScanner(r)
		s.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
		for n := 1; s.Scan(); n++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			line := s.Bytes()
			if len(trim(line)) == 0 {
				continue
			}
			var rd record
			if err := json.Unmarshal(line, &rd); err != nil {
				return fmt.Errorf("ndjson line %d: %w", n, err)
			}
			if err := yield(rd.triple()); err != nil {
				return err
			}
		}
		if err := s.Err(); err != nil {
			return fmt.Errorf("ndjson: %w", err)
		}
		return nil
	})
}

// trim drops leading and trailing ASCII space from a line.
func trim(b []byte) []byte {
	i, j := 0, len(b)
	for i < j && (b[i] == ' ' || b[i] == '\t' || b[i] == '\r' || b[i] == '\n') {
		i++
	}
	for j > i && (b[j-1] == ' ' || b[j-1] == '\t' || b[j-1] == '\r' || b[j-1] == '\n') {
		j--
	}
	return b[i:j]
}

// JSONLD writes the graph as JSON-LD: a context naming the namespace and a
// graph of nodes, one per assertion.
//
// One node per assertion rather than one per subject, because grouping by
// subject means holding the graph. A JSON-LD reader merges the nodes back by
// @id, so the document says the same thing either way. Being RDF, it carries
// the assertion and not the confidence or the span it came from.
type JSONLD struct{ Base string }

// Write serializes src to w as a JSON-LD document.
func (j JSONLD) Write(ctx context.Context, w io.Writer, src Source) error {
	b := ns(j.Base)
	p := ink(w)
	p.put("{\n  \"@context\": {\"sem\": ", quote(b), "},\n  \"@graph\": [")
	n := 0
	err := src(ctx, func(t semantic.Triple) error {
		if p.err != nil {
			return p.err
		}
		if err := whole(t, n); err != nil {
			return err
		}
		if n > 0 {
			p.put(",")
		}
		p.put("\n")
		n++
		p.put("    {\"@id\": ", quote(curie(b, t.Subject)),
			", ", quote(curie(b, t.Predicate)),
			": {\"@id\": ", quote(curie(b, t.Object)), "}}")
		return p.err
	})
	if err != nil {
		return err
	}
	if n > 0 {
		p.put("\n")
	}
	p.put("  ]\n}\n")
	return p.done()
}

// curie is a term as JSON-LD names it: minted against base and shortened to
// the sem prefix where the local part is one a reader will split back off.
func curie(base, term string) string {
	full := iri(base, term)
	if local := short(base, full); local != "" {
		return "sem:" + local
	}
	return full
}

// quote writes v as a JSON string. Marshalling a string cannot fail.
func quote(v string) string {
	b, _ := marshal(v, "")
	return string(b)
}
