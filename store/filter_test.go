package store

import "testing"

// meta is the record every condition below is asked about.
var meta = map[string]any{
	"lang":  "en",
	"year":  2020,
	"score": 0.5,
	"tags":  []string{"a", "b"},
	"title": "hello world",
	"draft": true,
}

func TestFilterMatch(t *testing.T) {
	for _, c := range []struct {
		name string
		f    Filter
		want bool
	}{
		{"empty filter admits everything", Filter{}, true},
		{"nil filter admits everything", nil, true},

		{"equal string", Filter{}.Eq("lang", "en"), true},
		{"unequal string", Filter{}.Eq("lang", "fr"), false},
		{"equal int", Filter{}.Eq("year", 2020), true},
		{"int equals float of the same value", Filter{}.Eq("year", 2020.0), true},
		{"float equals int of the same value", Filter{}.Eq("score", 0.5), true},
		{"equal bool", Filter{}.Eq("draft", true), true},
		{"string is not the number it spells", Filter{}.Eq("year", "2020"), false},

		{"different value", Filter{}.Ne("lang", "fr"), true},
		{"same value", Filter{}.Ne("lang", "en"), false},

		{"greater", Filter{}.Gt("year", 2019), true},
		{"not greater than itself", Filter{}.Gt("year", 2020), false},
		{"at least itself", Filter{}.Ge("year", 2020), true},
		{"less", Filter{}.Lt("year", 2021), true},
		{"not less than itself", Filter{}.Lt("year", 2020), false},
		{"at most itself", Filter{}.Le("year", 2020), true},
		{"strings order lexically", Filter{}.Gt("lang", "de"), true},
		{"a string has no order against a number", Filter{}.Gt("lang", 5), false},

		{"one of", Filter{}.In("lang", "en", "fr"), true},
		{"none of", Filter{}.In("lang", "de", "fr"), false},
		{"one of, numerically", Filter{}.In("year", 2019.0, 2020.0), true},

		{"list holds it", Filter{}.Has("tags", "a"), true},
		{"list does not hold it", Filter{}.Has("tags", "z"), false},
		{"string contains it", Filter{}.Has("title", "world"), true},
		{"contains is case sensitive", Filter{}.Has("title", "World"), false},
		{"a string contains only a string", Filter{}.Has("title", 3), false},

		{"an absent field fails equality", Filter{}.Eq("missing", "x"), false},
		{"an absent field fails inequality too", Filter{}.Ne("missing", "x"), false},
		{"an absent field fails comparison", Filter{}.Gt("missing", 1), false},

		{"every condition must hold", Filter{}.Eq("lang", "en").Ge("year", 2020), true},
		{"one failing condition fails the filter", Filter{}.Eq("lang", "en").Ge("year", 2021), false},
		{"three conditions", Filter{}.Eq("lang", "en").Lt("year", 2021).Has("tags", "b"), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := c.f.Match(meta); got != c.want {
				t.Errorf("Match = %v, want %v", got, c.want)
			}
		})
	}
}

func TestFilterMatchesNilMetadata(t *testing.T) {
	if !(Filter{}).Match(nil) {
		t.Error("an empty filter should admit metadata that is not there")
	}
	if (Filter{}.Eq("lang", "en")).Match(nil) {
		t.Error("a condition cannot hold of metadata that is not there")
	}
}

func TestFilterBuilds(t *testing.T) {
	f := Filter{}.Eq("a", 1).Ne("b", 2).Gt("c", 3).Ge("d", 4).Lt("e", 5).Le("f", 6).
		In("g", 7, 8).Has("h", 9).Add("i", Eq, 10)
	want := []Op{Eq, Ne, Gt, Ge, Lt, Le, In, Has, Eq}
	if len(f) != len(want) {
		t.Fatalf("built %d conditions, want %d", len(f), len(want))
	}
	for i, op := range want {
		if f[i].Op != op {
			t.Errorf("condition %d is %q, want %q", i, f[i].Op, op)
		}
	}
	if got := f[6].Value; len(list(got)) != 2 {
		t.Errorf("In kept %v, want a list of two", got)
	}
}

func TestFilterListsFromJSON(t *testing.T) {
	// What comes back out of a JSON file is []any and float64, and it has
	// to answer the same questions as what went in.
	loaded := map[string]any{"tags": []any{"a", "b"}, "year": 2020.0}
	for _, c := range []struct {
		name string
		f    Filter
		want bool
	}{
		{"list", Filter{}.Has("tags", "b"), true},
		{"list without", Filter{}.Has("tags", "c"), false},
		{"number", Filter{}.Eq("year", 2020), true},
		{"range", Filter{}.Ge("year", 2020).Lt("year", 2021), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := c.f.Match(loaded); got != c.want {
				t.Errorf("Match = %v, want %v", got, c.want)
			}
		})
	}
}

func TestUnknownOpMatchesNothing(t *testing.T) {
	if (Filter{{Field: "lang", Op: "sideways", Value: "en"}}).Match(meta) {
		t.Error("an operator nobody defined should not admit anything")
	}
}
