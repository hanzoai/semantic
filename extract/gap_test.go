package extract

import (
	"reflect"
	"strings"
	"testing"
)

func TestGapRelatesWhatIsNear(t *testing.T) {
	text := "Alice joined Acme last spring."
	got := Gap{}.Relations(text, []Entity{
		found(text, "Alice", "PERSON"),
		found(text, "Acme", "ORG"),
	})

	if len(got) != 1 {
		t.Fatalf("relations = %+v, want one", got)
	}
	r := got[0]
	if r.Subject.Text != "Alice" || r.Object.Text != "Acme" || r.Predicate != "related_to" {
		t.Errorf("relation = %s %s %s, want Alice related_to Acme", r.Subject.Text, r.Predicate, r.Object.Text)
	}
	if r.Score != 0.6 {
		t.Errorf("Score = %v, want 0.6: nearness is evidence, not proof", r.Score)
	}
	if r.Meta["apart"] != len(" joined ") {
		t.Errorf("Meta = %v, want the distance recorded", r.Meta)
	}
	if !strings.Contains(r.Context, "joined") {
		t.Errorf("Context = %q, want the words that joined them", r.Context)
	}
}

func TestGapIgnoresWhatIsFar(t *testing.T) {
	text := "Alice signed. " + strings.Repeat("x ", 60) + "Acme filed."
	near := []Entity{found(text, "Alice", "PERSON"), found(text, "Acme", "ORG")}
	if got := (Gap{}).Relations(text, near); got != nil {
		t.Errorf("relations = %+v, want none across 120 bytes of filler", got)
	}
	if got := (Gap{Max: 200}).Relations(text, near); len(got) != 1 {
		t.Errorf("relations = %+v, want one when the reach is widened", got)
	}
}

func TestGapNamesThePredicateItIsGiven(t *testing.T) {
	text := "Apple was founded by Steve Jobs."
	known := []Entity{found(text, "Apple", "ORG"), found(text, "Steve Jobs", "PERSON")}

	// A predicate named outright in the words between two entities fits them
	// exactly.
	got := Gap{Preds: []string{"located in", "founded by"}}.Relations(text, known)
	if len(got) != 1 {
		t.Fatalf("relations = %+v, want one", got)
	}
	if got[0].Predicate != "founded by" || got[0].Score != 1 {
		t.Errorf("relation = %q at %v, want \"founded by\" at 1", got[0].Predicate, got[0].Score)
	}
	if got[0].Meta["between"] != "was founded by" {
		t.Errorf("Meta = %v, want the words it read recorded", got[0].Meta)
	}
}

func TestGapDropsWhatItCannotName(t *testing.T) {
	text := "Apple was founded by Steve Jobs."
	known := []Entity{found(text, "Apple", "ORG"), found(text, "Steve Jobs", "PERSON")}

	// Asked for predicates none of which the text supports, Gap says nothing
	// rather than guessing.
	if got := (Gap{Preds: []string{"located in"}}).Relations(text, known); got != nil {
		t.Errorf("relations = %+v, want none", got)
	}
	// The floor is what decides. A predicate the words merely resemble is
	// admitted only when the caller lowers it.
	if got := (Gap{Preds: []string{"founded_by"}}).Relations(text, known); len(got) != 1 {
		t.Errorf("relations = %+v, want one: founded_by resembles \"was founded by\"", got)
	}
	if got := (Gap{Preds: []string{"founded_by"}, Min: 0.9}).Relations(text, known); got != nil {
		t.Errorf("relations = %+v, want none at a floor of 0.9", got)
	}
}

