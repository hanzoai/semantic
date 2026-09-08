package dedupe

import (
	"reflect"
	"testing"
	"time"

	"github.com/hanzoai/semantic"
)

// said is one record of a thing as one document stated it, which is the shape
// every conflict is found in.
func said(id, doc string, confidence float64, props map[string]any) Entity {
	return Entity{ID: id, Props: props, From: Source{Doc: doc, Score: confidence}}
}

// ages is the Python conflict fixture: one thing, two documents, two ages.
func ages() []Entity {
	return []Entity{
		said("e1", "source1", 0.9, map[string]any{"age": 30}),
		said("e1", "source2", 0.8, map[string]any{"age": 32}),
	}
}

func values(c Conflict) []any {
	out := make([]any, 0, len(c.Values))
	for _, v := range c.Values {
		out = append(out, v.Value)
	}
	return out
}

func TestValuesFindsDocumentsThatDisagree(t *testing.T) {
	cs := Values(ages(), "age")
	if len(cs) != 1 {
		t.Fatalf("found %d conflicts, want 1: %v", len(cs), cs)
	}
	c := cs[0]
	if c.Kind != Value || c.Subject != "e1" || c.Prop != "age" {
		t.Errorf("conflict = %+v, want a value conflict about e1's age", c)
	}
	if got, want := values(c), []any{30, 32}; !reflect.DeepEqual(got, want) {
		t.Errorf("values %v, want %v", got, want)
	}
	if c.ID != "e1.age#value" {
		t.Errorf("id = %q", c.ID)
	}
	if c.Level != Medium {
		t.Errorf("level = %q, want medium — two ages two apart", c.Level)
	}
	if c.Values[0].From.Doc != "source1" || c.Values[1].From.Doc != "source2" {
		t.Errorf("values lost the documents behind them: %+v", c.Values)
	}
}

func TestValuesNeedsTwoDocuments(t *testing.T) {
	for _, c := range []struct {
		name string
		es   []Entity
	}{
		{"one document", []Entity{said("e1", "source1", 0.9, map[string]any{"age": 30})}},
		{"two documents agreeing", []Entity{
			said("e1", "source1", 0.9, map[string]any{"age": 30}),
			said("e1", "source2", 0.9, map[string]any{"age": 30}),
		}},
		{"two documents about different things", []Entity{
			said("e1", "source1", 0.9, map[string]any{"age": 30}),
			said("e2", "source2", 0.9, map[string]any{"age": 32}),
		}},
		{"a property only one states", []Entity{
			said("e1", "source1", 0.9, map[string]any{"age": 30}),
			said("e1", "source2", 0.9, map[string]any{"height": 180}),
		}},
	} {
		if cs := Values(c.es, "age"); len(cs) != 0 {
			t.Errorf("%s: found %v, want nothing", c.name, cs)
		}
	}
}

func TestValuesGradesSpellingLowAndIdentityCritical(t *testing.T) {
	for _, c := range []struct {
		name  string
		prop  string
		a, b  any
		level Level
	}{
		{"a spelling", "town", "New York", "new  york", Low},
		{"a name", "name", "Ada Lovelace", "Ada Byron", Critical},
		{"numbers far apart", "revenue", 100, 100000, High},
		{"numbers close together", "age", 30, 32, Medium},
	} {
		es := []Entity{
			said("e1", "a", 0.9, map[string]any{c.prop: c.a}),
			said("e1", "b", 0.9, map[string]any{c.prop: c.b}),
		}
		cs := Values(es, c.prop)
		if len(cs) != 1 {
			t.Fatalf("%s: found %d conflicts, want 1", c.name, len(cs))
		}
		if cs[0].Level != c.level {
			t.Errorf("%s: level %q, want %q", c.name, cs[0].Level, c.level)
		}
	}
}

func TestValuesConfidenceRisesWithTheDocumentsBehindIt(t *testing.T) {
	weak := []Entity{
		said("e1", "a", 0.2, map[string]any{"age": 30}),
		said("e1", "b", 0.2, map[string]any{"age": 32}),
	}
	if got := Values(weak, "age")[0].Score; got >= Values(ages(), "age")[0].Score {
		t.Errorf("unsure documents gave confidence %v, want less than sure ones", got)
	}
	if got := Values(weak, "age")[0].Score; got != 0.4 {
		t.Errorf("confidence = %v, want 0.4: two documents 0.2 sure, disagreeing entirely", got)
	}
}

