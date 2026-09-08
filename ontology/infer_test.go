package ontology

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// observed is the instance graph the inference tests read: two people, two
// companies, and one type seen only once.
func observed() Graph {
	return Graph{
		Entities: []Entity{
			{ID: "1", Type: "person", Name: "Ada", Attrs: map[string]any{"age": 36, "email": "ada@e"}},
			{ID: "2", Type: "person", Name: "Alan", Attrs: map[string]any{"age": 41, "email": "alan@e"}},
			{ID: "3", Type: "company", Name: "Acme", Attrs: map[string]any{"founded": 1900}},
			{ID: "4", Type: "company", Name: "Bcme", Attrs: map[string]any{"founded": 1901}},
			{ID: "5", Type: "ghost", Name: "Once"},
		},
		Links: []Link{
			{Type: "works for", From: "1", To: "3", FromType: "person", ToType: "company"},
			{Type: "works for", From: "2", To: "4", FromType: "person", ToType: "company"},
		},
	}
}

func TestClasses(t *testing.T) {
	got, err := Inference{}.Classes(observed().Entities)
	if err != nil {
		t.Fatalf("Classes: %v", err)
	}
	// "ghost" was seen once and the default floor is two, so it is not a
	// class. Class names are normalized; the source type is kept alongside.
	if n := names(got); !reflect.DeepEqual(n, []string{"Person", "Company"}) {
		t.Fatalf("classes = %v, want [Person Company]", n)
	}
	p := got[0]
	if p.From != "person" || p.Seen != 2 || p.IRI != DefaultBase+"Person" {
		t.Errorf("Person = %+v", p)
	}
	// Attrs are the attributes every instance carried, not the union.
	if !reflect.DeepEqual(p.Attrs, []string{"age", "email"}) {
		t.Errorf("Person attrs = %v, want [age email]", p.Attrs)
	}

	// Lowering the floor admits the type seen once.
	got, err = Inference{Min: 1}.Classes(observed().Entities)
	if err != nil {
		t.Fatal(err)
	}
	if n := names(got); !reflect.DeepEqual(n, []string{"Person", "Company", "Ghost"}) {
		t.Errorf("with Min 1: %v, want [Person Company Ghost]", n)
	}
}

