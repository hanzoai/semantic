package ontology

import (
	"reflect"
	"testing"
)

func TestPascal(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"my class", "MyClass"},
		{"MY_CLASS", "MyClass"},
		{"works for", "WorksFor"},
		{"Person", "Person"},
		{"", "Entity"},
		// A name already in camelCase has no word breaks to find, so it is
		// one word. Camel is the method that preserves the interior capital.
		{"worksFor", "Worksfor"},
	} {
		if got := Pascal(c.in); got != c.want {
			t.Errorf("Pascal(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCamel(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"has name", "hasName"},
		{"WORKS_FOR", "worksFor"},
		{"Person", "person"},
		{"", "hasProperty"},
		// Idempotent: normalizing an already-normal name changes nothing.
		{"worksFor", "worksFor"},
	} {
		if got := Camel(c.in); got != c.want {
			t.Errorf("Camel(%q) = %q, want %q", c.in, got, c.want)
		}
		if again := Camel(Camel(c.in)); again != c.want {
			t.Errorf("Camel is not idempotent on %q: %q", c.in, again)
		}
	}
}

func TestSingular(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"companies", "company"},
		{"addresses", "address"},
		{"people", "people"}, // irregular: the package knows no irregulars
		{"class", "class"},   // a double s is not a plural
		{"status", "status"}, // and neither is every trailing s
		{"analysis", "analysis"},
		{"Persons", "Person"},
		{"", ""},
	} {
		if got := Singular(c.in); got != c.want {
			t.Errorf("Singular(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSchemaName(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"example", "ExampleOntology"},
		{"ExampleOntology", "ExampleOntology"},
		{"my domain", "MyDomainOntology"},
		{"", "Ontology"},
	} {
		if got := SchemaName(c.in); got != c.want {
			t.Errorf("SchemaName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Check reports on a name without changing it, and its suggestion is what the
// corresponding normalizer would return.
func TestCheck(t *testing.T) {
	for _, c := range []struct {
		name       string
		kind       Kind
		class      string
		classOK    bool
		object     string
		objectOK   bool
		data       string
		dataOK     bool
		schemaName string
		schemaOK   bool
	}{
		{name: "Person", class: "", classOK: true, object: "person", data: "person", schemaName: "PersonOntology"},
		{name: "person", class: "Person", object: "person", data: "", dataOK: true, schemaName: "PersonOntology"},
		{name: "Persons", class: "Person", object: "persons", data: "persons", schemaName: "PersonsOntology"},
		{name: "worksFor", class: "Worksfor", object: "", objectOK: true, data: "", dataOK: true, schemaName: "WorksforOntology"},
		{name: "works_for", class: "WorksFor", object: "", objectOK: true, data: "", dataOK: true, schemaName: "WorksForOntology"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got, ok := CheckClass(c.name); got != c.class || ok != c.classOK {
				t.Errorf("CheckClass = (%q, %v), want (%q, %v)", got, ok, c.class, c.classOK)
			}
			if got, ok := CheckProperty(c.name, Object); got != c.object || ok != c.objectOK {
				t.Errorf("CheckProperty object = (%q, %v), want (%q, %v)", got, ok, c.object, c.objectOK)
			}
			if got, ok := CheckProperty(c.name, Data); got != c.data || ok != c.dataOK {
				t.Errorf("CheckProperty data = (%q, %v), want (%q, %v)", got, ok, c.data, c.dataOK)
			}
			if got, ok := CheckSchema(c.name); got != c.schemaName || ok != c.schemaOK {
				t.Errorf("CheckSchema = (%q, %v), want (%q, %v)", got, ok, c.schemaName, c.schemaOK)
			}
		})
	}
}

func TestParseKind(t *testing.T) {
	for _, c := range []struct {
		in   string
		want Kind
	}{
		{"object", Object},
		{"ObjectProperty", Object},
		{"owl:ObjectProperty", Object},
		{"relationship", Object},
		{"datatype", Data},
		{"owl:DatatypeProperty", Data},
		{"literal", Data},
		{"  Annotation ", Annotation},
		{"nonsense", Kind("nonsense")}, // returned unchanged, so a caller can report it
	} {
		if got := ParseKind(c.in); got != c.want {
			t.Errorf("ParseKind(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNamespace(t *testing.T) {
	var zero Namespace
	if got := zero.Root(); got != DefaultBase {
		t.Errorf("zero root = %q, want %q", got, DefaultBase)
	}
	if got := zero.Class("my class"); got != DefaultBase+"MyClass" {
		t.Errorf("Class = %q", got)
	}
	if got := zero.Property("works for"); got != DefaultBase+"worksFor" {
		t.Errorf("Property = %q", got)
	}
	// An individual's IRI keeps only word characters, so a name with
	// punctuation still makes one identifier.
	if got := zero.Individual("Ada Lovelace!"); got != DefaultBase+"individual/AdaLovelace" {
		t.Errorf("Individual = %q", got)
	}

	// A version other than the first is carried in the namespace.
	v := Namespace{Base: "http://e/", Version: "2.0"}
	if got := v.Root(); got != "http://e/v2.0/" {
		t.Errorf("versioned root = %q, want http://e/v2.0/", got)
	}
	if got := (Namespace{Base: "http://e/", Version: "1.0"}).Root(); got != "http://e/" {
		t.Errorf("first version must not be carried: %q", got)
	}

	// A base that ends in no separator gets a hash, so the term does not run
	// into the namespace.
	if got := (Namespace{Base: "http://e"}).Class("thing"); got != "http://e#Thing" {
		t.Errorf("unterminated base = %q, want http://e#Thing", got)
	}
}

func TestNamespaceResolve(t *testing.T) {
	var n Namespace
	if _, ok := n.Resolve("rdfs"); !ok {
		t.Error("standard prefixes resolve without being bound")
	}
	if _, ok := n.Resolve("ex"); ok {
		t.Error("an unbound prefix must not resolve")
	}
	n.Bind("ex", "http://e/")
	if iri, ok := n.Resolve("ex"); !ok || iri != "http://e/" {
		t.Errorf("Resolve(ex) = (%q, %v)", iri, ok)
	}
	// A binding of a standard name wins.
	n.Bind("rdfs", "http://mine/")
	if iri, _ := n.Resolve("rdfs"); iri != "http://mine/" {
		t.Errorf("a binding must beat the standard prefix: %q", iri)
	}
	if got, want := len(n.All()), len(Standard)+1; got != want {
		t.Errorf("All has %d prefixes, want %d", got, want)
	}
}

func TestNormalize(t *testing.T) {
	s := Schema{
		Base: "http://e/",
		Classes: []Class{
			{Name: "Person"},
			{Name: "Engineer", Parent: "Person"},
			{Name: "Person"}, // a repeat keeps the first and its place
			{Name: ""},       // a class with no name is not a class
		},
		Properties: []Property{
			{Name: "worksFor", Kind: Object},
			{Name: "age", Kind: Data},
			{Name: "worksFor"},
		},
	}
	n := s.Normalize()

	if got := names(n.Classes); !reflect.DeepEqual(got, []string{"Person", "Engineer"}) {
		t.Errorf("classes = %v, want [Person Engineer]", got)
	}
	if got := propNames(n.Properties); !reflect.DeepEqual(got, []string{"worksFor", "age"}) {
		t.Errorf("properties = %v, want [worksFor age]", got)
	}
	c, _ := n.Class("Person")
	if c.IRI != "http://e/Person" || c.Label != "Person" {
		t.Errorf("class = %+v, want a minted IRI and its name as a label", c)
	}
	// An open domain is owl:Thing, stated. An object property with no range
	// is likewise open; a data property's range is a datatype and is left
	// alone rather than guessed.
	p, _ := n.Property("worksFor")
	if !reflect.DeepEqual(p.Domain, []string{"owl:Thing"}) || !reflect.DeepEqual(p.Range, []string{"owl:Thing"}) {
		t.Errorf("object property = %+v, want owl:Thing at both ends", p)
	}
	d, _ := n.Property("age")
	if !reflect.DeepEqual(d.Domain, []string{"owl:Thing"}) || len(d.Range) != 0 {
		t.Errorf("data property = %+v, want an open domain and no invented range", d)
	}
}

func TestAncestors(t *testing.T) {
	s := Schema{Classes: []Class{
		{Name: "Person"},
		{Name: "Engineer", Parent: "Person"},
		{Name: "Backend", Parent: "Engineer"},
		{Name: "A", Parent: "B"},
		{Name: "B", Parent: "A"},
	}}
	if got := s.Ancestors("Backend"); !reflect.DeepEqual(got, []string{"Engineer", "Person"}) {
		t.Errorf("Ancestors(Backend) = %v, want [Engineer Person]", got)
	}
	if got := s.Ancestors("Person"); len(got) != 0 {
		t.Errorf("Ancestors(Person) = %v, want none", got)
	}
	if got := s.Ancestors("Nope"); len(got) != 0 {
		t.Errorf("Ancestors of an unknown class = %v, want none", got)
	}
	// A cycle stops the walk instead of hanging it.
	if got := s.Ancestors("A"); !reflect.DeepEqual(got, []string{"B"}) {
		t.Errorf("Ancestors(A) through a cycle = %v, want [B]", got)
	}
}

func TestTermIRI(t *testing.T) {
	s := Schema{
		Base:       "http://e/",
		Classes:    []Class{{Name: "Person", IRI: "http://other/P"}, {Name: "Place"}},
		Properties: []Property{{Name: "worksFor"}},
	}
	for _, c := range []struct{ name, want string }{
		{"Person", "http://other/P"},               // the IRI it carries
		{"Place", "http://e/Place"},                // minted as a class
		{"worksFor", "http://e/worksFor"},          // minted as a property, in camelCase
		{"Ghost", "http://e/Ghost"},                // an unknown name is a class
		{"http://x/y", "http://x/y"},               // an absolute IRI stands
		{"rdfs:label", Standard["rdfs"] + "label"}, // a known prefix expands
	} {
		if got := s.TermIRI(c.name); got != c.want {
			t.Errorf("TermIRI(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

func names(cs []Class) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Name
	}
	return out
}

func propNames(ps []Property) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name
	}
	return out
}
