package main

import (
	"context"
	"fmt"
	"testing"
)

// What one hop of the graph reaches, which is the fact the walk arm is built
// on and the fact that decides how far it is worth walking.
//
// A LoCoMo speaker is the subject of about a thousand assertions, so standing
// on the node a question names and taking the assertions out of it already
// reaches most of the turns the dataset calls that question's evidence. Reach
// is therefore not what limits the arm; choosing among what is reached is. And
// the adversarial questions are the other half of the same fact: their
// evidence is about somebody else, so one hop does not reach it and the memory
// correctly has nothing to say. A second hop reaches it, which is why the arm
// does not take one.
//
// Two conversations rather than ten, because the property is structural and
// the whole set takes a minute and a quarter to walk.
func TestOneHopReachesTheEvidence(t *testing.T) {
	ctx := context.Background()
	samples, err := Load("data/locomo10.json")
	if err != nil {
		t.Skip(err)
	}
	hit := map[int]map[int]float64{1: {}, 2: {}}
	want := map[int]float64{}
	for _, s := range samples[:2] {
		words := Vocabulary(s.Turns)
		k, err := NewKnowledge(ctx, s, s.Turns, words)
		if err != nil {
			t.Fatal(err)
		}
		for _, ask := range s.Asks {
			at := k.fold(ask.Text)
			if at == "" {
				continue
			}
			for _, hops := range []int{1, 2} {
				got := map[string]bool{}
				for _, step := range k.graph.Walk(at, hops) {
					for _, doc := range step.Docs {
						got[doc] = true
					}
				}
				for _, e := range ask.Evidence {
					if hops == 1 {
						want[ask.Kind]++
					}
					if got[trim(e)] {
						hit[hops][ask.Kind]++
					}
				}
			}
		}
	}
	reach := func(hops, kind int) float64 { return hit[hops][kind] / want[kind] }
	for _, kind := range Answerable {
		if got := reach(1, kind); got < 0.80 {
			t.Errorf("one hop reaches %.3f of the evidence for category %d, which is too little to be choosing from", got, kind)
		}
	}
	if got := reach(1, 5); got > 0.55 {
		t.Errorf("one hop reaches %.3f of the adversarial evidence; the whole point is that it does not", got)
	}
	if got := reach(2, 5); got < 0.85 {
		t.Errorf("two hops reach %.3f of the adversarial evidence, so the trade the arm refuses is not the trade it thinks", got)
	}
	for _, kind := range []int{4, 1, 2, 3, 5} {
		fmt.Printf("category %d: one hop reaches %.3f, two %.3f\n", kind, reach(1, kind), reach(2, kind))
	}
}
