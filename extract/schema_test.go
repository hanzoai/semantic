package extract

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// The ontology every schema test reads, matching the one the Python suite
// uses: three concepts, one predicate constrained at both ends, one
// constrained by bare strings, one unconstrained.
var ont = Ontology{
	Classes: []Class{{Name: "Person"}, {Name: "Organization"}, {Label: "City"}},
	Properties: []Property{
		{Name: "worksAt", Domain: Names{"Person"}, Range: Names{"Organization"}},
		{Name: "locatedIn", Domain: Names{"Organization"}, Range: Names{"City"}},
		{Name: "knows"},
	},
}

func person() Entity  { return Entity{Text: "Alice", Label: "Person", End: 5, Score: 1} }
func org() Entity     { return Entity{Text: "Acme", Label: "Organization", End: 4, Score: 1} }
func city() Entity    { return Entity{Text: "Paris", Label: "City", End: 5, Score: 1} }
func product() Entity { return Entity{Text: "Widget", Label: "Product", End: 6, Score: 1} }

func rel(s Entity, p string, o Entity) Relation {
	return Relation{Subject: s, Predicate: p, Object: o, Score: 1}
}

func TestNewSchema(t *testing.T) {
	s := NewSchema(ont)

	want := []string{"City", "Organization", "Person"}
	if !reflect.DeepEqual(s.Concepts, want) {
		t.Errorf("concepts = %v, want %v", s.Concepts, want)
	}
	for _, name := range []string{"worksAt", "locatedIn", "knows"} {
		if !s.Pred(name) {
			t.Errorf("Pred(%q) = false, want true", name)
		}
	}
	if got := s.Predicates["worksAt"]; !reflect.DeepEqual(got.Domain, []string{"Person"}) ||
		!reflect.DeepEqual(got.Range, []string{"Organization"}) {
		t.Errorf("worksAt = %+v, want domain [Person] range [Organization]", got)
	}
	// A property with no stated domain or range restricts nothing.
	if got := s.Predicates["knows"]; got.Domain != nil || got.Range != nil {
		t.Errorf("knows = %+v, want both ends unconstrained", got)
	}
}

func TestNewSchemaFoldsEndpointTypes(t *testing.T) {
	// A type that appears only as a range is still a concept: a
	// class-frequency cutoff during induction must not make a property's own
	// range off-vocabulary.
	s := NewSchema(Ontology{
		Classes:    []Class{{Name: "Person"}},
		Properties: []Property{{Name: "worksFor", Domain: Names{"Person"}, Range: Names{"Org"}}},
	})
	if !s.Concept("Org") {
		t.Fatalf("concepts = %v, want Org folded in from the range", s.Concepts)
	}
	if !s.Allows("Person", "worksFor", "Org") {
		t.Error("Allows(Person, worksFor, Org) = false, want true")
	}
}

func TestNewSchemaThingIsUnconstrained(t *testing.T) {
	// An ontology generator writes owl:Thing when it cannot resolve an
	// endpoint's type. That means "any concept", not a concept named Thing,
	// so keeping it would reject every real endpoint.
	for _, universal := range []string{"owl:Thing", "Thing", "http://www.w3.org/2002/07/owl#Thing"} {
		s := NewSchema(Ontology{
			Classes:    []Class{{Name: "Person"}, {Name: "Organization"}},
			Properties: []Property{{Name: "relatedTo", Domain: Names{universal}, Range: Names{universal}}},
		})
		p := s.Predicates["relatedTo"]
		if p.Domain != nil || p.Range != nil {
			t.Errorf("%s: relatedTo = %+v, want both ends unconstrained", universal, p)
		}
		if !s.Allows("Person", "relatedTo", "Organization") {
			t.Errorf("%s: Allows(Person, relatedTo, Organization) = false, want true", universal)
		}
	}
}

