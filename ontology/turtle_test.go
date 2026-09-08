package ontology

import (
	"reflect"
	"strings"
	"testing"
)

const (
	rdfNS  = "http://www.w3.org/1999/02/22-rdf-syntax-ns#"
	rdfsNS = "http://www.w3.org/2000/01/rdf-schema#"
	owlNS  = "http://www.w3.org/2002/07/owl#"
)

// TestStatements reads the constructs a serializer emits. N-Triples is a
// subset of the grammar, so the last cases are N-Triples too.
func TestStatements(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want []Statement
	}{
		{
			name: "prefixed names",
			src:  `@prefix ex: <http://e/> . ex:a ex:p ex:b .`,
			want: []Statement{{Subject: "http://e/a", Predicate: "http://e/p", Object: "http://e/b"}},
		},
		{
			name: "sparql-style directives take no dot",
			src:  "PREFIX ex: <http://e/>\nBASE <http://b/>\nex:a ex:p <rel> .",
			want: []Statement{{Subject: "http://e/a", Predicate: "http://e/p", Object: "http://b/rel"}},
		},
		{
			name: "a is rdf:type",
			src:  `<http://a> a <http://C> .`,
			want: []Statement{{Subject: "http://a", Predicate: rdfNS + "type", Object: "http://C"}},
		},
		{
			name: "predicate list",
			src:  `<http://a> <http://p> <http://b> ; <http://q> <http://c> .`,
			want: []Statement{
				{Subject: "http://a", Predicate: "http://p", Object: "http://b"},
				{Subject: "http://a", Predicate: "http://q", Object: "http://c"},
			},
		},
		{
			name: "object list",
			src:  `<http://a> <http://p> <http://b> , <http://c> .`,
			want: []Statement{
				{Subject: "http://a", Predicate: "http://p", Object: "http://b"},
				{Subject: "http://a", Predicate: "http://p", Object: "http://c"},
			},
		},
		{
			name: "tagged and typed literals",
			src:  `<http://a> <http://p> "x"@en , "y"^^<http://t> , "plain" .`,
			want: []Statement{
				{Subject: "http://a", Predicate: "http://p", Object: "x", Literal: true, Lang: "en"},
				{Subject: "http://a", Predicate: "http://p", Object: "y", Literal: true, Datatype: "http://t"},
				{Subject: "http://a", Predicate: "http://p", Object: "plain", Literal: true},
			},
		},
		{
			name: "bare literals carry their xsd datatype",
			src:  `<http://a> <http://p> true , 42 , 1.5 .`,
			want: []Statement{
				{Subject: "http://a", Predicate: "http://p", Object: "true", Literal: true, Datatype: xsdNS + "boolean"},
				{Subject: "http://a", Predicate: "http://p", Object: "42", Literal: true, Datatype: xsdNS + "integer"},
				{Subject: "http://a", Predicate: "http://p", Object: "1.5", Literal: true, Datatype: xsdNS + "decimal"},
			},
		},
		{
			name: "long literal spans lines",
			src:  "# a comment first\n<http://a> <http://p> \"\"\"multi\nline\"\"\" .",
			want: []Statement{{Subject: "http://a", Predicate: "http://p", Object: "multi\nline", Literal: true}},
		},
		{
			name: "escapes in a literal",
			src:  `<http://a> <http://p> "say \"hi\"\nand \\ stop" .`,
			want: []Statement{{Subject: "http://a", Predicate: "http://p", Object: "say \"hi\"\nand \\ stop", Literal: true}},
		},
		{
			name: "blank node property list",
			src:  `<http://a> <http://p> [ <http://q> "v" ] .`,
			want: []Statement{
				{Subject: "_:b1", Predicate: "http://q", Object: "v", Literal: true},
				{Subject: "http://a", Predicate: "http://p", Object: "_:b1"},
			},
		},
		{
			name: "labelled blank nodes",
			src:  `_:x <http://p> _:y .`,
			want: []Statement{{Subject: "_:x", Predicate: "http://p", Object: "_:y"}},
		},
		{
			name: "collection becomes an rdf list",
			src:  `<http://a> <http://p> ( <http://x> <http://y> ) .`,
			want: []Statement{
				{Subject: "_:b1", Predicate: rdfNS + "first", Object: "http://x"},
				{Subject: "_:b1", Predicate: rdfNS + "rest", Object: "_:b2"},
				{Subject: "_:b2", Predicate: rdfNS + "first", Object: "http://y"},
				{Subject: "_:b2", Predicate: rdfNS + "rest", Object: rdfNS + "nil"},
				{Subject: "http://a", Predicate: "http://p", Object: "_:b1"},
			},
		},
		{
			name: "empty collection is rdf:nil",
			src:  `<http://a> <http://p> ( ) .`,
			want: []Statement{{Subject: "http://a", Predicate: "http://p", Object: rdfNS + "nil"}},
		},
		{
			name: "empty document",
			src:  "# nothing but a comment\n",
			want: nil,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := Statements([]byte(c.src))
			if err != nil {
				t.Fatalf("Statements: %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("got  %+v\nwant %+v", got, c.want)
			}
		})
	}
}

