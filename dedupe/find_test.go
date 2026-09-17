package dedupe

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/hanzoai/semantic"
)

// rel is one edge of an entity, written the way the Python fixtures write it.
func rel(subject, predicate, object string) semantic.Triple {
	return semantic.Triple{Subject: subject, Predicate: predicate, Object: object}
}

// firms is the fixture the Python deduplication tests are built on: two
// records of Apple that ought to merge, and one of Microsoft that ought not.
func firms() []Entity {
	return []Entity{{
		ID:    "e1",
		Name:  "Apple Inc.",
		Kind:  "Company",
		Props: map[string]any{"industry": "Technology", "headquarters": "Cupertino"},
		Edges: []semantic.Triple{rel("Apple Inc.", "competitor", "Microsoft")},
	}, {
		ID:    "e2",
		Name:  "Apple",
		Kind:  "Company",
		Props: map[string]any{"industry": "Tech", "headquarters": "Cupertino, CA"},
		Edges: []semantic.Triple{rel("Apple", "competitor", "Google")},
	}, {
		ID:    "e3",
		Name:  "Microsoft Corp",
		Kind:  "Company",
		Props: map[string]any{"industry": "Software"},
	}}
}

// loose is the threshold pair the Python tests use for this fixture.
var loose = Opt{Like: 0.4, Score: 0.4}

func names(p Pair) [2]string { return [2]string{p.A.Name, p.B.Name} }

func TestFindTheAppleRecords(t *testing.T) {
	ps, err := Find(context.Background(), firms(), loose)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 {
		t.Fatalf("found %d pairs, want the Apple pair alone: %v", len(ps), ps)
	}
	if got := names(ps[0]); got != [2]string{"Apple Inc.", "Apple"} {
		t.Errorf("paired %v, want Apple Inc. with Apple", got)
	}
	if ps[0].Like < 0.4 || ps[0].Score < ps[0].Like {
		t.Errorf("likeness %v, confidence %v: the kind they share should raise the confidence",
			ps[0].Like, ps[0].Score)
	}
	if !reflect.DeepEqual(ps[0].Why, []string{"kind"}) {
		t.Errorf("evidence %v, want the shared kind alone — the names and properties differ", ps[0].Why)
	}
}

func TestFindIsRepeatable(t *testing.T) {
	es := append(firms(), Entity{ID: "e4", Name: "Apple Computer", Kind: "Company"})
	var last []Pair
	for i := range 8 {
		ps, err := Find(context.Background(), es, loose)
		if err != nil {
			t.Fatal(err)
		}
		if last != nil && !reflect.DeepEqual(ps, last) {
			t.Fatalf("run %d differs from the one before:\n%v\n%v", i, ps, last)
		}
		last = ps
	}
}

func TestFindNeverPairsDifferentKinds(t *testing.T) {
	people := []Entity{
		{ID: "e1", Kind: "Person", Name: "Alice"},
		{ID: "e2", Kind: "Organization", Name: "Alice"},
	}
	for _, o := range []Opt{loose, {Like: 0.4}, {}} {
		ps, err := Find(context.Background(), people, o)
		if err != nil {
			t.Fatal(err)
		}
		if len(ps) != 0 {
			t.Errorf("opt %+v paired a person with an organisation: %v", o, ps)
		}
	}
}

func TestFindPairsWhereKindsAgreeOrAreUnstated(t *testing.T) {
	for _, c := range []struct {
		name string
		es   []Entity
	}{
		{"same kind, same name", []Entity{
			{ID: "e1", Kind: "Person", Name: "Alice"},
			{ID: "e2", Kind: "Person", Name: "Alice"},
		}},
		{"no kind stated", []Entity{
			{ID: "x1", Name: "Apple"},
			{ID: "x2", Name: "Apple"},
		}},
		{"one kind stated", []Entity{
			{ID: "y1", Kind: "Person", Name: "Alice"},
			{ID: "y2", Name: "Alice"},
		}},
		{"kinds differ only in case", []Entity{
			{ID: "z1", Kind: "person", Name: "Alice"},
			{ID: "z2", Kind: "Person", Name: "Alice"},
		}},
	} {
		ps, err := Find(context.Background(), c.es, loose)
		if err != nil {
			t.Fatal(err)
		}
		if len(ps) != 1 {
			t.Errorf("%s: found %d pairs, want 1", c.name, len(ps))
		}
	}
}