// Two source types that normalize to one class name are a collision, not a
// merge: folding them silently would lose a distinction the data drew.
func TestClassNameCollision(t *testing.T) {
	_, err := Inference{}.Classes([]Entity{
		{Type: "person"}, {Type: "person"},
		{Type: "Persons"}, {Type: "Persons"},
	})
	if !errors.Is(err, ErrCollision) {
		t.Fatalf("err = %v, want ErrCollision", err)
	}
	for _, want := range []string{"Person", "person", "Persons"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// A property's endpoints have to be spelled the way the classes are spelled,
// or the schema refers to classes it does not contain.
func TestPropertyEndpointsNameTheClasses(t *testing.T) {
	s, err := Inference{}.Schema(observed())
	if err != nil {
		t.Fatalf("Schema: %v", err)
	}
	for _, p := range s.Properties {
		for _, end := range append(append([]string{}, p.Domain...), p.Range...) {
			if strings.Contains(end, ":") { // owl:Thing, xsd:integer and the like
				continue
			}
			if _, ok := s.Class(end); !ok {
				t.Errorf("property %q names %q, which is not a class in this schema (classes: %v)",
					p.Name, end, names(s.Classes))
			}
		}
	}
}

func TestProperties(t *testing.T) {
	g := observed()
	classes, err := Inference{}.Classes(g.Entities)
	if err != nil {
		t.Fatal(err)
	}
	props, err := Inference{}.Properties(g, classes)
	if err != nil {
		t.Fatal(err)
	}
	if got := propNames(props); !reflect.DeepEqual(got, []string{"worksFor", "age", "email", "founded"}) {
		t.Fatalf("properties = %v", got)
	}
	byName := map[string]Property{}
	for _, p := range props {
		byName[p.Name] = p
	}
	for _, c := range []struct {
		name         string
		kind         Kind
		domain, rang []string
		seen         int
	}{
		{"worksFor", Object, []string{"Person"}, []string{"Company"}, 2},
		{"age", Data, []string{"Person"}, []string{"xsd:integer"}, 2},
		{"email", Data, []string{"Person"}, []string{"xsd:string"}, 2},
		{"founded", Data, []string{"Company"}, []string{"xsd:integer"}, 2},
	} {
		p := byName[c.name]
		if p.Kind != c.kind {
			t.Errorf("%s kind = %q, want %q", c.name, p.Kind, c.kind)
		}
		if !reflect.DeepEqual(p.Domain, c.domain) {
			t.Errorf("%s domain = %v, want %v", c.name, p.Domain, c.domain)
		}
		if !reflect.DeepEqual(p.Range, c.rang) {
			t.Errorf("%s range = %v, want %v", c.name, p.Range, c.rang)
		}
		if p.Seen != c.seen {
			t.Errorf("%s seen %d times, want %d", c.name, p.Seen, c.seen)
		}
	}
}

// A link with no type at all is still a relation, and a link seen once does
// not make a property.
func TestPropertiesFloor(t *testing.T) {
	g := Graph{
		Entities: []Entity{
			{ID: "1", Type: "person"}, {ID: "2", Type: "person"},
			{ID: "3", Type: "person"}, {ID: "4", Type: "person"},
		},
		Links: []Link{
			{From: "1", To: "2"}, {From: "3", To: "4"}, // untyped, twice
			{Type: "rare", From: "1", To: "3"}, // typed, once
		},
	}
	classes, _ := Inference{}.Classes(g.Entities)
	props, err := Inference{}.Properties(g, classes)
	if err != nil {
		t.Fatal(err)
	}
	if got := propNames(props); !reflect.DeepEqual(got, []string{"relatedTo"}) {
		t.Errorf("properties = %v, want just relatedTo", got)
	}
}

func TestSchema(t *testing.T) {
	s, err := Inference{Base: "http://e/"}.Schema(observed())
	if err != nil {
		t.Fatal(err)
	}
	if s.IRI != "http://e/" || s.Base != "http://e/" || s.Name != "InferredOntology" || s.Version != "1.0" {
		t.Errorf("header = %+v", s)
	}
	if len(s.Classes) != 2 || len(s.Properties) != 4 {
		t.Errorf("%d classes and %d properties, want 2 and 4", len(s.Classes), len(s.Properties))
	}
	// The inferred schema is a schema: it writes out and reads back.
	back, err := ParseTurtle([]byte(s.Turtle()))
	if err != nil {
		t.Fatalf("an inferred schema did not survive turtle: %v", err)
	}
	if got := names(back.Classes); !reflect.DeepEqual(got, names(s.Classes)) {
		t.Errorf("classes after a round trip = %v, want %v", got, names(s.Classes))
	}
}

func TestHierarchy(t *testing.T) {
	// Only the roots a vocabulary conventionally provides are inferred.
	// Without one present, nothing is invented.
	got := Hierarchy([]Class{{Name: "Person"}, {Name: "Company"}})
	for _, c := range got {
		if c.Parent != "" {
			t.Errorf("%s was given the parent %q out of nowhere", c.Name, c.Parent)
		}
	}
	// With a root present, the others hang off it — and the root does not
	// become its own parent.
	got = Hierarchy([]Class{{Name: "Entity"}, {Name: "Person"}, {Name: "Company"}})
	for _, c := range got {
		want := "Entity"
		if c.Name == "Entity" {
			want = ""
		}
		if c.Parent != want {
			t.Errorf("%s parent = %q, want %q", c.Name, c.Parent, want)
		}
	}
	// A parent already stated is left alone.
	got = Hierarchy([]Class{{Name: "Entity"}, {Name: "Engineer", Parent: "Person"}})
	if got[1].Parent != "Person" {
		t.Errorf("a stated parent was overwritten with %q", got[1].Parent)
	}
}

func TestCycles(t *testing.T) {
	for _, c := range []struct {
		name    string
		classes []Class
		want    []string
	}{
		{"none", []Class{{Name: "A"}, {Name: "B", Parent: "A"}}, nil},
		{"pair", []Class{{Name: "A", Parent: "B"}, {Name: "B", Parent: "A"}}, []string{"A", "B"}},
		{"self", []Class{{Name: "A", Parent: "A"}}, []string{"A"}},
		{"longer", []Class{{Name: "A", Parent: "B"}, {Name: "B", Parent: "C"}, {Name: "C", Parent: "A"}}, []string{"A", "B", "C"}},
		{"dangling parent is not a cycle", []Class{{Name: "A", Parent: "Ghost"}}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := Cycles(c.classes); !reflect.DeepEqual(got, c.want) {
				t.Errorf("Cycles = %v, want %v", got, c.want)
			}
		})
	}
}

func TestXSD(t *testing.T) {
	for _, c := range []struct {
		in   any
		want string
	}{
		{1, "xsd:integer"},
		{int64(2), "xsd:integer"},
		{1.5, "xsd:double"},
		{true, "xsd:boolean"},
		{"a string", "xsd:string"},
		{"2026-01-02", "xsd:date"},
		{"2026-01-02T03:04:05", "xsd:dateTime"},
		{time.Now(), "xsd:dateTime"},
		{nil, "xsd:string"},      // nothing known is a string
		{[]int{1}, "xsd:string"}, // and so is anything unrecognised
	} {
		if got := XSD(c.in); got != c.want {
			t.Errorf("XSD(%#v) = %q, want %q", c.in, got, c.want)
		}
	}
}