func TestGapReadsAttributes(t *testing.T) {
	// The linking verbs at a floor of 1: state the verb the sentence used,
	// and state nothing where it used one it was not given.
	verbs := Gap{Preds: []string{"is", "was", "has", "founded", "located"}, Min: 1}

	for _, c := range []struct {
		text    string
		subject string
		object  string
		want    string
	}{
		{"Apple is a Corporation.", "Apple", "Corporation", "is"},
		{"Acme has 400 Employees.", "Acme", "Employees", "has"},
		{"Alice joined Acme.", "Alice", "Acme", ""}, // joined is not one of them
	} {
		t.Run(c.text, func(t *testing.T) {
			known := []Entity{found(c.text, c.subject, "ORG"), found(c.text, c.object, "X")}
			got := verbs.Relations(c.text, known)
			if c.want == "" {
				if got != nil {
					t.Fatalf("relations = %+v, want none", got)
				}
				return
			}
			if len(got) != 1 || got[0].Predicate != c.want {
				t.Fatalf("relations = %+v, want one saying %q", got, c.want)
			}
		})
	}
}

func TestFitNeedsAWholeWord(t *testing.T) {
	// "is" inside "this" names nothing.
	if _, score := fit("this belongs to", []string{"is"}); score == 1 {
		t.Error("a predicate was read out of the middle of another word")
	}
	if p, score := fit("this is owned by", []string{"is"}); p != "is" || score != 1 {
		t.Errorf("fit = %q, %v; want is, 1", p, score)
	}
}

func TestGapOrdersEndpointsByTheText(t *testing.T) {
	// Two runs over the same text must agree, whatever order the entities
	// reached it in.
	text := "Alice joined Acme last spring."
	a, b := found(text, "Alice", "PERSON"), found(text, "Acme", "ORG")

	forward := Gap{}.Relations(text, []Entity{a, b})
	backward := Gap{}.Relations(text, []Entity{b, a})
	if !reflect.DeepEqual(forward, backward) {
		t.Errorf("relations depend on input order:\n  %+v\n  %+v", forward, backward)
	}
	if forward[0].Subject.Text != "Alice" {
		t.Errorf("subject = %q, want the one the text names first", forward[0].Subject.Text)
	}
}

func TestGapPairsEveryNeighbour(t *testing.T) {
	text := "Alice, Bob and Carol met."
	known := []Entity{
		found(text, "Alice", "PERSON"),
		found(text, "Bob", "PERSON"),
		found(text, "Carol", "PERSON"),
	}
	got := Gap{}.Relations(text, known)
	if len(got) != 3 {
		t.Fatalf("relations = %d, want each of the three pairs once", len(got))
	}
	seen := map[string]bool{}
	for _, r := range got {
		key := r.Subject.Text + "|" + r.Object.Text
		if seen[key] {
			t.Errorf("pair %q stated twice", key)
		}
		seen[key] = true
	}
}

func TestGapNeedsTwoEntities(t *testing.T) {
	text := "Alice signed."
	if got := (Gap{}).Relations(text, []Entity{found(text, "Alice", "PERSON")}); got != nil {
		t.Errorf("relations = %+v, want none from one entity", got)
	}
	if got := (Gap{}).Relations(text, nil); got != nil {
		t.Errorf("relations = %+v, want none from no entities", got)
	}
}

func TestGapSurvivesWrongOffsets(t *testing.T) {
	// Entities from a model carry no real offsets. Reading between them must
	// not index outside the text.
	known := []Entity{
		{Text: "Alice", Label: "PERSON", Start: 0, End: 5, Score: 1},
		{Text: "Acme", Label: "ORG", Start: 900, End: 904, Score: 1},
	}
	if got := (Gap{Max: 10000, Preds: []string{"knows"}}).Relations("short text", known); got != nil {
		t.Errorf("relations = %+v, want none: there are no words between them to read", got)
	}
}

func TestGapDoesNotRelateANameToItself(t *testing.T) {
	// Two mentions of one name are coreference, not a relation.
	text := "Apple sued Samsung. Apple won."
	known := []Entity{
		{Text: "Apple", Label: "ORG", Start: 0, End: 5, Score: 1},
		{Text: "Samsung", Label: "ORG", Start: 11, End: 18, Score: 1},
		{Text: "Apple", Label: "ORG", Start: 20, End: 25, Score: 1},
	}
	for _, r := range (Gap{}).Relations(text, known) {
		if strings.EqualFold(r.Subject.Text, r.Object.Text) {
			t.Errorf("relation = %+v, want no thing joined to itself", r)
		}
	}
}