func TestTypesAndExcludes(t *testing.T) {
	kinds := func(a, b string) []Entity {
		return []Entity{
			{ID: "e2", Kind: a, From: Source{Doc: "s1"}},
			{ID: "e2", Kind: b, From: Source{Doc: "s2"}},
		}
	}
	for _, c := range []struct {
		a, b            string
		types, excludes int
	}{
		{"Person", "Organization", 1, 1}, // different, and cannot both hold
		{"Person", "Employee", 1, 0},     // different, but both may hold
		{"Person", "Person", 0, 0},       // agreement
		{"Person", "person", 1, 0},       // a spelling, not a disagreement of kind
		{"Company", "Person", 1, 1},      // the table reads both ways
	} {
		if got := len(Types(kinds(c.a, c.b))); got != c.types {
			t.Errorf("Types(%s,%s) = %d, want %d", c.a, c.b, got, c.types)
		}
		if got := len(Excludes(kinds(c.a, c.b), nil)); got != c.excludes {
			t.Errorf("Excludes(%s,%s) = %d, want %d", c.a, c.b, got, c.excludes)
		}
	}
}

func TestExcludesIsCriticalAndCertain(t *testing.T) {
	cs := Excludes([]Entity{
		{ID: "e1", Kind: "Person", From: Source{Doc: "doc1"}},
		{ID: "e1", Kind: "Organization", From: Source{Doc: "doc2"}},
	}, nil)
	if len(cs) != 1 {
		t.Fatalf("found %d conflicts, want 1", len(cs))
	}
	if cs[0].Kind != Logic || cs[0].Level != Critical || cs[0].Score != 1 {
		t.Errorf("conflict = %+v, want a certain, critical logic conflict", cs[0])
	}
	if got, want := values(cs[0]), []any{"Person", "Organization"}; !reflect.DeepEqual(got, want) {
		t.Errorf("values %v, want %v", got, want)
	}
}

func TestExcludesTakesItsOwnTable(t *testing.T) {
	es := []Entity{
		{ID: "e1", Kind: "Draft", From: Source{Doc: "a"}},
		{ID: "e1", Kind: "Final", From: Source{Doc: "b"}},
	}
	if cs := Excludes(es, nil); len(cs) != 0 {
		t.Errorf("the default table knows nothing of drafts: %v", cs)
	}
	if cs := Excludes(es, Disjoint{"draft": {"final"}}); len(cs) != 1 {
		t.Errorf("a table naming them found %d conflicts, want 1", len(cs))
	}
}

func TestTimesComparesDatesByTheYearTheyName(t *testing.T) {
	for _, c := range []struct {
		name string
		a, b any
		want int
	}{
		{"different years", "1998", "2004", 1},
		{"the same year", "1998", "1998", 0},
		{"the same year written differently", "founded in 1998", 1998, 0},
		{"a year and a full date", "1998-04-01", "1998", 0},
		{"neither is a date", "spring", "summer", 1},
	} {
		es := []Entity{
			said("e1", "doc1", 0.9, map[string]any{"founded": c.a}),
			said("e1", "doc2", 0.8, map[string]any{"founded": c.b}),
		}
		cs := Times(es)
		if len(cs) != c.want {
			t.Errorf("%s: found %d conflicts, want %d", c.name, len(cs), c.want)
		}
		if c.want == 1 && (cs[0].Kind != Time || cs[0].Prop != "founded") {
			t.Errorf("%s: conflict = %+v", c.name, cs[0])
		}
	}
}

func TestTimesReadsOnlyThePropertiesItIsGiven(t *testing.T) {
	es := []Entity{
		said("e1", "doc1", 0.9, map[string]any{"founded": "1998", "seen": "2001"}),
		said("e1", "doc2", 0.9, map[string]any{"founded": "2004", "seen": "2002"}),
	}
	if got := len(Times(es)); got != 1 {
		t.Errorf("the default list found %d conflicts, want the founding alone", got)
	}
	if got := len(Times(es, "seen")); got != 1 {
		t.Errorf("naming a property found %d conflicts, want 1", got)
	}
	if got := len(Times(es, "founded", "seen")); got != 2 {
		t.Errorf("naming both found %d conflicts, want 2", got)
	}
}

