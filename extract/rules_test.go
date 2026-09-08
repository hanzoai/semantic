package extract

import (
	"context"
	"reflect"
	"regexp"
	"testing"

	"github.com/hanzoai/semantic"
)

// Rules is a pipeline stage that needs no model.
var _ semantic.Extractor = Rules{}

// span is what an entity says, without the confidence and provenance the
// individual tests assert separately.
type span struct {
	text       string
	label      string
	start, end int
}

func spans(es []Entity) []span {
	var out []span
	for _, e := range es {
		out = append(out, span{e.Text, e.Label, e.Start, e.End})
	}
	return out
}

func TestRulesEntities(t *testing.T) {
	for _, c := range []struct {
		name string
		text string
		want []span
	}{
		{
			"a name, a company and a place",
			"Apple Inc. was founded by Steve Jobs in Cupertino City.",
			[]span{
				{"Apple Inc", "ORG", 0, 9},
				{"Steve Jobs", "PERSON", 26, 36},
				{"Cupertino City", "GPE", 40, 54},
			},
		},
		{
			"money, dates and percentages",
			"Alice paid $1,200 on 03/14/2021 for a 15% stake.",
			[]span{
				{"$1,200", "MONEY", 11, 17},
				{"03/14/2021", "DATE", 21, 31},
				{"15%", "PERCENT", 38, 41},
			},
		},
		{
			// A capitalised word is the last resort, and only when nothing
			// else matched: it is worth more downstream than an empty answer.
			"nothing but a capitalised word",
			"Hello world, nothing to see.",
			[]span{{"Hello", "UNKNOWN", 0, 5}},
		},
		{
			"no text, no entities",
			"",
			nil,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := spans(Rules{}.Entities(c.text)); !reflect.DeepEqual(got, c.want) {
				t.Errorf("entities =\n  %+v\nwant\n  %+v", got, c.want)
			}
		})
	}
}

func TestRulesEntitiesAreOrderedAndDisjoint(t *testing.T) {
	// "Apple Inc" matches both the company pattern and the two-capitalised-
	// words name pattern. Only one survives, and the result reads in text
	// order whatever order the patterns ran in.
	es := Rules{}.Entities("Apple Inc. was founded by Steve Jobs in Cupertino City.")
	for i, e := range es {
		if i > 0 && e.Start < es[i-1].End {
			t.Fatalf("entity %d %+v overlaps %+v", i, e, es[i-1])
		}
	}
	if es[0].Label != "ORG" {
		t.Errorf("Apple Inc labelled %q, want ORG: the company pattern is the specific one", es[0].Label)
	}
}

func TestRulesGazetteerOutranksPattern(t *testing.T) {
	// A name the caller stated beats a name a pattern guessed over the same
	// span, and matches case-insensitively on whole words.
	es := Rules{Names: map[string]string{"acme": "ORG", "Steve Jobs": "FOUNDER"}}.
		Entities("Steve Jobs left ACME today.")

	want := []span{{"Steve Jobs", "FOUNDER", 0, 10}, {"ACME", "ORG", 16, 20}}
	if got := spans(es); !reflect.DeepEqual(got, want) {
		t.Fatalf("entities = %+v, want %+v", got, want)
	}
	for _, e := range es {
		if e.Score != scoreName {
			t.Errorf("%q scored %v, want %v", e.Text, e.Score, scoreName)
		}
		if e.Meta["by"] != "name" {
			t.Errorf("%q came by %v, want name", e.Text, e.Meta["by"])
		}
	}
}

func TestRulesGazetteerMatchesWholeWordsOnly(t *testing.T) {
	es := Rules{Names: map[string]string{"Apple": "ORG"}}.Entities("Pineapple juice.")
	for _, e := range es {
		if e.Meta["by"] == "name" {
			t.Errorf("matched %q inside a longer word", e.Text)
		}
	}
}

func TestRulesMin(t *testing.T) {
	// Pattern finds score 0.75 and gazetteer hits 0.9, so a floor between the
	// two keeps exactly the stated names.
	r := Rules{Names: map[string]string{"Cupertino City": "GPE"}, Min: 0.8}
	got := spans(r.Entities("Apple Inc. was founded by Steve Jobs in Cupertino City."))
	want := []span{{"Cupertino City", "GPE", 40, 54}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("entities = %+v, want %+v", got, want)
	}
}

func TestRulesCustomPatterns(t *testing.T) {
	// A caller's patterns replace the built-in set outright, and the first
	// capturing group is the entity.
	r := Rules{Patterns: map[string]*regexp.Regexp{
		"TICKER": regexp.MustCompile(`\$([A-Z]{2,5})\b`),
	}}
	got := spans(r.Entities("Bought $AAPL and $MSFT today."))
	want := []span{{"AAPL", "TICKER", 8, 12}, {"MSFT", "TICKER", 18, 22}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("entities = %+v, want %+v", got, want)
	}
}