func TestSchemaAllows(t *testing.T) {
	s := NewSchema(ont)
	for _, c := range []struct {
		name           string
		sub, pred, obj string
		want           bool
	}{
		{"conforming", "Person", "worksAt", "Organization", true},
		{"range violated", "Person", "worksAt", "City", false},
		{"domain violated", "Organization", "worksAt", "Organization", false},
		{"predicate off vocabulary", "Person", "founded", "Organization", false},
		{"subject off vocabulary", "Product", "worksAt", "Organization", false},
		{"object off vocabulary", "Person", "worksAt", "Product", false},
		{"unconstrained predicate takes any concepts", "Person", "knows", "City", true},
		{"unconstrained predicate still needs concepts", "Person", "knows", "Product", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := s.Allows(c.sub, c.pred, c.obj); got != c.want {
				t.Errorf("Allows(%q, %q, %q) = %v, want %v", c.sub, c.pred, c.obj, got, c.want)
			}
		})
	}
}

func TestSchemaCheckEntities(t *testing.T) {
	s := NewSchema(ont)

	ok := s.Check(Set{Entities: []Entity{person(), org(), city()}})
	if !ok.OK || ok.Score != 1 {
		t.Errorf("conforming entities: OK=%v Score=%v, want true 1", ok.OK, ok.Score)
	}
	if ok.Counts["unknown"] != 0 || ok.Err() != nil {
		t.Errorf("conforming entities: counts=%v err=%v", ok.Counts, ok.Err())
	}

	bad := s.Check(Set{Entities: []Entity{person(), product()}})
	if bad.OK {
		t.Error("off-vocabulary entity accepted, want rejected")
	}
	if bad.Score != 0.5 {
		t.Errorf("Score = %v, want 0.5 (one of two conforming)", bad.Score)
	}
	if !reflect.DeepEqual(bad.Unknown, []string{"Product"}) {
		t.Errorf("Unknown = %v, want [Product]", bad.Unknown)
	}
	if bad.Counts["known"] != 1 || bad.Counts["unknown"] != 1 {
		t.Errorf("counts = %v, want known 1 unknown 1", bad.Counts)
	}
	if err := bad.Err(); !errors.Is(err, ErrInvalid) {
		t.Errorf("Err() = %v, want it to wrap ErrInvalid", err)
	} else if !strings.Contains(err.Error(), "Product") {
		t.Errorf("Err() = %v, want the offending label named", err)
	}
}

func TestSchemaCheckEmptyIsVacuouslyValid(t *testing.T) {
	r := NewSchema(ont).Check(Set{})
	if !r.OK || r.Score != 1 {
		t.Errorf("OK=%v Score=%v, want true 1: nothing extracted breaks no rule", r.OK, r.Score)
	}
}

func TestSchemaCheckRelations(t *testing.T) {
	s := NewSchema(ont)

	ok := s.Check(Set{Relations: []Relation{
		rel(person(), "worksAt", org()),
		rel(person(), "knows", city()),
	}})
	if !ok.OK || ok.Score != 1 {
		t.Errorf("conforming relations: OK=%v Score=%v, want true 1", ok.OK, ok.Score)
	}

	bad := s.Check(Set{Relations: []Relation{
		rel(person(), "worksAt", org()),  // conforming
		rel(person(), "founded", org()),  // predicate off vocabulary
		rel(person(), "worksAt", city()), // range violated
	}})
	if bad.OK {
		t.Error("non-conforming relations accepted, want rejected")
	}
	if got, want := bad.Score, 1.0/3.0; got != want {
		t.Errorf("Score = %v, want %v", got, want)
	}
	for k, want := range map[string]int{"conforming": 1, "offVocabulary": 1, "offDomain": 1, "torn": 0} {
		if bad.Counts[k] != want {
			t.Errorf("Counts[%q] = %d, want %d (all: %v)", k, bad.Counts[k], want, bad.Counts)
		}
	}
}

