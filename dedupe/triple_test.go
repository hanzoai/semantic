package dedupe

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/hanzoai/semantic"
)

// claim is one assertion with the confidence its extractor gave it.
func claim(s, p, o string, score float64) semantic.Triple {
	return semantic.Triple{Subject: s, Predicate: p, Object: o, Score: score}
}

func TestUniqueKeepsOneOfEachAssertion(t *testing.T) {
	ts := []semantic.Triple{
		claim("Ada", "works at", "Acme", 0.6),
		claim("Ada", "WORKS_AT", "Acme", 0.9), // the same claim, spelled otherwise
		claim("Ada", "knows", "Charles", 0.7),
		claim("Ada", "works at", "Globex", 0.5), // a different claim
	}
	if got := Unique(ts, Canon{}); len(got) != 4 {
		t.Fatalf("the zero Canon folds case alone, so works_at is its own claim: %v", got)
	}
	got := Unique(ts, Canon{Fold: true})
	if len(got) != 3 {
		t.Fatalf("kept %d assertions, want 3: %v", len(got), got)
	}
	if got[0].Predicate != "works at" {
		t.Errorf("kept %q, want the spelling asserted first", got[0].Predicate)
	}
	if got[0].Score != 0.9 {
		t.Errorf("score = %v, want the best any repetition carried", got[0].Score)
	}
	if len(Unique(nil, Canon{})) != 0 {
		t.Error("nothing folds to something")
	}
}

func TestUniqueFoldsThroughSynonymsWhenAsked(t *testing.T) {
	ts := []semantic.Triple{
		claim("Ada", "works at", "Acme", 0),
		claim("Ada", "employed by", "  ACME ", 0),
	}
	plain := Canon{}
	if got := len(Unique(ts, plain)); got != 2 {
		t.Errorf("without a table they are %d claims, want 2", got)
	}
	table := Canon{Synonyms: map[string]string{"employed by": "works at"}, Fold: true}
	if got := len(Unique(ts, table)); got != 1 {
		t.Errorf("with a table they are %d claims, want 1", got)
	}
}

func TestAlike(t *testing.T) {
	table := Canon{Synonyms: map[string]string{"employed by": "works at"}, Fold: true}
	for _, c := range []struct {
		name string
		a, b semantic.Triple
		c    Canon
		m    Metric
		want float64
		tol  float64
	}{
		{"the same claim", claim("Ada", "works at", "Acme", 0), claim("Ada", "works at", "Acme", 0), Canon{}, Jaro, 1, 0},
		{"different subjects", claim("Ada", "works at", "Acme", 0), claim("Bob", "works at", "Acme", 0), Canon{}, Jaro, 0, 0},
		{"subjects differing in case", claim("ada", "works at", "Acme", 0), claim("Ada", "works at", "Acme", 0), Canon{}, Jaro, 1, 0},
		{"a synonym", claim("Ada", "employed by", "Acme", 0), claim("Ada", "works at", "Acme", 0), table, Jaro, 1, 0},
		{"a separator", claim("Ada", "works_at", "Acme", 0), claim("Ada", "Works At", "Acme", 0), Canon{Fold: true}, Jaro, 1, 0},
		// The predicate agrees and the object does not: six tenths, and what
		// the two objects share on top.
		{"the same relation, another object", claim("Ada", "works at", "Acme", 0), claim("Ada", "works at", "Globex", 0), Canon{}, Exact, 0.6, 0},
		// The object agrees and the relation does not: four tenths.
		{"another relation, the same object", claim("Ada", "works at", "Acme", 0), claim("Ada", "founded", "Acme", 0), Canon{}, Exact, 0.4, 0},
		// Names the metric sees as close raise the score above the floor.
		{"a near object", claim("Ada", "works at", "Acme Corp", 0), claim("Ada", "works at", "Acme Corporation", 0), Canon{}, Jaro, 0.95, 0.05},
	} {
		got := Alike(c.a, c.b, c.c, c.m)
		if math.Abs(got-c.want) > c.tol {
			t.Errorf("%s: %.3f, want %.3f ±%g", c.name, got, c.want, c.tol)
		}
	}
}

func TestAlikeWeighsThePredicateAboveTheObject(t *testing.T) {
	base := claim("Ada", "works at", "Acme", 0)
	sameRelation := Alike(base, claim("Ada", "works at", "Globex", 0), Canon{}, Exact)
	sameObject := Alike(base, claim("Ada", "founded", "Acme", 0), Canon{}, Exact)
	if sameRelation <= sameObject {
		t.Errorf("agreeing on the relation scored %v, agreeing on the object %v: the relation should count for more",
			sameRelation, sameObject)
	}
	if sameRelation != 0.6 || sameObject != 0.4 {
		t.Errorf("weights are %v and %v, want 0.6 and 0.4", sameRelation, sameObject)
	}
}

func TestRepeats(t *testing.T) {
	ts := []semantic.Triple{
		claim("Ada", "works at", "Acme", 0),
		claim("Ada", "employed by", "Acme", 0),
		claim("Ada", "knows", "Charles", 0),
		claim("Bob", "works at", "Acme", 0),
	}
	table := Canon{Synonyms: map[string]string{"employed by": "works at"}, Fold: true}
	got, err := Repeats(context.Background(), ts, table, 0.9, Jaro)
	if err != nil {
		t.Fatal(err)
	}
	if want := [][2]int{{0, 1}}; !reflect.DeepEqual(got, want) {
		t.Errorf("repeats = %v, want %v", got, want)
	}
	loose, err := Repeats(context.Background(), ts, Canon{}, 0.4, Jaro)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range loose {
		if ts[p[0]].Subject != ts[p[1]].Subject {
			t.Errorf("pair %v crosses subjects", p)
		}
	}
}

func TestRepeatsRejectsAThresholdOutsideItsRange(t *testing.T) {
	for _, like := range []float64{-0.1, 1.5} {
		if _, err := Repeats(context.Background(), nil, Canon{}, like, Jaro); !errors.Is(err, ErrOption) {
			t.Errorf("threshold %v gave %v, want ErrOption", like, err)
		}
	}
}

func TestRepeatsStopsWhenTheContextIsDone(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	stop()
	ts := []semantic.Triple{claim("Ada", "works at", "Acme", 0), claim("Ada", "works at", "Acme", 0)}
	if _, err := Repeats(ctx, ts, Canon{}, 0.9, Jaro); !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}