func TestStatementsRefused(t *testing.T) {
	for _, c := range []struct{ name, src, msg string }{
		{"truncated triple", `<http://a> <http://p>`, "unexpected end of input"},
		{"prefix without a colon", `@prefix ex <http://e/> .`, "expected <"},
		{"unclosed iri", `<http://a <http://p> <http://b> .`, ""},
		{"unknown prefix", `ex:a <http://p> <http://b> .`, "prefix"},
		{"unterminated literal", `<http://a> <http://p> "x .`, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := Statements([]byte(c.src))
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.HasPrefix(err.Error(), "turtle: line ") {
				t.Errorf("error %q does not say where it gave up", err)
			}
			if c.msg != "" && !strings.Contains(err.Error(), c.msg) {
				t.Errorf("error = %q, want it to mention %q", err, c.msg)
			}
		})
	}
}

const schemaTTL = `@prefix : <https://ex.org/ns#> .
@prefix owl: <http://www.w3.org/2002/07/owl#> .
@prefix rdfs: <http://www.w3.org/2000/01/rdf-schema#> .
@prefix xsd: <http://www.w3.org/2001/XMLSchema#> .

<https://ex.org/ns#> a owl:Ontology ;
    rdfs:label "Example" ;
    rdfs:comment "An example" ;
    owl:versionInfo "2.0" .

:Person a owl:Class ;
    rdfs:label "Person" ;
    rdfs:comment "A human being" .

:Engineer a owl:Class ;
    rdfs:subClassOf :Person .

:worksFor a owl:ObjectProperty ;
    rdfs:domain :Person ;
    rdfs:range :Organization .

:age a owl:DatatypeProperty ;
    rdfs:domain :Person ;
    rdfs:range xsd:integer .
`

func TestParseTurtle(t *testing.T) {
	s, err := ParseTurtle([]byte(schemaTTL))
	if err != nil {
		t.Fatalf("ParseTurtle: %v", err)
	}
	if s.IRI != "https://ex.org/ns#" || s.Name != "Example" || s.Comment != "An example" || s.Version != "2.0" {
		t.Errorf("ontology header = %+v", s)
	}
	// Terms are named by the local part of their IRI, which is what the rest
	// of the package indexes by.
	if got := names(s.Classes); !reflect.DeepEqual(got, []string{"Person", "Engineer"}) {
		t.Errorf("classes = %v, want [Person Engineer]", got)
	}
	p, ok := s.Class("Person")
	if !ok || p.Label != "Person" || p.Comment != "A human being" || p.IRI != "https://ex.org/ns#Person" {
		t.Errorf("Person = %+v", p)
	}
	e, _ := s.Class("Engineer")
	if e.Parent != "Person" {
		t.Errorf("Engineer parent = %q, want Person", e.Parent)
	}
	w, ok := s.Property("worksFor")
	if !ok || w.Kind != Object || !reflect.DeepEqual(w.Domain, []string{"Person"}) || !reflect.DeepEqual(w.Range, []string{"Organization"}) {
		t.Errorf("worksFor = %+v", w)
	}
	a, ok := s.Property("age")
	if !ok || a.Kind != Data || !reflect.DeepEqual(a.Range, []string{"xsd:integer"}) {
		t.Errorf("age = %+v", a)
	}
}

