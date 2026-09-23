package extract

import (
	"reflect"
	"testing"
)

func TestRatio(t *testing.T) {
	// The similarity Python's difflib reports: twice the length that lies in
	// matching blocks over the summed length.
	for _, c := range []struct {
		a, b string
		want float64
	}{
		{"", "", 1},
		{"abc", "", 0},
		{"abc", "abc", 1},
		{"abcd", "abed", 0.75},              // one block of 2, one of 1
		{"kitten", "sitting", 2 * 4.0 / 13}, // itt, then n
		{"abc", "xyz", 0},
	} {
		if got := Ratio(c.a, c.b); !about(got, c.want) {
			t.Errorf("Ratio(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
		if got := Ratio(c.b, c.a); !about(got, c.want) {
			t.Errorf("Ratio(%q, %q) = %v, want %v (it must be symmetric)", c.b, c.a, got, c.want)
		}
	}
}

func TestNear(t *testing.T) {
	for _, c := range []struct {
		name       string
		text       string
		candidates []string
		want       int
		score      float64
	}{
		{"exact wins outright", "Person", []string{"Widget", "Person"}, 1, 1},
		{"case does not matter", "PERSON", []string{"person"}, 0, 1},
		{"a synonym of the query", "PERSON", []string{"people"}, 0, 0.95},
		{"a query that is a synonym", "company", []string{"org"}, 0, 0.95},
		{"contained as a whole word", "Apple", []string{"Apple Inc."}, 0, 0.88},
		{"nothing in common", "zzz", []string{"qqq"}, -1, 0},
		{"no candidates", "Person", nil, -1, 0},
		{"no query", "", []string{"Person"}, -1, 0},
		{"blank query", "   ", []string{"Person"}, -1, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			i, s := Near(c.text, c.candidates)
			if i != c.want || !about(s, c.score) {
				t.Errorf("Near(%q, %v) = %d, %v; want %d, %v", c.text, c.candidates, i, s, c.want, c.score)
			}
		})
	}
}

// TestNearDirection holds a predicate synonym to the direction of the
// predicate. A synonym stands in for the name it is listed under with the
// same subject and object, so Acme employs Alice is not a synonym of Alice
// works_for Acme, and a name that reads either way — a merger, a bare
// "founder" — is not one of a predicate that has a direction.
func TestNearDirection(t *testing.T) {
	for _, c := range []struct{ said, want string }{
		{"employs", "works_for"},
		{"managed_by", "ceo_of"},
		{"parent_company", "subsidiary_of"},
		{"chief_executive", "ceo_of"},
		{"team_member", "works_for"},
		{"merged_with", "acquired"},
		{"ownership", "acquired"},
		{"founder", "founded_by"},
		{"creator", "founded_by"},
		{"venture_capital", "invested_in"},
	} {
		for _, pair := range [][2]string{{c.said, c.want}, {c.want, c.said}} {
			if _, s := Near(pair[0], []string{pair[1]}); s >= 0.8 {
				t.Errorf("Near(%q, %q) = %v, want below 0.8: they do not read the same way round", pair[0], pair[1], s)
			}
		}
	}
	for _, c := range []struct{ said, want string }{
		{"owned_by", "subsidiary_of"},
		{"employed_by", "works_for"},
		{"head_of", "ceo_of"},
		{"bought", "acquired"},
		{"established_by", "founded_by"},
		{"rival", "competitor_of"},
	} {
		if _, s := Near(c.said, []string{c.want}); !about(s, 0.95) {
			t.Errorf("Near(%q, %q) = %v, want 0.95", c.said, c.want, s)
		}
	}
}

func TestNearFallsBackToRatio(t *testing.T) {
	// Below the containment threshold the longest-matching-blocks ratio
	// decides, so a near-miss spelling still finds its candidate.
	i, s := Near("Organisation", []string{"Widget", "Organization"})
	if i != 1 {
		t.Fatalf("Near = %d, want 1", i)
	}
	if s <= 0.9 || s >= 1 {
		t.Errorf("score = %v, want a high ratio short of an exact match", s)
	}
}

func TestSimilar(t *testing.T) {
	if got := Similar("PERSON", []string{"people"}); !about(got, 0.95) {
		t.Errorf("Similar = %v, want 0.95", got)
	}
	if got := Similar("zzz", nil); got != 0 {
		t.Errorf("Similar with no candidates = %v, want 0", got)
	}
}

func TestBind(t *testing.T) {
	known := []Entity{
		{Text: "Apple Inc.", Label: "ORG", End: 10, Score: 1},
		{Text: "Steve Jobs", Label: "PERSON", End: 10, Score: 1},
	}
	for _, c := range []struct {
		name string
		text string
		min  float64
		want string
	}{
		{"exact", "Steve Jobs", 0.8, "Steve Jobs"},
		{"exact but for case and space", "  apple inc. ", 0.8, "Apple Inc."},
		{"near enough", "Apple", 0.8, "Apple Inc."},
		{"not near enough", "Apple", 0.95, ""},
		{"nothing like it", "Ronald Wayne", 0.8, ""},
		{"no text", "", 0.8, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, ok := Bind(c.text, known, c.min)
			if ok != (c.want != "") || e.Text != c.want {
				t.Errorf("Bind(%q, %v) = %q, %v; want %q", c.text, c.min, e.Text, ok, c.want)
			}
		})
	}
	if _, ok := Bind("Apple", nil, 0.8); ok {
		t.Error("Bind against no entities reported a match")
	}
}

