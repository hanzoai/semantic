// Package ontology is the type system a graph is read through: classes,
// properties, the domains and ranges that connect them, and the subclass
// hierarchy over both. It infers a schema from observed data, checks data
// against a schema, and reads and writes the interchange formats — JSON-LD,
// JSON Schema, Turtle and N-Triples.
package ontology

import (
	"errors"
	"maps"
	"sort"
	"strings"
)

// ErrCollision reports two source names that normalize to one term, which
// would silently merge two different things into one.
var ErrCollision = errors.New("name collision")

// Kind separates the two sorts of property: one whose values are other
// entities, one whose values are literals.
type Kind string

const (
	// Object is a property whose values are entities.
	Object Kind = "object"
	// Data is a property whose values are literals.
	Data Kind = "data"
	// Annotation is a property that describes a term rather than constraining
	// its instances, such as a label or an editorial note.
	Annotation Kind = "annotation"
)

// ParseKind reads the spellings other vocabularies use for a property kind.
// An unrecognised spelling is returned unchanged so a caller can report it.
func ParseKind(s string) Kind {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "object", "objectproperty", "object_property", "owl:objectproperty", "relationship":
		return Object
	case "data", "datatype", "dataproperty", "datatypeproperty", "data_property", "owl:datatypeproperty", "literal":
		return Data
	case "annotation", "annotationproperty", "owl:annotationproperty":
		return Annotation
	}
	return Kind(strings.TrimSpace(s))
}

// Class is one type in the schema.
type Class struct {
	Name    string
	IRI     string
	Label   string
	Comment string
	Parent  string   // the class this one specialises, by name
	Attrs   []string // attribute names shared by the instances observed
	Seen    int      // instances observed
	From    string   // the source type this class was inferred from
}

// Property is one relation in the schema. An Object property ranges over
// classes, a Data property over datatypes.
type Property struct {
	Name    string
	IRI     string
	Kind    Kind
	Label   string
	Comment string
	Domain  []string // classes the property applies to
	Range   []string // classes for Object, datatypes for Data
	Min     *int     // fewest values allowed; nil leaves the count open
	Max     *int     // most values allowed; nil leaves the count open
	One     []string // the only values allowed
	Pattern string   // regular expression every value must match
	Seen    int      // observations behind the inference
}

// Count returns a pointer to n, for setting Min and Max inline. A nil Min or
// Max leaves that end of the count open, which is not the same as a count of
// zero.
//
//go:fix inline
func Count(n int) *int { return new(n) }

// Schema is a whole ontology: the terms and the namespace they live in.
type Schema struct {
	IRI        string
	Name       string
	Comment    string
	Version    string
	Base       string // namespace the terms live in
	Prefixes   map[string]string
	Classes    []Class
	Properties []Property
}

// Class returns the class of that name, and whether the schema has one.
func (s Schema) Class(name string) (Class, bool) {
	for _, c := range s.Classes {
		if c.Name == name {
			return c, true
		}
	}
	return Class{}, false
}

// Property returns the property of that name, and whether the schema has one.
func (s Schema) Property(name string) (Property, bool) {
	for _, p := range s.Properties {
		if p.Name == name {
			return p, true
		}
	}
	return Property{}, false
}

// Ancestors lists the classes a class specialises, nearest parent first. A
// cycle in the hierarchy stops the walk rather than hanging it.
func (s Schema) Ancestors(name string) []string {
	byName := make(map[string]Class, len(s.Classes))
	for _, c := range s.Classes {
		byName[c.Name] = c
	}
	var out []string
	seen := map[string]bool{name: true}
	for cur := byName[name].Parent; cur != "" && !seen[cur]; cur = byName[cur].Parent {
		seen[cur] = true
		out = append(out, cur)
		if _, ok := byName[cur]; !ok {
			break
		}
	}
	return out
}

// Normalize returns the schema with duplicate terms dropped and the IRIs,
// labels and endpoints left blank filled in. Terms keep their first
// occurrence and their order.
func (s Schema) Normalize() Schema {
	ns := Namespace{Base: s.Base, Version: s.Version}
	if ns.Base == "" {
		ns.Base = s.IRI
	}

	out := s
	out.Classes = nil
	seen := map[string]bool{}
	for _, c := range s.Classes {
		if c.Name == "" || seen[c.Name] {
			continue
		}
		seen[c.Name] = true
		if c.IRI == "" {
			c.IRI = ns.Class(c.Name)
		}
		if c.Label == "" {
			c.Label = c.Name
		}
		out.Classes = append(out.Classes, c)
	}

	out.Properties = nil
	seen = map[string]bool{}
	for _, p := range s.Properties {
		if p.Name == "" || seen[p.Name] {
			continue
		}
		seen[p.Name] = true
		if p.IRI == "" {
			p.IRI = ns.Property(p.Name)
		}
		if p.Label == "" {
			p.Label = p.Name
		}
		if len(p.Domain) == 0 {
			p.Domain = []string{"owl:Thing"}
		}
		if p.Kind == Object && len(p.Range) == 0 {
			p.Range = []string{"owl:Thing"}
		}
		out.Properties = append(out.Properties, p)
	}
	return out
}

