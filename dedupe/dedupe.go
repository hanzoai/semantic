// Package dedupe decides which records are the same thing and which
// disagree.
//
// Both questions start from the same comparison, so they live together. When
// two records agree they merge, and the merge keeps the records it was made
// from. When they disagree the disagreement is a Conflict: a value the caller
// can read, rank and settle. Nothing is dropped for being unresolvable — an
// unsettled Conflict is a result, not an error.
//
// Work that is quadratic in the input takes a context and can be cancelled;
// linear work does not, so the common calls stay short.
package dedupe

import (
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/hanzoai/semantic"
)

// ErrOption reports an option outside its allowed range.
var ErrOption = errors.New("bad option")

// ErrEmpty reports a merge with nothing to merge.
var ErrEmpty = errors.New("no entities")

// Source is the document a record came from and how far it is to be trusted.
// Every value in this package carries one, because a claim without the
// document that made it cannot be ranked against a claim that disagrees.
type Source struct {
	Doc   string    // the document
	Page  int       // page within it; 0 when unpaged
	Span  string    // section or heading within the page
	At    time.Time // when the document stated it; zero when unstated
	Score float64   // the document's own confidence, 0 to 1; 0 means unstated
}

// sure is the confidence a source states, or half where it states none —
// the benefit of the doubt an unrated document gets.
func sure(s Source) float64 {
	if s.Score > 0 {
		return s.Score
	}
	return 0.5
}

// Entity is one record about one thing, as a single source stated it. The
// same thing described by two documents is two Entity values sharing an ID
// and differing somewhere else — which is what a Conflict is made of.
type Entity struct {
	ID     string
	Name   string
	Kind   string // what it is: Person, Company, Place
	Props  map[string]any
	Edges  []semantic.Triple
	Vector []float32
	Score  float64 // confidence in this record; 0 means unstated
	From   Source  // the document it came from
	Meta   map[string]any
}

// Value returns the property named by prop. Props are searched first, then
// the named fields, so "name" and "kind" read as properties even though
// they have their own place on the struct.
func (e Entity) Value(prop string) (any, bool) {
	if v, ok := e.Props[prop]; ok {
		return v, true
	}
	switch strings.ToLower(prop) {
	case "name":
		return e.Name, e.Name != ""
	case "kind", "type", "class":
		return e.Kind, e.Kind != ""
	}
	return nil, false
}

// key identifies an entity across calls. IDs are the identity; a record with
// no ID falls back to its name, which is the only other thing it has.
func (e Entity) key() string {
	if e.ID != "" {
		return e.ID
	}
	return fold(e.Name)
}

// size counts how much an entity carries, which is how "most complete" is
// decided.
func (e Entity) size() int { return len(e.Props) + len(e.Edges) }

// Canon folds a triple to the form comparisons are made on: the predicate
// mapped through a synonym table, and, when Fold is set, the predicate and
// object reduced to their words — lower case, single spaces, underscores and
// hyphens read as spaces, so works_at and "Works At" are one relation. The
// zero value folds nothing but case in the predicate.
type Canon struct {
	Synonyms map[string]string
	Fold     bool
}

// Key returns the triple as it is compared: subject, canonical predicate,
// canonical object.
func (c Canon) Key(t semantic.Triple) [3]string {
	p := strings.ToLower(t.Predicate)
	if c.Fold {
		p = strings.Join(strings.FieldsFunc(p, split), " ")
	}
	if s, ok := c.Synonyms[p]; ok {
		p = strings.ToLower(s)
	}
	o := t.Object
	if c.Fold {
		o = fold(o)
	}
	return [3]string{t.Subject, p, o}
}

// fold reduces a string to the form names are compared in: lower case, with
// leading, trailing and repeated whitespace gone. Two spellings that fold
// alike are one name.
func fold(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

// same reports whether two property values are equal. It uses reflection so
// that maps and slices compare by content rather than panicking.
func same(a, b any) bool { return reflect.DeepEqual(a, b) }
