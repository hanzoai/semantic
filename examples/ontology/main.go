// Command ontology reads a schema off data that has already been observed,
// writes it out as OWL, and reads it back. That is the way round the package
// is mostly used: entities and links come out of extraction, and the type
// system that describes them is inferred rather than written by hand.
//
// Naming is not decoration here. A class is PascalCase and singular, a
// property is camelCase, and the schema mints an IRI for each — so two spellings
// of one concept become one term, and two concepts that would collide under
// normalization are reported rather than silently merged.
//
//	go run ./examples/ontology
package main

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/hanzoai/semantic/ontology"
)

// observed is what extraction found: instances with attributes, and links
// between them. Nothing here states a type system; the type system is what
// comes out.
var observed = ontology.Graph{
	Entities: []ontology.Entity{
		{ID: "p1", Type: "person", Name: "Ada Lovelace", Attrs: map[string]any{
			"full name": "Augusta Ada King", "born": 1815, "email": "ada@example.org"}},
		{ID: "p2", Type: "person", Name: "Charles Babbage", Attrs: map[string]any{
			"full name": "Charles Babbage", "born": 1791}},
		{ID: "p3", Type: "person", Name: "Joseph Clement", Attrs: map[string]any{"born": 1779}},
		{ID: "o1", Type: "company", Name: "Babbage Engines", Attrs: map[string]any{
			"founded": 1843, "staff count": 40, "public": false}},
		{ID: "o2", Type: "company", Name: "Menabrea Works", Attrs: map[string]any{
			"founded": 1852, "staff count": 12, "public": true}},
		{ID: "c1", Type: "city", Name: "London", Attrs: map[string]any{"country": "England"}},
		{ID: "c2", Type: "city", Name: "Montréal", Attrs: map[string]any{"country": "Canada"}},
		// One instance of a type. It does not become a class: a type seen
		// once is a coincidence, not a category.
		{ID: "x1", Type: "loom", Name: "Jacquard Loom"},
	},
	Links: []ontology.Link{
		{Type: "works for", From: "p1", To: "o1", FromType: "person", ToType: "company"},
		{Type: "works for", From: "p2", To: "o1", FromType: "person", ToType: "company"},
		{Type: "works for", From: "p3", To: "o1", FromType: "person", ToType: "company"},
		{Type: "founded by", From: "o1", To: "p2", FromType: "company", ToType: "person"},
		{Type: "located in", From: "o1", To: "c1", FromType: "company", ToType: "city"},
		{Type: "located in", From: "o2", To: "c2", FromType: "company", ToType: "city"},
	},
}

func main() {
	schema := infer()
	naming()
	hierarchy()
	serialize(schema)
	namespace()
	types()
	collision()
}

// infer groups the instances by type into classes, reads object properties off
// the links and datatype properties off the attributes, and returns the two as
// one schema.
func infer() ontology.Schema {
	schema, err := ontology.Inference{}.Schema(observed)
	if err != nil {
		log.Fatal(err)
	}
	schema.Name = "Engines Ontology"
	schema = schema.Normalize()

	fmt.Printf("inferred from %d entities and %d links\n", len(observed.Entities), len(observed.Links))
	fmt.Printf("\nclasses  %d\n", len(schema.Classes))
	for _, c := range schema.Classes {
		fmt.Printf("  %-14s from %-8q seen %d  attributes %v\n", c.Name, c.From, c.Seen, c.Attrs)
	}
	fmt.Println("  loom is not here: a type seen once does not become a class (Inference.Min defaults to 2)")

	fmt.Printf("\nproperties  %d\n", len(schema.Properties))
	for _, p := range schema.Properties {
		fmt.Printf("  %-14s %-11s %-10s → %-12s seen %d\n",
			p.Name, p.Kind, strings.Join(p.Domain, ","), strings.Join(p.Range, ","), p.Seen)
	}
	fmt.Println("  an object property ranges over classes, a data property over datatypes")
	return schema
}

// naming is what makes two spellings of one concept one term. The functions
// are idempotent, so normalizing an already-normal name leaves it alone.
func naming() {
	fmt.Println("\nnaming")
	fmt.Printf("  %-16s %-16s %-16s %s\n", "source", "ClassName", "PropertyName", "SchemaName")
	for _, s := range []string{"staff count", "MY_CLASS", "worksFor", "people", "has name"} {
		fmt.Printf("  %-16q %-16s %-16s %s\n",
			s, ontology.ClassName(s), ontology.PropertyName(s), ontology.SchemaName(s))
	}

	fmt.Println("\nconventions")
	for _, s := range []string{"Person", "people", "person"} {
		fix, ok := ontology.CheckClass(s)
		fmt.Printf("  class    %-10q ok=%-5v suggestion %q\n", s, ok, fix)
	}
	for _, s := range []string{"worksFor", "works_for", "WorksFor"} {
		fix, ok := ontology.CheckProperty(s, ontology.Object)
		fmt.Printf("  property %-10q ok=%-5v suggestion %q\n", s, ok, fix)
	}
	fmt.Println("  a PascalCase property lowers whole: WorksFor becomes worksfor, not worksFor")
}