func TestSchemaCheckTornRelation(t *testing.T) {
	// A relation missing an endpoint must be reported, not dereferenced.
	r := NewSchema(ont).Check(Set{Relations: []Relation{
		rel(person(), "worksAt", org()),
		rel(person(), "worksAt", Entity{}),
	}})
	if r.OK {
		t.Error("torn relation accepted, want rejected")
	}
	if r.Counts["torn"] != 1 || r.Counts["conforming"] != 1 {
		t.Errorf("counts = %v, want torn 1 conforming 1", r.Counts)
	}
}

func TestSchemaKeep(t *testing.T) {
	s := NewSchema(ont)
	kept := s.Keep(Set{
		Entities: []Entity{person(), org(), product()},
		Relations: []Relation{
			rel(person(), "worksAt", org()),  // keep
			rel(person(), "founded", org()),  // drop: predicate off vocabulary
			rel(person(), "worksAt", city()), // drop: range violated
			rel(person(), "knows", city()),   // keep
			rel(person(), "knows", Entity{}), // drop: torn
		},
	})

	var labels []string
	for _, e := range kept.Entities {
		labels = append(labels, e.Label)
	}
	if !reflect.DeepEqual(labels, []string{"Person", "Organization"}) {
		t.Errorf("kept labels = %v, want [Person Organization]", labels)
	}

	var preds []string
	for _, r := range kept.Relations {
		preds = append(preds, r.Predicate)
	}
	if !reflect.DeepEqual(preds, []string{"worksAt", "knows"}) {
		t.Errorf("kept predicates = %v, want [worksAt knows]", preds)
	}
}

func TestReadSchema(t *testing.T) {
	// A generator writes a domain or range as a bare string, a list, or an
	// object; all three name the same concept.
	const doc = `{
	  "classes": [{"name": "Person"}, {"label": "City"}],
	  "properties": [
	    {"name": "worksAt", "domain": "Person", "range": ["Organization"]},
	    {"name": "livesIn", "domain": {"name": "Person"}, "range": {"label": "City"}},
	    {"label": "knows"}
	  ]
	}`
	s, err := ReadSchema(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}
	if !reflect.DeepEqual(s.Concepts, []string{"City", "Organization", "Person"}) {
		t.Errorf("concepts = %v", s.Concepts)
	}
	if !s.Allows("Person", "worksAt", "Organization") {
		t.Error("string domain and list range did not build a usable predicate")
	}
	if !s.Allows("Person", "livesIn", "City") {
		t.Error("object domain and range did not build a usable predicate")
	}
	// A property named only by its label is still a predicate.
	if !s.Pred("knows") {
		t.Error("Pred(knows) = false, want a label to stand in for a missing name")
	}
}

func TestReadSchemaRejectsBadJSON(t *testing.T) {
	if _, err := ReadSchema(strings.NewReader("not json")); err == nil {
		t.Fatal("ReadSchema accepted a document that is not JSON")
	}
}

func TestReadSchemaOddConstraints(t *testing.T) {
	// Generators write a domain in shapes no specification promised. Each one
	// still has to name concepts or the schema silently allows nothing.
	for _, c := range []struct {
		name   string
		domain string
		want   []string
	}{
		{"an object naming neither", `{"foo":1,"bar":2}`, []string{"bar", "foo"}},
		{"a number", `42`, []string{"42"}},
		{"null", `null`, nil},
		{"an empty string", `""`, nil},
		{"a list of objects", `[{"name":"Person"},{"label":"City"}]`, []string{"City", "Person"}},
		{"duplicates", `["Person","Person"]`, []string{"Person"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			doc := `{"properties":[{"name":"p","domain":` + c.domain + `}]}`
			s, err := ReadSchema(strings.NewReader(doc))
			if err != nil {
				t.Fatalf("ReadSchema: %v", err)
			}
			if got := s.Predicates["p"].Domain; !reflect.DeepEqual(got, c.want) {
				t.Errorf("domain = %v, want %v", got, c.want)
			}
			for _, name := range c.want {
				if !s.Concept(name) {
					t.Errorf("%q is a domain but not a concept", name)
				}
			}
		})
	}
}
