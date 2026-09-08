package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

// Every type here says it is safe for concurrent use, and an agent's context
// is shared by whatever the agent is running at once. Under -race this is the
// proof; without it, it is still a check that nothing is lost.
func TestManyAtOnce(t *testing.T) {
	ctx := context.Background()
	k := New()
	if _, err := k.Rules.Add(lending()); err != nil {
		t.Fatal(err)
	}

	const writers = 8
	const each = 25
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				who := fmt.Sprintf("%d-%d", w, i)
				if _, err := k.Learn(ctx, Note{Text: "application from " + who, Of: []string{"applicant " + who}}); err != nil {
					t.Error(err)
					return
				}
				d := applicant(700, 0.3)
				d.ID = "d" + who
				d.Because = []string{"applicant " + who}
				if _, err := k.Decide(ctx, d); err != nil {
					t.Error(err)
					return
				}
				k.Recall(ctx, "application "+who, 5)
				k.Cause.Up(d.ID, 3)
				k.Graph.Near("applicant "+who, Reach{Hops: 2})
				k.Journal.Like(Ask{Case: "credit application", N: 3})
			}
		}(w)
	}
	wg.Wait()

	if got := k.Memory.Count(); got != writers*each {
		t.Errorf("%d notes kept, want %d", got, writers*each)
	}
	if got := len(k.Journal.All()); got != writers*each {
		t.Errorf("%d decisions written, want %d", got, writers*each)
	}
	if got := len(k.Graph.Nodes(kindDecision)); got != writers*each {
		t.Errorf("%d decisions in the graph, want %d", got, writers*each)
	}
}
