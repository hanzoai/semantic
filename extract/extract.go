// Package extract turns text into entities, the relations between them, and
// the triples those relations assert.
//
// Two extractors implement the same stage. Rules needs nothing but the text:
// regexes, a gazetteer and relation patterns, all deterministic and all
// testable without a network. LLM asks a Model — the one thing this package
// does not compute itself — and does the work around the call: building the
// prompt, recovering JSON from whatever the model wrapped it in, and binding
// the answer back to the entities already known.
//
// A Schema gates either extractor's output against a domain ontology, and a
// Floor gates it on confidence. The two are orthogonal — one asks "is this in
// the vocabulary", the other "is this sure enough" — so they share the two
// entry points of Test and a caller runs both.
package extract

import (
	"context"
	"errors"

	"github.com/hanzoai/semantic"
)

// Entity is a labelled span of the text it was found in. Start and End are
// byte offsets, so text[Start:End] is the span. Score is the extractor's
// confidence in [0,1], and zero when it gave none: no score is ever made up.
type Entity struct {
	Text  string
	Label string
	Start int
	End   int
	Score float64
	Meta  map[string]any
}

// Relation joins two entities with a predicate. Context is the surrounding
// text the relation was read from, which is what makes it checkable later.
type Relation struct {
	Subject   Entity
	Predicate string
	Object    Entity
	Score     float64
	Context   string
	Meta      map[string]any
}

// Set is one pass over a text: the entities found and the relations among
// them. It is what both extractors return before the pipeline flattens it to
// triples, and what both checks read.
type Set struct {
	Entities  []Entity
	Relations []Relation
}

// Triples flattens the relations into pipeline triples, attributing each to
// the chunk it came from.
func (s Set) Triples(c semantic.Chunk) []semantic.Triple {
	out := make([]semantic.Triple, 0, len(s.Relations))
	for _, r := range s.Relations {
		out = append(out, semantic.Triple{
			Subject:   r.Subject.Text,
			Predicate: r.Predicate,
			Object:    r.Object.Text,
			From:      c,
			Score:     r.Score,
		})
	}
	return out
}

// Model is the completion this package needs and does not provide. The schema
// argument is the Kind being asked for; an adapter may turn Kind.JSON into
// whatever structured-output format its API wants, or ignore it and let the
// prompt do the work.
type Model interface {
	Complete(ctx context.Context, prompt string, schema any) (string, error)
}

// Kind is which extraction a prompt asks for. Its value is the JSON key the
// reply must use.
type Kind string

// The three extractions a prompt can ask for.
const (
	Ents Kind = "entities"
	Rels Kind = "relations"
	Tris Kind = "triplets"
)

// JSON is the JSON Schema of the reply this Kind demands, for an adapter that
// can constrain its model's output.
func (k Kind) JSON() string {
	switch k {
	case Ents:
		return entSchema
	case Rels:
		return relSchema
	case Tris:
		return triSchema
	}
	return ""
}

// ErrInvalid is what Report.Err wraps, so a caller can tell a rejected
// extraction from a transport failure with errors.Is.
var ErrInvalid = errors.New("extraction rejected")

// ErrParse reports that a model reply held no usable JSON.
var ErrParse = errors.New("no JSON in model reply")

const entSchema = `{"type":"object","required":["entities"],"properties":{"entities":{"type":"array","items":{"type":"object","required":["text","label"],"properties":{"text":{"type":"string"},"label":{"type":"string"},"confidence":{"type":"number"}}}}}}`

const relSchema = `{"type":"object","required":["relations"],"properties":{"relations":{"type":"array","items":{"type":"object","required":["subject","predicate","object"],"properties":{"subject":{"type":"string"},"predicate":{"type":"string"},"object":{"type":"string"},"confidence":{"type":"number"},"valid_from":{"type":["string","null"]},"valid_until":{"type":["string","null"]},"temporal_confidence":{"type":"number"},"temporal_source_text":{"type":["string","null"]}}}}}}`

const triSchema = `{"type":"object","required":["triplets"],"properties":{"triplets":{"type":"array","items":{"type":"object","required":["subject","predicate","object"],"properties":{"subject":{"type":"string"},"predicate":{"type":"string"},"object":{"type":"string"},"confidence":{"type":"number"}}}}}}`
