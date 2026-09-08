package extract

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Schema is a read-only view over a domain ontology: the concepts an entity
// label may take and the predicates a relation may use, with the concepts each
// predicate joins. It is what makes extraction schema-guided — the deterministic
// half of ontology-based information extraction, needing no model.
//
// Names match exactly. An ontology generator that writes concepts in PascalCase
// and predicates in camelCase expects labels in the same convention, so a
// schema built from one rejects "person" where it allows "Person".
//
// The zero Schema has no concepts and no predicates, so it allows nothing.
type Schema struct {
	Concepts   []string // sorted
	Predicates map[string]Predicate
}

// Predicate is an allowed predicate and the concepts it may join. An empty
// Domain or Range means any concept, following OWL: a property with no
// rdfs:domain restricts nothing.
type Predicate struct {
	Name          string
	Domain, Range []string // sorted; empty means unconstrained
}

// Ontology is the vocabulary a Schema reads. It is the shape an ontology
// generator emits: named classes, and named properties that may name the
// classes they join.
type Ontology struct {
	Classes    []Class    `json:"classes"`
	Properties []Property `json:"properties"`
}

// Class is one concept. Label stands in when Name is absent.
type Class struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}

// Property is one predicate and the concepts it joins.
type Property struct {
	Name   string `json:"name"`
	Label  string `json:"label"`
	Domain Names  `json:"domain"`
	Range  Names  `json:"range"`
}

// Names is a domain or range constraint. Generators write it as a bare string,
// a list of strings, or an object carrying a name, so it reads all three.
type Names []string

// UnmarshalJSON reads a constraint written as a string, a list, or an object.
func (n *Names) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*n = flatten(v)
	return nil
}

// flatten reduces whatever a generator wrote to the concept names it meant.
func flatten(v any) []string {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		if t == "" {
			return nil
		}
		return []string{t}
	case []any:
		var out []string
		for _, item := range t {
			out = append(out, flatten(item)...)
		}
		return out
	case map[string]any:
		for _, key := range []string{"name", "label"} {
			if s, ok := t[key].(string); ok && s != "" {
				return []string{s}
			}
		}
		var out []string
		for k := range t {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	default:
		return []string{fmt.Sprint(t)}
	}
}

// thing is the universal class. A generator emits it when it cannot resolve an
// endpoint's type, so it means "any concept", not a concept literally named
// Thing.
var thing = map[string]bool{
	"owl:Thing":                           true,
	"Thing":                               true,
	"http://www.w3.org/2002/07/owl#Thing": true,
}

