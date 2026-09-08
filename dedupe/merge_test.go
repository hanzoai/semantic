package dedupe

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/hanzoai/semantic"
)

func TestMergeKeepsTheRecordTheStrategyNames(t *testing.T) {
	es := firms()[:2] // Apple Inc. then Apple
	rich := Entity{ID: "e9", Name: "Apple", Score: 0.99, Props: map[string]any{"industry": "Technology"}}
	for _, c := range []struct {
		keep Keep
		es   []Entity
		want string
	}{
		{First, es, "e1"},
		{Last, es, "e2"},
		// Both records carry two properties and one edge, so the fullest is
		// the one given first.
		{Fullest, es, "e1"},
		{Surest, es, "e1"},
		{Surest, append([]Entity{es[0]}, rich), "e9"},
		{Fullest, []Entity{{ID: "thin", Name: "Apple"}, es[0]}, "e1"},
	} {
		m, err := Merge(c.es, c.keep)
		if err != nil {
			t.Fatal(err)
		}
		if m.Entity.ID != c.want {
			t.Errorf("%s kept %q, want %q", c.keep, m.Entity.ID, c.want)
		}
		if m.Keep != c.keep {
			t.Errorf("merge recorded %q, want %q", m.Keep, c.keep)
		}
	}
}

func TestMergeTakesEveryPropertyAndRecordsEveryClash(t *testing.T) {
	m, err := Merge(firms()[:2], First)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Entity.Props["industry"]; got != "Technology" {
		t.Errorf("industry = %v, want the kept record's value", got)
	}
	if len(m.Clash) != 2 {
		t.Fatalf("recorded %d clashes, want industry and headquarters: %v", len(m.Clash), m.Clash)
	}
	want := map[string][]any{
		"headquarters": {"Cupertino", "Cupertino, CA"},
		"industry":     {"Technology", "Tech"},
	}
	for _, c := range m.Clash {
		if !reflect.DeepEqual(c.Values, want[c.Prop]) {
			t.Errorf("clash on %q holds %v, want %v", c.Prop, c.Values, want[c.Prop])
		}
		if c.Took != c.Values[0] {
			t.Errorf("clash on %q took %v, want the kept record's %v", c.Prop, c.Took, c.Values[0])
		}
		if c.How != First {
			t.Errorf("clash on %q settled by %q, want first", c.Prop, c.How)
		}
	}
}

func TestMergeAddsPropertiesTheKeptRecordLacks(t *testing.T) {
	es := []Entity{
		{ID: "a", Name: "Ada", Props: map[string]any{"born": 1815}},
		{ID: "b", Name: "Ada", Props: map[string]any{"died": 1852}},
	}
	m, err := Merge(es, First)
	if err != nil {
		t.Fatal(err)
	}
	if m.Entity.Props["born"] != 1815 || m.Entity.Props["died"] != 1852 {
		t.Errorf("merged properties = %v, want both records' values", m.Entity.Props)
	}
	if len(m.Clash) != 0 {
		t.Errorf("records that state different properties do not clash: %v", m.Clash)
	}
}

func TestMergeAllKeepsBothValues(t *testing.T) {
	es := []Entity{
		{ID: "a", Props: map[string]any{"industry": "Technology"}},
		{ID: "b", Props: map[string]any{"industry": "Tech"}},
		{ID: "c", Props: map[string]any{"industry": "Technology"}},
	}
	m, err := Merge(es, All)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := m.Entity.Props["industry"], []any{"Technology", "Tech"}; !reflect.DeepEqual(got, want) {
		t.Errorf("industry = %v, want %v — repeated values are not added twice", got, want)
	}
}

func TestMergeUnionsEdges(t *testing.T) {
	shared := rel("Apple", "competitor", "Microsoft")
	es := []Entity{
		{ID: "a", Edges: []semantic.Triple{shared, rel("Apple", "competitor", "Google")}},
		{ID: "b", Edges: []semantic.Triple{shared, rel("Apple", "founder", "Jobs")}},
	}
	m, err := Merge(es, First)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Entity.Edges) != 3 {
		t.Errorf("merged %d edges, want 3 — the shared edge is one edge", len(m.Entity.Edges))
	}
}

func TestMergeRecordsWhatItWasMadeFrom(t *testing.T) {
	m, err := Merge(firms()[:2], First)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"e1", "e2"}; !reflect.DeepEqual(m.Kept, want) {
		t.Errorf("kept %v, want %v", m.Kept, want)
	}
	if !reflect.DeepEqual(m.Entity.Meta["merged"], m.Kept) {
		t.Errorf("the merged record does not name what it was made from: %v", m.Entity.Meta)
	}
	if m.Entity.Meta["keep"] != string(First) {
		t.Errorf("the merged record does not say how it was made: %v", m.Entity.Meta)
	}
	if len(m.From) != 2 {
		t.Errorf("merge kept %d source records, want 2", len(m.From))
	}
}

func TestMergeLeavesItsInputAlone(t *testing.T) {
	es := firms()[:2]
	before := reflect.DeepEqual(es, firms()[:2])
	if !before {
		t.Fatal("fixture is not reproducible")
	}
	if _, err := Merge(es, All); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(es, firms()[:2]) {
		t.Errorf("merge changed the records it was given: %v", es)
	}
}

func TestMergeFillsAnEmptyNameAndKind(t *testing.T) {
	es := []Entity{{ID: "a"}, {ID: "b", Name: "Ada", Kind: "Person"}}
	m, err := Merge(es, First)
	if err != nil {
		t.Fatal(err)
	}
	if m.Entity.Name != "Ada" || m.Entity.Kind != "Person" {
		t.Errorf("merged to name %q kind %q, want the values the other record states", m.Entity.Name, m.Entity.Kind)
	}
}

func TestMergeOfOneAndOfNone(t *testing.T) {
	if _, err := Merge(nil, First); !errors.Is(err, ErrEmpty) {
		t.Errorf("merging nothing gave %v, want ErrEmpty", err)
	}
	m, err := Merge(firms()[:1], Fullest)
	if err != nil {
		t.Fatal(err)
	}
	if m.Entity.ID != "e1" || len(m.Clash) != 0 {
		t.Errorf("merging one record changed it: %+v", m)
	}
}

func TestFuseMergesEachGroupItFinds(t *testing.T) {
	ms, err := Fuse(context.Background(), firms(), loose, Fullest)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 {
		t.Fatalf("fused into %d records, want 1 — only the Apple records match", len(ms))
	}
	if ms[0].Entity.ID != "e1" {
		t.Errorf("kept %q, want the fullest record", ms[0].Entity.ID)
	}
	if got, want := ms[0].Kept, []string{"e1", "e2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("fused record was made from %v, want %v", got, want)
	}
}

func TestFusePassesOptionErrorsBack(t *testing.T) {
	if _, err := Fuse(context.Background(), firms(), Opt{Like: 2}, First); !errors.Is(err, ErrOption) {
		t.Errorf("error = %v, want ErrOption", err)
	}
}