func TestFindRejectsOptionsOutsideTheirRange(t *testing.T) {
	for _, o := range []Opt{
		{Like: -0.1}, {Like: 1.1}, {Score: -1}, {Score: 2}, {Floor: -0.5}, {Floor: 9},
		{Per: -1}, {Max: -2},
	} {
		if _, err := Find(context.Background(), firms(), o); !errors.Is(err, ErrOption) {
			t.Errorf("Find(%+v) error = %v, want ErrOption", o, err)
		}
	}
}

func TestFindStopsWhenTheContextIsDone(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	stop()
	if _, err := Find(ctx, firms(), loose); !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

// ring is n records of one name, so every pair is a duplicate: the fixture
// the ranking and capping cases need.
func ring(n int) []Entity {
	es := make([]Entity, n)
	for i := range es {
		es[i] = Entity{ID: string(rune('a' + i)), Name: "Ada Lovelace"}
	}
	return es
}

func TestLimitCapsAndRanks(t *testing.T) {
	es := ring(4) // six pairs
	all, err := Find(context.Background(), es, loose)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 6 {
		t.Fatalf("four identical records gave %d pairs, want 6", len(all))
	}
	for _, c := range []struct {
		name string
		o    Opt
		want int
	}{
		{"uncapped", loose, 6},
		{"max of two", Opt{Like: 0.4, Score: 0.4, Max: 2}, 2},
		{"max above the count", Opt{Like: 0.4, Score: 0.4, Max: 99}, 6},
		{"one pair per record", Opt{Like: 0.4, Score: 0.4, Per: 1}, 3},
		{"two pairs per record", Opt{Like: 0.4, Score: 0.4, Per: 2}, 5},
		{"cap applied after the per-record cap", Opt{Like: 0.4, Score: 0.4, Per: 1, Max: 2}, 2},
	} {
		ps, err := Find(context.Background(), es, c.o)
		if err != nil {
			t.Fatal(err)
		}
		if len(ps) != c.want {
			t.Errorf("%s: %d pairs, want %d", c.name, len(ps), c.want)
		}
	}
}

func TestLimitRanksByConfidenceThenByLikeness(t *testing.T) {
	es := []Entity{
		{ID: "a", Name: "Ada Lovelace"},
		{ID: "b", Name: "Ada Lovelace"},         // identical: the surest pair
		{ID: "c", Name: "Ada Lovelace Byron"},   // close
		{ID: "d", Name: "Adam Lovelace Bryant"}, // less close
	}
	sure, err := Find(context.Background(), es, Opt{Like: 0.4, Score: 0.4})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(sure); i++ {
		if sure[i-1].Score < sure[i].Score {
			t.Fatalf("confidence out of order at %d: %v", i, sure)
		}
	}
	alike, err := Find(context.Background(), es, Opt{Like: 0.4, Score: 0.4, Alike: true})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(alike); i++ {
		if alike[i-1].Like < alike[i].Like {
			t.Fatalf("likeness out of order at %d: %v", i, alike)
		}
	}
	if sure[0].Score != 1 || len(alike) != len(sure) {
		t.Errorf("the identical pair should lead the ranking: %v", sure[0])
	}
}

func TestLimitKeepsAPairWhileEitherRecordIsUnderQuota(t *testing.T) {
	// A record already at its quota does not block a pair whose other record
	// is still under it — the Python "or" rule.
	ps, err := Find(context.Background(), ring(3), Opt{Like: 0.4, Score: 0.4, Per: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 {
		t.Fatalf("three identical records with a quota of one gave %d pairs, want 2", len(ps))
	}
}

func TestAddComparesFreshAgainstKnownOnly(t *testing.T) {
	es := firms()
	fresh := []Entity{es[1], es[2]} // Apple, Microsoft Corp
	known := []Entity{es[0]}        // Apple Inc.
	ps, err := Add(context.Background(), fresh, known, loose)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 {
		t.Fatalf("found %d pairs, want Apple against Apple Inc. alone: %v", len(ps), ps)
	}
	if got := names(ps[0]); got != [2]string{"Apple", "Apple Inc."} {
		t.Errorf("paired %v, want the fresh record first", got)
	}
}

func TestAddIgnoresPairsWithinTheFreshSet(t *testing.T) {
	fresh := []Entity{{ID: "n1", Name: "Ada Lovelace"}, {ID: "n2", Name: "Ada Lovelace"}}
	ps, err := Add(context.Background(), fresh, nil, loose)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 0 {
		t.Errorf("Add paired two fresh records against each other: %v", ps)
	}
}

func TestGroupsJoinPairsThatShareARecord(t *testing.T) {
	es := []Entity{
		{ID: "a", Name: "Ada Lovelace"},
		{ID: "b", Name: "Ada Lovelace"},
		{ID: "c", Name: "Ada Lovelace"},
		{ID: "z", Name: "Charles Babbage"},
	}
	ps, err := Find(context.Background(), es, loose)
	if err != nil {
		t.Fatal(err)
	}
	gs := Groups(ps)
	if len(gs) != 1 {
		t.Fatalf("built %d groups, want 1", len(gs))
	}
	if len(gs[0].Of) != 3 {
		t.Errorf("group holds %d records, want 3 — sameness is transitive", len(gs[0].Of))
	}
	// Identical records score 0.9 alike, not 1: neither states an edge, and
	// no edges on either side is neutral rather than agreement.
	if got := gs[0].Tight(); math.Abs(got-0.9) > 1e-9 {
		t.Errorf("three identical records are %v tight, want 0.9", got)
	}
	if got, want := gs[0].Score, 0.9*(0.8+0.2*(3.0/5)); math.Abs(got-want) > 1e-9 {
		t.Errorf("group confidence %v, want %v", got, want)
	}
	if loose := Loose(es, gs); len(loose) != 1 || loose[0].ID != "z" {
		t.Errorf("unclaimed records = %v, want Charles Babbage alone", loose)
	}
}

func TestGroupHeadIsTheFullestRecord(t *testing.T) {
	thin := Entity{ID: "a", Name: "Ada"}
	fat := Entity{ID: "b", Name: "Ada", Props: map[string]any{"born": 1815}, Edges: []semantic.Triple{rel("Ada", "wrote", "Note G")}}
	gs := Groups([]Pair{{A: thin, B: fat, Like: 1, Score: 1}})
	if len(gs) != 1 || gs[0].Head.ID != "b" {
		t.Errorf("head = %+v, want the record carrying the most", gs[0].Head)
	}
}

func TestGroupsMergeWhenAPairJoinsTwoOfThem(t *testing.T) {
	a := Entity{ID: "a", Name: "Ada"}
	b := Entity{ID: "b", Name: "Ada"}
	c := Entity{ID: "c", Name: "Ada"}
	d := Entity{ID: "d", Name: "Ada"}
	gs := Groups([]Pair{
		{A: a, B: b, Like: 1, Score: 1},
		{A: c, B: d, Like: 1, Score: 1},
		{A: b, B: c, Like: 1, Score: 1}, // joins the two groups
	})
	if len(gs) != 1 {
		t.Fatalf("built %d groups, want the two joined into 1", len(gs))
	}
	if len(gs[0].Of) != 4 {
		t.Errorf("joined group holds %d records, want 4", len(gs[0].Of))
	}
	if len(gs[0].Like) != 3 {
		t.Errorf("joined group kept %d pair scores, want 3", len(gs[0].Like))
	}
}

func TestGroupsOfNothing(t *testing.T) {
	if gs := Groups(nil); len(gs) != 0 {
		t.Errorf("no pairs gave %d groups", len(gs))
	}
	if l := Loose(firms(), nil); len(l) != 3 {
		t.Errorf("with no groups every record is unclaimed, got %d", len(l))
	}
}

func TestFloorDropsThePairsLeastAlike(t *testing.T) {
	es := []Entity{
		{ID: "a", Name: "Ada Lovelace"},
		{ID: "b", Name: "Ada Lovelace"},
		{ID: "c", Name: "Adam Lovelace Bryant"},
	}
	all, err := Find(context.Background(), es, Opt{Like: 0.4, Score: 0.4})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 2 {
		t.Fatalf("found %d pairs, want at least 2 to have something to drop", len(all))
	}
	floor := all[0].Like
	kept, err := Find(context.Background(), es, Opt{Like: 0.4, Score: 0.4, Floor: floor})
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) >= len(all) {
		t.Errorf("a floor at the best pair's likeness kept %d of %d pairs", len(kept), len(all))
	}
	for _, p := range kept {
		if p.Like < floor {
			t.Errorf("pair %v survived a floor of %v with likeness %v", names(p), floor, p.Like)
		}
	}
}
