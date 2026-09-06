// Package semantic turns sources into a knowledge graph: ingest, parse,
// split, extract, then assert. Each stage is one interface with one job;
// a pipeline is their composition, so a caller may replace any one of them
// without touching the rest.
package semantic

import "context"

// Doc is one parsed source: its text and where the text came from.
type Doc struct {
	ID     string
	Source string
	Text   string
	Meta   map[string]any
}

// Chunk is a span of a Doc sized for extraction.
type Chunk struct {
	DocID string
	Index int
	Text  string
}

// Triple is one assertion an extractor found, with the span it came from.
// The span is what makes the assertion answerable later: a claim without
// its source is not evidence.
type Triple struct {
	Subject   string
	Predicate string
	Object    string
	From      Chunk
	Score     float64
}

// The stages. Each takes the previous stage's value and returns the next.
type (
	Ingester interface {
		Ingest(context.Context, string) ([]Doc, error)
	}
	Parser interface {
		Parse(context.Context, Doc) (Doc, error)
	}
	Splitter interface {
		Split(context.Context, Doc) ([]Chunk, error)
	}
	Extractor interface {
		Extract(context.Context, Chunk) ([]Triple, error)
	}
)

// Pipeline runs the stages in order. A nil stage is skipped, so a caller
// that only wants to split does not have to supply an extractor.
type Pipeline struct {
	Ingest  Ingester
	Parse   Parser
	Split   Splitter
	Extract Extractor
}

// Run carries one reference through every stage and returns what was asserted.
func (p Pipeline) Run(ctx context.Context, ref string) ([]Triple, error) {
	docs, err := p.Ingest.Ingest(ctx, ref)
	if err != nil {
		return nil, err
	}
	var out []Triple
	for _, d := range docs {
		if p.Parse != nil {
			if d, err = p.Parse.Parse(ctx, d); err != nil {
				return nil, err
			}
		}
		chunks := []Chunk{{DocID: d.ID, Text: d.Text}}
		if p.Split != nil {
			if chunks, err = p.Split.Split(ctx, d); err != nil {
				return nil, err
			}
		}
		if p.Extract == nil {
			continue
		}
		for _, c := range chunks {
			t, err := p.Extract.Extract(ctx, c)
			if err != nil {
				return nil, err
			}
			out = append(out, t...)
		}
	}
	return out, nil
}