// Entity is one observed instance: what it is, what it is called, and the
// attributes carried on it. Framework bookkeeping has no place in Attrs, so
// it cannot be mistaken for a datatype property.
type Entity struct {
	ID    string
	Type  string
	Name  string
	Attrs map[string]any
}

// Link is one observed relation between two entities. From and To name their
// endpoints by ID or by Name; FromType and ToType carry the endpoint classes
// when the source states them.
type Link struct {
	Type     string
	From     string
	To       string
	FromType string
	ToType   string
}

// Graph is an instance graph: what was observed, before any schema is imposed.
type Graph struct {
	Entities []Entity
	Links    []Link
}

// Standard are the prefixes every RDF document may assume.
var Standard = map[string]string{
	"rdf":     "http://www.w3.org/1999/02/22-rdf-syntax-ns#",
	"rdfs":    "http://www.w3.org/2000/01/rdf-schema#",
	"owl":     "http://www.w3.org/2002/07/owl#",
	"xsd":     "http://www.w3.org/2001/XMLSchema#",
	"skos":    "http://www.w3.org/2004/02/skos/core#",
	"sh":      "http://www.w3.org/ns/shacl#",
	"dc":      "http://purl.org/dc/elements/1.1/",
	"dcterms": "http://purl.org/dc/terms/",
}

// DefaultBase is the namespace terms are minted in when none is given.
const DefaultBase = "https://semantica.dev/ontology/"

// Namespace mints IRIs for terms and resolves prefixes. The zero value mints
// in DefaultBase and knows the standard prefixes.
type Namespace struct {
	Base    string
	Version string
	Bound   map[string]string // bindings beyond the standard ones
}

// Root is the namespace terms are minted in. A version other than the first
// is carried in the namespace, never in the term IRIs, so that a term keeps
// one identity across versions.
func (n Namespace) Root() string {
	base := n.Base
	if base == "" {
		base = DefaultBase
	}
	if n.Version != "" && n.Version != "1.0" {
		return strings.TrimRight(base, "/") + "/v" + n.Version + "/"
	}
	return base
}

// Class returns the IRI for a class name, in PascalCase.
func (n Namespace) Class(name string) string { return join(n.Root(), Pascal(name)) }

// Property returns the IRI for a property name, in camelCase.
func (n Namespace) Property(name string) string { return join(n.Root(), Camel(name)) }

// Individual returns the IRI for a named instance.
func (n Namespace) Individual(name string) string {
	var b strings.Builder
	for _, r := range name {
		if isWordRune(r) {
			b.WriteRune(r)
		}
	}
	return join(n.Root(), "individual/"+b.String())
}

// Bind records a prefix for an IRI.
func (n *Namespace) Bind(prefix, iri string) {
	if n.Bound == nil {
		n.Bound = map[string]string{}
	}
	n.Bound[prefix] = iri
}

// Resolve returns the IRI a prefix stands for. Standard prefixes resolve
// without being bound; a binding of the same name wins.
func (n Namespace) Resolve(prefix string) (string, bool) {
	if iri, ok := n.Bound[prefix]; ok {
		return iri, true
	}
	iri, ok := Standard[prefix]
	return iri, ok
}

// All returns every prefix in scope, standard and bound.
func (n Namespace) All() map[string]string {
	out := make(map[string]string, len(Standard)+len(n.Bound))
	maps.Copy(out, Standard)
	maps.Copy(out, n.Bound)
	return out
}

// join pastes a local name onto a namespace. A namespace that already ends in
// a separator keeps it; one that does not gets a hash, because a bare
// concatenation would run the two together.
func join(base, local string) string {
	if base == "" {
		base = DefaultBase
	}
	if strings.HasSuffix(base, "#") || strings.HasSuffix(base, "/") || strings.HasSuffix(base, ":") {
		return base + local
	}
	return base + "#" + local
}

// local returns the part of an IRI after the last separator, which is the
// term's name in the vocabulary that minted it.
func local(iri string) string {
	s := strings.TrimSpace(strings.Trim(iri, "<>"))
	if i := strings.LastIndexAny(s, "#/"); i >= 0 {
		return s[i+1:]
	}
	if i := strings.LastIndex(s, ":"); i >= 0 && !strings.Contains(s, "://") {
		return s[i+1:]
	}
	return s
}

// absolute reports whether an IRI carries a scheme, which is what makes it
// resolvable on its own.
func absolute(s string) bool {
	s = strings.TrimSpace(s)
	i := strings.Index(s, ":")
	if i <= 0 || i+1 >= len(s) {
		return false
	}
	if s[i+1] != '/' {
		return false
	}
	for j, r := range s[:i] {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case j > 0 && (r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.'):
		default:
			return false
		}
	}
	return true
}

func sorted(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