func TestRulesRelations(t *testing.T) {
	text := "Apple Inc. was founded by Steve Jobs in Cupertino City."
	r := Rules{}
	es := r.Entities(text)
	rels := r.Relations(text, es)

	type edge struct{ s, p, o string }
	got := make([]edge, 0, len(rels))
	for _, x := range rels {
		got = append(got, edge{x.Subject.Text, x.Predicate, x.Object.Text})
	}
	want := []edge{
		{"Apple Inc", "founded_by", "Steve Jobs"},
		{"Steve Jobs", "located_in", "Cupertino City"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("relations = %+v, want %+v", got, want)
	}
	for _, x := range rels {
		if x.Score != scoreRel {
			t.Errorf("%+v scored %v, want %v", x, x.Score, scoreRel)
		}
		if x.Context == "" {
			t.Errorf("%+v carries no context, so nothing can check it later", x)
		}
	}
}

func TestRulesRelationsNeedKnownEndpoints(t *testing.T) {
	// Requiring both endpoints to be entities already found is what keeps a
	// pattern from swallowing a run of prose that merely ends in the right
	// verb.
	text := "The company that we all remember was founded by Steve Jobs."
	only := []Entity{{Text: "Steve Jobs", Label: "PERSON", Start: 48, End: 58, Score: 1}}
	if got := (Rules{}).Relations(text, only); len(got) != 0 {
		t.Errorf("relations = %+v, want none: the subject is not a known entity", got)
	}
	if got := (Rules{}).Relations(text, nil); got != nil {
		t.Errorf("relations = %+v, want nil with no entities", got)
	}
}

func TestRulesRelationsRespectMin(t *testing.T) {
	text := "Apple Inc. was founded by Steve Jobs."
	es := Rules{}.Entities(text)
	if got := (Rules{Min: 0.8}).Relations(text, es); got != nil {
		t.Errorf("relations = %+v, want nil: no pattern relation reaches the floor", got)
	}
}

func TestRulesExtract(t *testing.T) {
	c := semantic.Chunk{DocID: "doc-1", Index: 2, Text: "Apple Inc. was founded by Steve Jobs."}
	got, err := Rules{}.Extract(context.Background(), c)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("triples = %+v, want one", got)
	}
	want := semantic.Triple{
		Subject: "Apple Inc", Predicate: "founded_by", Object: "Steve Jobs",
		From: c, Score: scoreRel,
	}
	if first := got[0]; first != want {
		t.Errorf("triple = %+v, want %+v", first, want)
	}
}

func TestSetTriplesAttributesToChunk(t *testing.T) {
	c := semantic.Chunk{DocID: "d", Index: 7, Text: "..."}
	x := Set{Relations: []Relation{{
		Subject:   Entity{Text: "Alice"},
		Predicate: "knows",
		Object:    Entity{Text: "Bob"},
		Score:     0.42,
	}}}
	got := x.Triples(c)
	if len(got) != 1 || got[0].From != c || got[0].Score != 0.42 {
		t.Fatalf("triples = %+v, want one attributed to %+v", got, c)
	}
	if got[0].Subject != "Alice" || got[0].Object != "Bob" {
		t.Errorf("triple = %+v, want Alice knows Bob", got[0])
	}
}

func TestRulesFind(t *testing.T) {
	x := Rules{}.Find("Apple Inc. was founded by Steve Jobs.")
	if len(x.Entities) != 2 || len(x.Relations) != 1 {
		t.Fatalf("Find = %d entities, %d relations; want 2 and 1", len(x.Entities), len(x.Relations))
	}
}

// A name does not cross a line break. Reading one that does turns a heading
// and the sentence beneath it into a single entity that is neither, and takes
// the real relation down with it — the two names are no longer there to be
// joined.
func TestNamesDoNotCrossLines(t *testing.T) {
	const text = "# Notes\n\nAda Lovelace works for Babbage Engines.\n"
	set := Rules{}.Find(text)
	if got := texts(set.Entities); !reflect.DeepEqual(got, []string{"Ada Lovelace", "Babbage Engines"}) {
		t.Fatalf("entities = %q, want [Ada Lovelace Babbage Engines]", got)
	}
	if len(set.Relations) != 1 {
		t.Fatalf("%d relations, want 1: %v", len(set.Relations), set.Relations)
	}
	r := set.Relations[0]
	if r.Subject.Text != "Ada Lovelace" || r.Predicate != "works_for" || r.Object.Text != "Babbage Engines" {
		t.Errorf("relation = %q %q %q", r.Subject.Text, r.Predicate, r.Object.Text)
	}

	// The same holds for the organisation and place patterns.
	if got := texts(Rules{}.Entities("Acme\n\nWidgets Inc")); !reflect.DeepEqual(got, []string{"Widgets Inc"}) {
		t.Errorf("ORG across a break = %q, want [Widgets Inc]", got)
	}
	if got := texts(Rules{}.Entities("Kansas\n\nNew City")); !reflect.DeepEqual(got, []string{"New City"}) {
		t.Errorf("GPE across a break = %q, want [New City]", got)
	}
}

// texts is the surface form of each entity, for the cases that care about
// what was found rather than where.
func texts(es []Entity) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Text
	}
	return out
}
