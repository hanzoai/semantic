package reason_test

import (
	"context"
	"fmt"

	"github.com/hanzoai/semantic"
	"github.com/hanzoai/semantic/reason"
)

// Reasoning over a small graph: a schema lifts the stated predicate to the
// one the rule is written in terms of, the rule closes over the result, and
// the model answers for what it derived.
func Example() {
	facts := []semantic.Triple{
		{Subject: "ada", Predicate: "mother", Object: "bob"},
		{Subject: "bob", Predicate: "mother", Object: "cy"},
	}

	closure, err := reason.Engine{
		Rules: []reason.Rule{reason.Transitive("parent")},
		Prop:  reason.Tree{"mother": "parent"},
	}.Run(context.Background(), facts)
	if err != nil {
		fmt.Println(err)
		return
	}

	for _, f := range closure.Derived() {
		fmt.Println(f)
	}

	why, err := closure.Why(reason.Key(semantic.Triple{Subject: "ada", Predicate: "parent", Object: "cy"}))
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("because:")
	for _, step := range why {
		if step.Rule == "" {
			fmt.Println("  " + step.String())
			continue
		}
		fmt.Println("  " + step.String() + " by " + step.Rule)
	}

	// Output:
	// parent(ada, bob)
	// parent(bob, cy)
	// parent(ada, cy)
	// because:
	//   mother(ada, bob)
	//   parent(ada, bob) by subproperty
	//   mother(bob, cy)
	//   parent(bob, cy) by subproperty
	//   parent(ada, cy) by transitive parent
}

// Rules can be read from text, which is what makes them configuration
// rather than code.
func ExampleParse() {
	r, err := reason.Parse("grandparent(?x, ?y) :- parent(?x, ?z), parent(?z, ?y).")
	if err != nil {
		fmt.Println(err)
		return
	}
	m, err := reason.Engine{Rules: []reason.Rule{r}}.Run(context.Background(), []semantic.Triple{
		{Subject: "tom", Predicate: "parent", Object: "bob"},
		{Subject: "bob", Predicate: "parent", Object: "ann"},
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, b := range m.Match(reason.Pattern{Subject: "tom", Predicate: "grandparent", Object: "?who"}) {
		fmt.Println(b["who"])
	}

	// Output:
	// ann
}