func TestOverlapsFindsSpansThatCannotBothHold(t *testing.T) {
	span := func(doc, from, to string) Entity {
		return said("e1", doc, 0.9, map[string]any{"start": from, "end": to})
	}
	for _, c := range []struct {
		name string
		es   []Entity
		want int
	}{
		{"spans that run over one another", []Entity{
			span("doc1", "2000-01-01", "2005-01-01"),
			span("doc2", "2003-01-01", "2008-01-01"),
		}, 1},
		{"spans that follow one another", []Entity{
			span("doc1", "2000-01-01", "2002-01-01"),
			span("doc2", "2003-01-01", "2005-01-01"),
		}, 0},
		{"the same span twice", []Entity{
			span("doc1", "2000-01-01", "2005-01-01"),
			span("doc2", "2000-01-01", "2005-01-01"),
		}, 0},
		{"a span inside another", []Entity{
			span("doc1", "2000-01-01", "2009-01-01"),
			span("doc2", "2003-01-01", "2005-01-01"),
		}, 1},
		{"one span only", []Entity{span("doc1", "2000-01-01", "2005-01-01")}, 0},
		{"a span that runs backwards is not a span", []Entity{
			span("doc1", "2005-01-01", "2000-01-01"),
			span("doc2", "2003-01-01", "2008-01-01"),
		}, 0},
	} {
		cs := Overlaps(c.es, "start", "end")
		if len(cs) != c.want {
			t.Errorf("%s: found %d conflicts, want %d", c.name, len(cs), c.want)
		}
		if c.want == 1 && cs[0].Kind != Time {
			t.Errorf("%s: kind = %q, want time", c.name, cs[0].Kind)
		}
	}
}

func TestOverlapsReadsTimesHoweverTheyAreWritten(t *testing.T) {
	es := []Entity{
		said("e1", "doc1", 0.9, map[string]any{"start": time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), "end": "2005"}),
		said("e1", "doc2", 0.9, map[string]any{"start": "2003-01-01", "end": "2008-01-01T00:00:00Z"}),
	}
	if got := len(Overlaps(es, "start", "end")); got != 1 {
		t.Errorf("found %d conflicts, want 1", got)
	}
}

func TestLimitsFindsValuesOutsideTheirRange(t *testing.T) {
	lim := map[string]Limit{"age": {Lo: 0, Hi: 130}}
	for _, c := range []struct {
		name string
		age  any
		want int
	}{
		{"inside", 30, 0},
		{"at the top", 130, 0},
		{"above", 200, 1},
		{"below", -1, 1},
		{"written out", "200", 1},
		{"not a number at all", "old", 0},
	} {
		cs := Limits([]Entity{said("e1", "doc1", 0.9, map[string]any{"age": c.age})}, lim)
		if len(cs) != c.want {
			t.Errorf("%s: found %d conflicts, want %d", c.name, len(cs), c.want)
		}
		if c.want == 1 {
			if cs[0].Kind != Range || cs[0].Level != High {
				t.Errorf("%s: conflict = %+v", c.name, cs[0])
			}
			if len(cs[0].Values) != 1 {
				t.Errorf("%s: a range conflict holds the one value that broke it", c.name)
			}
		}
	}
}

func TestEdgesFindsOneSubjectGivenTwoObjects(t *testing.T) {
	tri := func(s, p, o, doc string) semantic.Triple {
		return semantic.Triple{Subject: s, Predicate: p, Object: o, From: semantic.Chunk{DocID: doc}}
	}
	ts := []semantic.Triple{
		tri("Ada", "works at", "Acme", "doc1"),
		tri("Ada", "works at", "Globex", "doc2"),
		tri("Ada", "knows", "Charles", "doc1"),
		tri("Ada", "knows", "Mary", "doc2"),
		tri("Bob", "works at", "Acme", "doc1"),
	}
	cs := Edges(ts, Canon{}, nil)
	if len(cs) != 2 {
		t.Fatalf("found %d conflicts, want both relations read strictly: %v", len(cs), cs)
	}
	many := Edges(ts, Canon{}, map[string]bool{"knows": true})
	if len(many) != 1 {
		t.Fatalf("found %d conflicts, want the employer alone: %v", len(many), many)
	}
	c := many[0]
	if c.Kind != Edge || c.Subject != "ada" || c.Prop != "works at" {
		t.Errorf("conflict = %+v", c)
	}
	if got, want := values(c), []any{"Acme", "Globex"}; !reflect.DeepEqual(got, want) {
		t.Errorf("values %v, want %v", got, want)
	}
	if c.Values[0].From.Doc != "doc1" || c.Values[1].From.Doc != "doc2" {
		t.Errorf("values lost the documents behind them: %+v", c.Values)
	}
}