func TestWeigh(t *testing.T) {
	for _, c := range []struct {
		name        string
		label, text string
		score       float64
		want        []string
		expect      float64
	}{
		{"no types to compare against", "PERSON", "", 0.8, nil, 0.8},
		{"the label matches a type", "PERSON", "", 0.8, []string{"people"}, 0.5*0.8 + 0.5*0.95},
		{"the content matches better than the label", "UNKNOWN", "artist", 0.6, []string{"artist"}, 0.5*0.6 + 0.5},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := Weigh(c.label, c.score, c.want, c.text); !about(got, c.expect) {
				t.Errorf("Weigh = %v, want %v", got, c.expect)
			}
		})
	}
}

func TestNarrow(t *testing.T) {
	apple := Entity{Text: "Apple", Label: "ORG", End: 5, Score: 1}
	micro := Entity{Text: "Microsoft", Label: "ORG", End: 9, Score: 1}
	tesla := Entity{Text: "Tesla", Label: "ORG", End: 5, Score: 1}
	acme := Entity{Text: "Acme Corp", Label: "ORG", End: 9, Score: 1}
	all := []Entity{apple, micro, tesla}

	for _, c := range []struct {
		name  string
		text  string
		known []Entity
		max   int
		want  []string
	}{
		{
			"a roster already within the cap is untouched",
			"nothing here at all", all, 3, []string{"Apple", "Microsoft", "Tesla"},
		},
		{
			"only what the text mentions",
			"Apple sued Microsoft yesterday.", all, 2, []string{"Apple", "Microsoft"},
		},
		{
			"a mention by one distinctive word",
			"acme is great", []Entity{acme, micro, tesla}, 2, []string{"Acme Corp"},
		},
		{
			// A filter that removes everything has told the caller nothing,
			// so the longest names are kept instead.
			"nothing mentioned keeps the longest",
			"nothing here at all", all, 2, []string{"Microsoft", "Apple"},
		},
		{"no room", "Apple", all, 0, nil},
		{"no text", "", all, 2, nil},
		{"no entities", "Apple", nil, 2, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			for _, e := range Narrow(c.text, c.known, c.max) {
				got = append(got, e.Text)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("Narrow = %v, want %v", got, c.want)
			}
		})
	}
}

func TestNarrowIgnoresCorporateWords(t *testing.T) {
	// "Inc" alone must not prove that "Widget Inc" is mentioned, or every
	// company in the roster survives every chunk that names any of them.
	known := []Entity{
		{Text: "Widget Inc", Label: "ORG", End: 10, Score: 1},
		{Text: "Apple", Label: "ORG", End: 5, Score: 1},
		{Text: "Tesla", Label: "ORG", End: 5, Score: 1},
	}
	got := Narrow("Apple Inc. reported earnings.", known, 2)
	if len(got) != 1 || got[0].Text != "Apple" {
		t.Errorf("Narrow = %+v, want only Apple", spans(got))
	}
}
