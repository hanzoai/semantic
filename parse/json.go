package parse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/hanzoai/semantic"
)

// JSON reads JSON and, without being told, the line-delimited form: it decodes
// values until the input runs out, so one object and a stream of records are
// the same code path. The decoded value goes in Meta as data; the text becomes
// one "path: value" line per leaf, which is what makes a JSON document
// something a splitter and an extractor can work on at all. Each record of a
// stream is a section.
//
// Numbers keep the digits they were written with rather than becoming floats.
type JSON struct{}

// Parse decodes the document and renders its leaves as text.
func (JSON) Parse(_ context.Context, d semantic.Doc) (semantic.Doc, error) {
	dec := json.NewDecoder(strings.NewReader(d.Text))
	dec.UseNumber()

	var vals []any
	for {
		var v any
		switch err := dec.Decode(&v); {
		case errors.Is(err, io.EOF):
			return build(d, vals), nil
		case err != nil:
			return d, fmt.Errorf("parse json: %w", err)
		}
		vals = append(vals, v)
	}
}

// build renders the decoded values and records them.
func build(d semantic.Doc, vals []any) semantic.Doc {
	kv := map[string]any{"format": "json"}
	switch len(vals) {
	case 0:
		return with(d, "", kv)
	case 1:
		kv["data"] = vals[0]
		return with(d, render(vals[0]), kv)
	}

	var (
		b    buf
		secs []Section
	)
	for _, v := range vals {
		start := b.len()
		b.raw(render(v))
		b.nl()
		secs = append(secs, Section{Start: start, End: b.len()})
	}
	b.trim()
	kv["format"] = "jsonl"
	kv["data"] = vals
	kv["sections"] = tile(secs, b.len())
	return with(d, b.text(), kv)
}

// render writes one value as "path: value" lines, one per leaf.
func render(v any) string {
	flat := Flatten(v)
	keys := make([]string, 0, len(flat))
	for k := range flat {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, order)

	var b buf
	for _, k := range keys {
		if k != "" {
			b.raw(k)
			b.raw(": ")
		}
		b.put(text(flat[k]))
		b.nl()
	}
	b.trim()
	return b.text()
}

// Flatten reduces a decoded JSON value to its leaves, keyed by path: object
// keys and array indices joined by ".". An empty object or array has no
// leaves and so contributes nothing.
func Flatten(v any) map[string]any {
	out := map[string]any{}
	flat(v, "", out)
	return out
}

func flat(v any, at string, out map[string]any) {
	join := func(k string) string {
		if at == "" {
			return k
		}
		return at + "." + k
	}
	switch t := v.(type) {
	case map[string]any:
		for k, v := range t {
			flat(v, join(k), out)
		}
	case []any:
		for i, v := range t {
			flat(v, join(strconv.Itoa(i)), out)
		}
	default:
		out[at] = v
	}
}

// order sorts paths so that array indices compare as numbers: a plain string
// sort puts "a.10" before "a.2", which reads as a shuffled document.
func order(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		if as[i] == bs[i] {
			continue
		}
		an, aerr := strconv.Atoi(as[i])
		bn, berr := strconv.Atoi(bs[i])
		if aerr == nil && berr == nil {
			return an - bn
		}
		return strings.Compare(as[i], bs[i])
	}
	return len(as) - len(bs)
}

// text writes one decoded leaf the way it was written in the document.
func text(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64)
	default:
		return fmt.Sprint(v)
	}
}