// constraint normalises one domain or range: sorted, deduplicated, and empty
// when it names the universal class.
func constraint(n Names) []string {
	seen := map[string]bool{}
	var out []string
	for _, name := range n {
		if name == "" {
			continue
		}
		if thing[name] {
			return nil
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// NewSchema builds a Schema from an ontology. Concepts come from the classes
// and from every type a property names as an endpoint, so a type that appears
// only as a range is still known — a class-frequency cutoff during induction
// should not make a property's own range off-vocabulary.
func NewSchema(o Ontology) Schema {
	seen := map[string]bool{}
	add := func(names ...string) {
		for _, n := range names {
			if n != "" {
				seen[n] = true
			}
		}
	}
	for _, c := range o.Classes {
		if c.Name != "" {
			add(c.Name)
		} else {
			add(c.Label)
		}
	}

	preds := map[string]Predicate{}
	for _, p := range o.Properties {
		name := p.Name
		if name == "" {
			name = p.Label
		}
		if name == "" {
			continue
		}
		dom, rng := constraint(p.Domain), constraint(p.Range)
		preds[name] = Predicate{Name: name, Domain: dom, Range: rng}
		add(dom...)
		add(rng...)
	}

	concepts := make([]string, 0, len(seen))
	for c := range seen {
		concepts = append(concepts, c)
	}
	sort.Strings(concepts)
	return Schema{Concepts: concepts, Predicates: preds}
}

// ReadSchema builds a Schema from an ontology written as JSON.
func ReadSchema(r io.Reader) (Schema, error) {
	var o Ontology
	if err := json.NewDecoder(r).Decode(&o); err != nil {
		return Schema{}, fmt.Errorf("read ontology: %w", err)
	}
	return NewSchema(o), nil
}

// Concept reports whether name is an allowed entity label.
func (s Schema) Concept(name string) bool { return has(s.Concepts, name) }

// Pred reports whether name is an allowed predicate.
func (s Schema) Pred(name string) bool {
	_, ok := s.Predicates[name]
	return ok
}

// Allows reports whether a relation conforms: both endpoints are known
// concepts, the predicate is known, and the endpoints satisfy its domain and
// range. Membership is exact — subClassOf is not followed, so a subclass
// endpoint does not satisfy a superclass domain.
func (s Schema) Allows(subject, predicate, object string) bool {
	if !s.Concept(subject) || !s.Concept(object) {
		return false
	}
	p, ok := s.Predicates[predicate]
	if !ok {
		return false
	}
	if len(p.Domain) > 0 && !has(p.Domain, subject) {
		return false
	}
	if len(p.Range) > 0 && !has(p.Range, object) {
		return false
	}
	return true
}

// Check reports how much of an extraction conforms to the schema: every entity
// label must be a concept, and every relation must satisfy Allows.
func (s Schema) Check(x Set) Report {
	r := Report{Counts: map[string]int{}}

	var unknown []string
	seen := map[string]bool{}
	bad := 0
	for _, e := range x.Entities {
		if s.Concept(e.Label) {
			continue
		}
		bad++
		if !seen[e.Label] {
			seen[e.Label] = true
			unknown = append(unknown, e.Label)
		}
	}
	sort.Strings(unknown)
	r.Unknown = unknown
	r.Counts["entities"] = len(x.Entities)
	r.Counts["known"] = len(x.Entities) - bad
	r.Counts["unknown"] = bad
	r.Counts["concepts"] = len(s.Concepts)
	if bad > 0 {
		r.Errs = append(r.Errs, fmt.Sprintf("%d entities labelled outside the schema: %s",
			bad, strings.Join(unknown, ", ")))
	}

	var torn, wrongPred, wrongEnds int
	var preds []string
	seenPred := map[string]bool{}
	for _, rel := range x.Relations {
		switch {
		case rel.Subject.Text == "" || rel.Object.Text == "":
			torn++
		case !s.Pred(rel.Predicate):
			wrongPred++
			if !seenPred[rel.Predicate] {
				seenPred[rel.Predicate] = true
				preds = append(preds, rel.Predicate)
			}
		case !s.Allows(rel.Subject.Label, rel.Predicate, rel.Object.Label):
			wrongEnds++
		}
	}
	sort.Strings(preds)
	if torn > 0 {
		r.Errs = append(r.Errs, fmt.Sprintf("%d relations missing a subject or object", torn))
	}
	if wrongPred > 0 {
		r.Errs = append(r.Errs, fmt.Sprintf("%d relations using predicates outside the schema: %s",
			wrongPred, strings.Join(preds, ", ")))
	}
	if wrongEnds > 0 {
		r.Errs = append(r.Errs, fmt.Sprintf("%d relations joining concepts their predicate does not allow", wrongEnds))
	}
	r.Counts["relations"] = len(x.Relations)
	r.Counts["conforming"] = len(x.Relations) - torn - wrongPred - wrongEnds
	r.Counts["torn"] = torn
	r.Counts["offVocabulary"] = wrongPred
	r.Counts["offDomain"] = wrongEnds
	r.Counts["predicates"] = len(s.Predicates)

	r.OK = len(r.Errs) == 0
	r.Score = share(r.Counts["known"], len(x.Entities), r.Counts["conforming"], len(x.Relations))
	return r
}

// Keep returns the conforming subset: entities whose label is a concept, and
// relations that satisfy Allows.
func (s Schema) Keep(x Set) Set {
	var out Set
	for _, e := range x.Entities {
		if s.Concept(e.Label) {
			out.Entities = append(out.Entities, e)
		}
	}
	for _, r := range x.Relations {
		if r.Subject.Text != "" && r.Object.Text != "" &&
			s.Pred(r.Predicate) && s.Allows(r.Subject.Label, r.Predicate, r.Object.Label) {
			out.Relations = append(out.Relations, r)
		}
	}
	return out
}

func has(list []string, name string) bool {
	for _, n := range list {
		if n == name {
			return true
		}
	}
	return false
}