func TestEdgesFoldsPredicatesThroughTheSynonyms(t *testing.T) {
	c := Canon{Synonyms: map[string]string{"employed by": "works at"}, Fold: true}
	ts := []semantic.Triple{
		{Subject: "Ada", Predicate: "works at", Object: "Acme"},
		{Subject: "Ada", Predicate: "Employed By", Object: "Globex"},
	}
	if got := len(Edges(ts, c, nil)); got != 1 {
		t.Errorf("found %d conflicts, want the two spellings read as one relation", got)
	}
	if got := len(Edges(ts, Canon{}, nil)); got != 0 {
		t.Errorf("without the synonyms they are two relations, got %d conflicts", got)
	}
}

func TestConflictsRunsEveryCheck(t *testing.T) {
	es := []Entity{
		{ID: "e1", Kind: "Person", From: Source{Doc: "doc1", Score: 0.9},
			Props: map[string]any{"age": 30, "founded": "1998"}},
		{ID: "e1", Kind: "Organization", From: Source{Doc: "doc2", Score: 0.8},
			Props: map[string]any{"age": 200, "founded": "2004"}},
	}
	cs := Conflicts(es, Watch{Limits: map[string]Limit{"age": {Lo: 0, Hi: 130}}})
	found := map[Kind]int{}
	for _, c := range cs {
		found[c.Kind]++
	}
	for _, want := range [...]Kind{Value, Type, Time, Range, Logic} {
		if found[want] == 0 {
			t.Errorf("no %s conflict among %v", want, cs)
		}
	}
	if found[Value] != 2 {
		t.Errorf("value conflicts = %d, want the age and the founding", found[Value])
	}
	seen := map[string]bool{}
	for _, c := range cs {
		if seen[c.ID] {
			t.Errorf("two conflicts share the id %q", c.ID)
		}
		seen[c.ID] = true
		if c.Advice == "" {
			t.Errorf("%s says nothing about what to do next", c.ID)
		}
		if c.Score <= 0 || c.Score > 1 {
			t.Errorf("%s is %v sure, want a confidence in 0 to 1", c.ID, c.Score)
		}
	}
}

func TestConflictsComparesOnlyThePropertiesWatched(t *testing.T) {
	es := []Entity{
		said("e1", "doc1", 0.9, map[string]any{"age": 30, "town": "York"}),
		said("e1", "doc2", 0.9, map[string]any{"age": 32, "town": "Leeds"}),
	}
	if got := len(Conflicts(es, Watch{Props: []string{"age"}})); got != 1 {
		t.Errorf("watching one property found %d conflicts, want 1", got)
	}
	if got := len(Conflicts(es, Watch{})); got != 2 {
		t.Errorf("watching everything stated found %d conflicts, want 2", got)
	}
}

func TestClaimsRecordWhoSaidWhat(t *testing.T) {
	got := Claims(ages(), "age")
	if len(got) != 1 {
		t.Fatalf("tracked %d things, want 1", len(got))
	}
	want := []Claim{
		{Value: 30, From: Source{Doc: "source1", Score: 0.9}},
		{Value: 32, From: Source{Doc: "source2", Score: 0.8}},
	}
	if !reflect.DeepEqual(got["e1"], want) {
		t.Errorf("claims = %v, want %v", got["e1"], want)
	}
	if len(Claims(ages(), "height")) != 0 {
		t.Error("a property nobody states has no claims")
	}
}

func TestCount(t *testing.T) {
	es := []Entity{
		{ID: "e1", Kind: "Person", From: Source{Doc: "doc1"}, Props: map[string]any{"age": 30}},
		{ID: "e1", Kind: "Organization", From: Source{Doc: "doc2"}, Props: map[string]any{"age": 32}},
	}
	t1 := Count(Conflicts(es, Watch{}))
	if t1.Total != len(Conflicts(es, Watch{})) {
		t.Errorf("total %d does not match what was counted", t1.Total)
	}
	if t1.Kind[Type] != 1 || t1.Kind[Logic] != 1 || t1.Kind[Value] != 1 {
		t.Errorf("by kind = %v", t1.Kind)
	}
	if t1.Level[Critical] < 1 {
		t.Errorf("by level = %v, want the logic conflict counted critical", t1.Level)
	}
	if t1.Doc["doc1"] != 3 || t1.Doc["doc2"] != 3 {
		t.Errorf("by document = %v, want each document in all three conflicts", t1.Doc)
	}
	if t1.Prop["age"] != 1 || t1.Prop["kind"] != 2 {
		t.Errorf("by property = %v", t1.Prop)
	}
	if empty := Count(nil); empty.Total != 0 || len(empty.Kind) != 0 {
		t.Errorf("counting nothing gave %+v", empty)
	}
}
