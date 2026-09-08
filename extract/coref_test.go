package extract

import (
	"reflect"
	"strings"
	"testing"
)

// found is the entity a text names at its first occurrence, so the tests
// state offsets by naming the word rather than by counting characters.
func found(text, word, label string) Entity {
	i := strings.Index(text, word)
	if i < 0 {
		panic("no " + word + " in " + text)
	}
	return Entity{Text: word, Label: label, Start: i, End: i + len(word), Score: 1}
}

func TestMentions(t *testing.T) {
	text := "Steve Jobs founded Apple. He led it for years."
	got := Mentions(text, []Entity{
		found(text, "Apple", "ORG"),
		found(text, "Steve Jobs", "PERSON"),
	})

	type m struct {
		text    string
		pronoun bool
	}
	var flat []m
	for _, x := range got {
		flat = append(flat, m{x.Text, x.Pronoun})
	}
	want := []m{{"Steve Jobs", false}, {"Apple", false}, {"He", true}, {"it", true}}
	if !reflect.DeepEqual(flat, want) {
		t.Errorf("mentions = %+v, want %+v (in text order)", flat, want)
	}
}

func TestMentionsReadsLongestPronoun(t *testing.T) {
	// "its" must not be read as "it", or the possessive resolves as if it
	// were the subject.
	got := Mentions("Acme lost its way.", nil)
	if len(got) != 1 || got[0].Text != "its" {
		t.Errorf("mentions = %+v, want one reading of \"its\"", got)
	}
}

func TestRefer(t *testing.T) {
	for _, c := range []struct {
		name  string
		text  string
		marks [][2]string // word, label
		want  []Ref
	}{
		{
			"the nearest name of a kind the pronoun admits",
			"Alice joined Acme. She resigned.",
			[][2]string{{"Alice", "PERSON"}, {"Acme", "ORG"}},
			[]Ref{{"She", "Alice"}},
		},
		{
			"it takes the organisation, he takes the person",
			"Steve Jobs founded Apple. He led it for years.",
			[][2]string{{"Steve Jobs", "PERSON"}, {"Apple", "ORG"}},
			[]Ref{{"He", "Steve Jobs"}, {"it", "Apple"}},
		},
		{
			// A wrong antecedent of the right shape beats no antecedent, and
			// the label is on the chain for a caller who wants to check.
			"the nearest name when no label fits",
			"Widget shipped. It works.",
			[][2]string{{"Widget", "GADGET"}},
			[]Ref{{"It", "Widget"}},
		},
		{
			"nothing precedes the pronoun",
			"It rose before Bitcoin was named.",
			[][2]string{{"Bitcoin", "PRODUCT"}},
			nil,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			var known []Entity
			for _, m := range c.marks {
				known = append(known, found(c.text, m[0], m[1]))
			}
			if got := Refer(Mentions(c.text, known)); !reflect.DeepEqual(got, c.want) {
				t.Errorf("Refer = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestReferRecordsWhatItSettled(t *testing.T) {
	text := "Alice joined Acme. She resigned."
	ms := Mentions(text, []Entity{found(text, "Alice", "PERSON"), found(text, "Acme", "ORG")})
	Refer(ms)
	for _, m := range ms {
		if m.Pronoun && m.Of != "Alice" {
			t.Errorf("%q refers to %q, want it recorded on the mention", m.Text, m.Of)
		}
		if !m.Pronoun && m.Of != "" {
			t.Errorf("%q was given an antecedent; only pronouns take one", m.Text)
		}
	}
}

func TestCoref(t *testing.T) {
	text := "Steve Jobs founded Apple. He led it for years."
	got := Coref(text, []Entity{found(text, "Steve Jobs", "PERSON"), found(text, "Apple", "ORG")})

	if len(got) != 2 {
		t.Fatalf("chains = %+v, want two", got)
	}
	for i, want := range []struct {
		head, label string
		mentions    []string
	}{
		{"Steve Jobs", "PERSON", []string{"Steve Jobs", "He"}},
		{"Apple", "ORG", []string{"Apple", "it"}},
	} {
		c := got[i]
		var texts []string
		for _, m := range c.Mentions {
			texts = append(texts, m.Text)
		}
		if c.Head.Text != want.head || c.Label != want.label || !reflect.DeepEqual(texts, want.mentions) {
			t.Errorf("chain %d = head %q label %q %v; want %q %q %v",
				i, c.Head.Text, c.Label, texts, want.head, want.label, want.mentions)
		}
	}
}

func TestCorefReadsPronounsThroughTheirAntecedent(t *testing.T) {
	// A pronoun's own letters say nothing. Comparing them as text is what
	// makes "it" a mention of "Bitcoin", so an unresolved pronoun joins
	// nothing at all.
	text := "It rose before Bitcoin was named."
	if got := Coref(text, []Entity{found(text, "Bitcoin", "PRODUCT")}); got != nil {
		t.Errorf("chains = %+v, want none: the pronoun has no antecedent", got)
	}
}

func TestCorefGroupsRepeatedNames(t *testing.T) {
	text := "Apple sued Samsung. Apple won."
	known := []Entity{
		{Text: "Apple", Label: "ORG", Start: 0, End: 5, Score: 1},
		{Text: "Samsung", Label: "ORG", Start: 11, End: 18, Score: 1},
		{Text: "Apple", Label: "ORG", Start: 20, End: 25, Score: 1},
	}
	got := Coref(text, known)
	if len(got) != 1 || len(got[0].Mentions) != 2 {
		t.Fatalf("chains = %+v, want the two mentions of Apple in one chain", got)
	}
	if head := got[0].Head; head.Text != "Apple" || head.Start != 0 {
		t.Errorf("head = %+v, want the earliest mention", head)
	}
}

func TestCorefKeepsRivalsApart(t *testing.T) {
	// Two companies sharing a word are two things. Only more than seven
	// words in ten, or one name inside the other, makes them one.
	text := "Apple Inc. sued Apple Corps over the name."
	known := []Entity{
		{Text: "Apple Inc.", Label: "ORG", Start: 0, End: 10, Score: 1},
		{Text: "Apple Corps", Label: "ORG", Start: 16, End: 27, Score: 1},
	}
	if got := Coref(text, known); got != nil {
		t.Errorf("chains = %+v, want none", got)
	}
}

func TestCorefNeedsMoreThanOneMention(t *testing.T) {
	text := "Apple shipped."
	if got := Coref(text, []Entity{found(text, "Apple", "ORG")}); got != nil {
		t.Errorf("chains = %+v, want none: a thing named once is not a chain", got)
	}
	if got := Coref("", nil); got != nil {
		t.Errorf("chains = %+v, want none from no text", got)
	}
}

func TestSameNames(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"Apple", "apple", true},
		{"Apple", "Apple Inc.", true},                          // one inside the other
		{"Steve Jobs", "Jobs", true},                           // ditto
		{"New York City Council", "New York City Board", true}, // three words in four
		{"Steve Paul Jobs", "Steve Jobs", false},               // two in three is not enough
		{"Apple", "Microsoft", false},
		{"Alice", "", false},
	} {
		if got := same(c.a, c.b); got != c.want {
			t.Errorf("same(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