// hierarchy fills in the parent of a class that has none — but only from the
// roots a vocabulary conventionally provides. Guessing a parent from an
// overlap in two names invents a taxonomy the data never showed.
func hierarchy() {
	classes := []ontology.Class{
		{Name: "Thing"},
		{Name: "Person", Parent: "Thing"},
		{Name: "Employee", Parent: "Person"},
		{Name: "Company"},
	}
	fmt.Println("\nhierarchy")
	for _, c := range ontology.Hierarchy(classes) {
		fmt.Printf("  %-10s parent %q\n", c.Name, c.Parent)
	}

	s := ontology.Schema{Classes: ontology.Hierarchy(classes)}
	fmt.Printf("  Employee specialises %v\n", s.Ancestors("Employee"))

	// A cycle makes the subclass relation meaningless, so it is reported
	// rather than followed.
	bad := []ontology.Class{{Name: "A", Parent: "B"}, {Name: "B", Parent: "A"}}
	fmt.Printf("  cycles in a broken hierarchy: %v\n", ontology.Cycles(bad))
}

// serialize writes the schema in the formats other tools read, and reads one
// of them back. What the package writes, the package parses.
func serialize(schema ontology.Schema) {
	ttl := schema.Turtle()
	lines := strings.Split(strings.TrimRight(ttl, "\n"), "\n")
	fmt.Printf("\nturtle  %d lines; the prefix block is the first 9, then:\n", len(lines))
	for _, line := range lines[9:26] {
		fmt.Printf("  %s\n", line)
	}
	fmt.Println("  …")

	back, err := ontology.ParseTurtle([]byte(ttl))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\nround trip  %d classes and %d properties out, %d and %d back\n",
		len(schema.Classes), len(schema.Properties), len(back.Classes), len(back.Properties))
	if c, ok := back.Class("Person"); ok {
		fmt.Printf("  Person survives with IRI %s\n", c.IRI)
	}

	doc, err := schema.JSONLD()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  json-ld is %d bytes, n-triples %d lines\n",
		len(doc), strings.Count(schema.NTriples(), "\n"))

	again, err := ontology.ParseJSONLD(doc)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  and json-ld reads back too: %d classes, %d properties\n",
		len(again.Classes), len(again.Properties))
}

// namespace mints the IRI a term lives at. A version other than the first is
// carried in the namespace, never in the term IRIs, so a term keeps one
// identity across versions.
func namespace() {
	n := ontology.Namespace{Base: "https://engines.example/ns#"}
	n.Bind("eng", "https://engines.example/ns#")
	fmt.Println("\nnamespace")
	fmt.Printf("  class      %s\n", n.Class("staff member"))
	fmt.Printf("  property   %s\n", n.Property("works for"))
	fmt.Printf("  individual %s\n", n.Individual("Ada Lovelace"))
	iri, _ := n.Resolve("rdfs")
	fmt.Printf("  rdfs resolves without being bound: %s\n", iri)
	fmt.Printf("  a bound prefix wins: eng → %s\n", must(n.Resolve("eng")))
}

// types is the datatype a value takes, deliberately coarse: the point is a
// range a validator can check, not an exact Go type.
func types() {
	fmt.Println("\ndatatypes")
	for _, v := range []any{"Ada", 1843, 40.5, true, time.Now(), []string{"a"}} {
		fmt.Printf("  %-24T %s\n", v, ontology.XSD(v))
	}
}

// collision is the failure the package refuses to paper over. Two source types
// that normalize to one class name would silently merge two different things,
// so it is an error.
func collision() {
	g := ontology.Graph{Entities: []ontology.Entity{
		{ID: "a1", Type: "staff member", Name: "Ada"},
		{ID: "a2", Type: "staff member", Name: "Charles"},
		{ID: "b1", Type: "staff_member", Name: "Joseph"},
		{ID: "b2", Type: "staff_member", Name: "Luigi"},
	}}
	_, err := ontology.Inference{}.Schema(g)
	fmt.Println("\ncollision")
	if errors.Is(err, ontology.ErrCollision) {
		fmt.Printf("  %v\n", err)
		fmt.Println("  both normalize to StaffMember; merging them would lose a distinction the data drew")
	}
}

func must(s string, ok bool) string {
	if !ok {
		return "unbound"
	}
	return s
}