// A schema written out and read back is the same schema. Turtle, N-Triples
// and JSON-LD all have to agree on that, or the formats are not saying the
// same thing.
func TestRoundTrip(t *testing.T) {
	want := Schema{
		IRI: "http://e/", Name: "ExampleOntology", Comment: "c", Version: "1.0", Base: "http://e/",
		// No Prefixes: reading resolves every prefixed name to its full IRI
		// and does not keep the bindings, and the writers spell every term
		// out in full, so the terms survive and the header does not.
		Classes: []Class{
			{Name: "Person", IRI: "http://e/Person", Label: "Person", Comment: "A person"},
			{Name: "Engineer", IRI: "http://e/Engineer", Label: "Engineer", Parent: "Person"},
		},
		Properties: []Property{
			{Name: "worksFor", IRI: "http://e/worksFor", Kind: Object, Label: "worksFor",
				Domain: []string{"Person"}, Range: []string{"Organization"}},
			{Name: "age", IRI: "http://e/age", Kind: Data, Label: "age",
				Domain: []string{"Person"}, Range: []string{"xsd:integer"}},
		},
	}
	for _, c := range []struct {
		name  string
		write func(Schema) ([]byte, error)
		read  func([]byte) (Schema, error)
	}{
		{"turtle", func(s Schema) ([]byte, error) { return []byte(s.Turtle()), nil }, ParseTurtle},
		{"ntriples", func(s Schema) ([]byte, error) { return []byte(s.NTriples()), nil }, ParseTurtle},
		{"jsonld", Schema.JSONLD, ParseJSONLD},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, err := c.write(want)
			if err != nil {
				t.Fatalf("write: %v", err)
			}
			got, err := c.read(b)
			if err != nil {
				t.Fatalf("read: %v\n%s", err, b)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("got  %+v\nwant %+v\nfrom\n%s", got, want, b)
			}
		})
	}
}

// A property with no IRI of its own mints one in camelCase. Minting it the
// way a class is minted would rewrite worksFor to Worksfor and rename the
// term on the way out of the document.
func TestPropertyKeepsItsNameThroughTurtle(t *testing.T) {
	s := Schema{Base: "http://e/", Properties: []Property{{Name: "worksFor", Kind: Object}}}
	ttl := s.Turtle()
	if !strings.Contains(ttl, "<http://e/worksFor> a owl:ObjectProperty") {
		t.Fatalf("turtle does not name the property in camelCase:\n%s", ttl)
	}
	back, err := ParseTurtle([]byte(ttl))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := back.Property("worksFor"); !ok {
		t.Errorf("property came back as %v, want worksFor", propNames(back.Properties))
	}
}

func TestTurtleQuotesAreEscaped(t *testing.T) {
	s := Schema{Base: "http://e/", Classes: []Class{{Name: "C", Comment: `he said "no"` + "\nthen \\ left"}}}
	back, err := ParseTurtle([]byte(s.Turtle()))
	if err != nil {
		t.Fatalf("a comment with a quote in it broke the document: %v\n%s", err, s.Turtle())
	}
	c, _ := back.Class("C")
	if c.Comment != `he said "no"`+"\nthen \\ left" {
		t.Errorf("comment = %q", c.Comment)
	}
}

// A prefix the document declared is part of what it said, and both readers
// report it the same way: the standard bindings are assumed and dropped, the
// rest are kept, and none at all is a nil map rather than an empty one.
func TestPrefixesSurviveBothReaders(t *testing.T) {
	s := Schema{
		IRI: "http://e/", Name: "N", Version: "1.0", Base: "http://e/",
		Prefixes: map[string]string{"ex": "http://ex/"},
		Classes:  []Class{{Name: "C", IRI: "http://e/C", Label: "C"}},
	}
	for _, c := range []struct {
		name  string
		write func(Schema) ([]byte, error)
		read  func([]byte) (Schema, error)
	}{
		{"turtle", func(s Schema) ([]byte, error) { return []byte(s.Turtle()), nil }, ParseTurtle},
		{"jsonld", Schema.JSONLD, ParseJSONLD},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, err := c.write(s)
			if err != nil {
				t.Fatal(err)
			}
			got, err := c.read(b)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Prefixes, map[string]string{"ex": "http://ex/"}) {
				t.Errorf("prefixes = %v, want just the one the document declared\n%s", got.Prefixes, b)
			}
		})
	}

	// A document that declares nothing beyond the standard prefixes reports
	// no bindings, not an empty set of them.
	for _, c := range []struct {
		name string
		read func([]byte) (Schema, error)
		src  []byte
	}{
		{"turtle", ParseTurtle, []byte(schemaTTL)},
		{"jsonld", ParseJSONLD, []byte(`{"@context":{"@vocab":"http://e/"},"@graph":[]}`)},
	} {
		t.Run("none/"+c.name, func(t *testing.T) {
			got, err := c.read(c.src)
			if err != nil {
				t.Fatal(err)
			}
			if got.Prefixes != nil {
				t.Errorf("prefixes = %#v, want nil", got.Prefixes)
			}
		})
	}
}
