package extract

import (
	"errors"
	"math"
	"strings"
	"testing"
)

// about compares two scores without demanding bit equality of arithmetic
// that is only ever read as a fraction.
func about(got, want float64) bool { return math.Abs(got-want) < 1e-9 }

func at(text, label string, score float64) Entity {
	return Entity{Text: text, Label: label, End: len(text), Score: score}
}

func TestFloorScoresConfidence(t *testing.T) {
	// One entity below the floor of three: a penalty of half that share, then
	// a factor for the mean confidence. 0.9, 0.6 and 0.3 average 0.6, so the
	// score is (1 - 1/3 x 0.5) x (0.5 + 0.6 x 0.5).
	r := Floor(0.5).Check(Set{Entities: []Entity{
		at("Alice", "PERSON", 0.9),
		at("Acme", "ORG", 0.6),
		at("Paris", "GPE", 0.3),
	}})

	if !r.OK {
		t.Errorf("OK = false, want true: a low score is a warning, not a fault (%v)", r.Errs)
	}
	if want := (1 - 0.5/3) * (0.5 + 0.6*0.5); !about(r.Score, want) {
		t.Errorf("Score = %v, want %v", r.Score, want)
	}
	for k, want := range map[string]int{
		"entities": 3, "high": 1, "medium": 1, "low": 1,
		"distinct": 3, "labels": 3, "empty": 0,
	} {
		if r.Counts[k] != want {
			t.Errorf("Counts[%q] = %d, want %d (all: %v)", k, r.Counts[k], want, r.Counts)
		}
	}
	if len(r.Warns) != 1 || !strings.Contains(r.Warns[0], "1 entities") {
		t.Errorf("Warns = %v, want one naming the entity below the floor", r.Warns)
	}
}

func TestFloorScoresRelations(t *testing.T) {
	// One of two relations below the floor: (1 - 1/2 x 0.5) x (0.5 + 0.65 x 0.5).
	r := Floor(0.5).Check(Set{Relations: []Relation{
		{Subject: at("Alice", "PERSON", 1), Predicate: "worksAt", Object: at("Acme", "ORG", 1), Score: 0.9},
		{Subject: at("Alice", "PERSON", 1), Predicate: "knows", Object: at("Bob", "PERSON", 1), Score: 0.4},
	}})

	if !r.OK {
		t.Errorf("OK = false, want true (%v)", r.Errs)
	}
	if want := 0.75 * (0.5 + 0.65*0.5); !about(r.Score, want) {
		t.Errorf("Score = %v, want %v", r.Score, want)
	}
	if r.Counts["relations"] != 2 || r.Counts["predicates"] != 2 || r.Counts["torn"] != 0 {
		t.Errorf("counts = %v", r.Counts)
	}
}

func TestFloorAveragesBothAxes(t *testing.T) {
	x := Set{
		Entities: []Entity{at("Alice", "PERSON", 0.9), at("Acme", "ORG", 0.6), at("Paris", "GPE", 0.3)},
		Relations: []Relation{
			{Subject: at("Alice", "PERSON", 1), Predicate: "worksAt", Object: at("Acme", "ORG", 1), Score: 0.9},
			{Subject: at("Alice", "PERSON", 1), Predicate: "knows", Object: at("Bob", "PERSON", 1), Score: 0.4},
		},
	}
	ents := (1 - 0.5/3) * (0.5 + 0.6*0.5)
	rels := 0.75 * (0.5 + 0.65*0.5)
	if got := Floor(0.5).Check(x).Score; !about(got, (ents+rels)/2) {
		t.Errorf("Score = %v, want the mean of %v and %v", got, ents, rels)
	}
}

func TestFloorReportsStructure(t *testing.T) {
	// The zero Floor admits any confidence and still fails what is malformed:
	// the two axes are separate questions.
	for _, c := range []struct {
		name string
		set  Set
		want string
	}{
		{
			"entity with no text",
			Set{Entities: []Entity{at("Alice", "PERSON", 1), {Label: "ORG", Score: 1}}},
			"empty text",
		},
		{
			"relation missing an endpoint",
			Set{Relations: []Relation{{Subject: at("Alice", "PERSON", 1), Predicate: "knows", Score: 1}}},
			"missing an endpoint",
		},
		{
			"relation joining a thing to itself",
			Set{Relations: []Relation{{
				Subject: at("Alice", "PERSON", 1), Predicate: "knows",
				Object: at("Alice", "PERSON", 1), Score: 1,
			}}},
			"joining a thing to itself",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := Floor(0).Check(c.set)
			if r.OK {
				t.Fatalf("OK = true, want the fault reported")
			}
			if len(r.Errs) != 1 || !strings.Contains(r.Errs[0], c.want) {
				t.Fatalf("Errs = %v, want one containing %q", r.Errs, c.want)
			}
			if err := r.Err(); !errors.Is(err, ErrInvalid) {
				t.Errorf("Err() = %v, want it to wrap ErrInvalid", err)
			}
		})
	}
}

func TestFloorEmptySetScoresZero(t *testing.T) {
	// Nothing extracted is not a fault, but it is also not evidence of
	// quality — unlike a Schema check, which an empty set passes vacuously.
	r := Floor(0.5).Check(Set{})
	if !r.OK || r.Score != 0 {
		t.Errorf("OK=%v Score=%v, want true 0", r.OK, r.Score)
	}
}

func TestFloorKeep(t *testing.T) {
	kept := Floor(0.7).Keep(Set{
		Entities: []Entity{at("Alice", "PERSON", 0.9), at("Acme", "ORG", 0.6), at("Paris", "GPE", 0.7)},
		Relations: []Relation{
			{Subject: at("Alice", "PERSON", 1), Predicate: "worksAt", Object: at("Acme", "ORG", 1), Score: 0.9},
			{Subject: at("Alice", "PERSON", 1), Predicate: "knows", Object: at("Bob", "PERSON", 1), Score: 0.4},
		},
	})

	var texts []string
	for _, e := range kept.Entities {
		texts = append(texts, e.Text)
	}
	if len(texts) != 2 || texts[0] != "Alice" || texts[1] != "Paris" {
		t.Errorf("kept entities = %v, want [Alice Paris]: the floor is inclusive", texts)
	}
	if len(kept.Relations) != 1 || kept.Relations[0].Predicate != "worksAt" {
		t.Errorf("kept relations = %+v, want only worksAt", kept.Relations)
	}
}

func TestChecksCompose(t *testing.T) {
	// Schema and Floor ask orthogonal questions of the same extraction, so a
	// caller runs both. Here the labels conform but the confidence does not,
	// and the reverse.
	x := Set{Entities: []Entity{
		{Text: "Alice", Label: "Person", End: 5, Score: 0.2},
		{Text: "Widget", Label: "Product", End: 6, Score: 0.99},
	}}
	s := NewSchema(ont)

	if s.Check(x).OK {
		t.Error("Schema accepted an off-vocabulary label")
	}
	if !Floor(0.1).Check(x).OK {
		t.Error("Floor rejected an extraction whose only fault is its vocabulary")
	}

	kept := Floor(0.5).Keep(s.Keep(x))
	if len(kept.Entities) != 0 {
		t.Errorf("kept = %+v, want nothing: Alice fails the floor, Widget the schema", kept.Entities)
	}
}
