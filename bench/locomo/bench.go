package main

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
)

// The experiment. Every arm sees the same conversations, is asked the same
// questions in the same order, is allowed the same number of turns, and is
// marked by the same scorer. Nothing here knows how a memory works.

// Options is how a run is configured.
type Options struct {
	K     int     // turns a memory may return
	Floor float64 // coverage below which a memory declines to answer
	Span  int     // words the reader may take out of a sentence
	Band  float64 // how close to the best a further piece of evidence must be
	Parts int     // how many pieces of evidence the answer may draw on
}

// Arm is one memory read one way. Three of the four arms read their evidence
// the same way and differ only in the memory behind it; graph+edge shares the
// graph's memory and reads the answer off the matched edge instead of out of
// the turn the edge came from.
type Arm struct {
	Name   string
	Memory Memory
	Edge   bool
}

// Record is one question, answered.
type Record struct {
	Sample   string
	Question string
	Answer   string
	Kind     int
	Evidence []string
	Reply    string
	Context  []string
	Sure     float64
	Score    float64
	Recall   float64
}

// Tally is what an arm scored, kept by category so the shape of the result is
// visible and not just its mean.
type Tally struct {
	n       map[int]int
	score   map[int]float64
	recall  map[int]float64
	refused map[int]float64
}

// NewTally is an empty tally.
func NewTally() *Tally {
	return &Tally{
		n: map[int]int{}, score: map[int]float64{},
		recall: map[int]float64{}, refused: map[int]float64{},
	}
}

// Add records one answered question.
func (t *Tally) Add(r Record) {
	t.n[r.Kind]++
	t.score[r.Kind] += r.Score
	t.recall[r.Kind] += r.Recall
	if declined(r.Reply) {
		t.refused[r.Kind]++
	}
}

// Mean is the mean score over some categories, or over all of them when none
// is named.
func (t *Tally) Mean(kinds ...int) float64 { return t.over(t.score, kinds) }

// Reach is the mean share of a question's evidence the memory returned.
func (t *Tally) Reach(kinds ...int) float64 { return t.over(t.recall, kinds) }

// Quiet is the share of questions the memory declined to answer.
func (t *Tally) Quiet(kinds ...int) float64 { return t.over(t.refused, kinds) }

// Count is how many questions were asked in those categories.
func (t *Tally) Count(kinds ...int) int {
	if len(kinds) == 0 {
		n := 0
		for _, c := range t.n {
			n += c
		}
		return n
	}
	n := 0
	for _, k := range kinds {
		n += t.n[k]
	}
	return n
}

func (t *Tally) over(sums map[int]float64, kinds []int) float64 {
	n := t.Count(kinds...)
	if n == 0 {
		return 0
	}
	total := 0.0
	if len(kinds) == 0 {
		for _, v := range sums {
			total += v
		}
	} else {
		for _, k := range kinds {
			total += sums[k]
		}
	}
	return total / float64(n)
}

// Answerable are the categories with an answer in the conversation, as against
// category 5, where the right reply is that there is none. The two are scored
// in opposite directions, so a mean over all five hides the trade between
// them and they are reported apart wherever that trade is what is being shown.
var Answerable = []int{1, 2, 3, 4}

// Kinds are the question categories, by the numbers the dataset uses.
var Kinds = []struct {
	Kind int
	Name string
}{
	{4, "single hop"},
	{1, "multi hop"},
	{2, "temporal"},
	{3, "open domain"},
	{5, "adversarial"},
}

// Bench is every conversation with its memories already built. Building is the
// slow half and does not depend on how the reader is set, so a sweep over the
// reader's settings builds once and asks many times.
type Bench []Case

// Case is one conversation prepared for questioning: its vocabulary, and the
// memories built over it. The graph is kept beside the arms because what it
// read out of the turns — which people this conversation knows about — is what
// decides whether a question can be looked up by subject at all.
type Case struct {
	Sample Sample
	Words  *Words
	Know   *Knowledge
	Arms   []Arm
}

// Build reads every conversation into every memory. With factor set it builds
// the 2x2 of what is indexed against what is reachable instead of the four
// arms, which is what says whether the result belongs to the claim spans or to
// the subject scope.
func Build(ctx context.Context, samples []Sample, log io.Writer, factor bool) (Bench, error) {
	var out Bench
	var turns, claims, nodes, edges, facts int
	for _, s := range samples {
		words := Vocabulary(s.Turns)
		vector, err := NewVector(ctx, s.Turns, words)
		if err != nil {
			return nil, err
		}
		graph, err := NewKnowledge(ctx, s, s.Turns, words)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(log, "%-10s %4d turns  %5d terms  %6d claims  %5d nodes  %6d edges  %4d derived\n",
			s.ID, len(s.Turns), words.Size(), graph.Claims(), graph.Nodes(), graph.Edges(), graph.Facts())
		turns, claims = turns+len(s.Turns), claims+graph.Claims()
		nodes, edges, facts = nodes+graph.Nodes(), edges+graph.Edges(), facts+graph.Facts()
		arms := []Arm{
			{Name: "vector", Memory: vector},
			{Name: "graph", Memory: graph},
			{Name: "graph+edge", Memory: graph, Edge: true},
			{Name: "oracle", Memory: NewOracle(s, words)},
		}
		if factor {
			scoped, err := NewScoped(ctx, graph)
			if err != nil {
				return nil, err
			}
			arms = []Arm{
				{Name: "vector", Memory: vector},
				{Name: "scoped", Memory: scoped},
				{Name: "span", Memory: Flat{graph}},
				{Name: "graph", Memory: graph},
			}
		}
		out = append(out, Case{Sample: s, Words: words, Know: graph, Arms: arms})
	}
	fmt.Fprintf(log, "%-10s %4d turns  %5s terms  %6d claims  %5d nodes  %6d edges  %4d derived\n",
		"total", turns, "", claims, nodes, edges, facts)
	return out, nil
}

// Subjects counts the questions a memory can look a person up for: those
// naming one of the two speakers, and those naming anyone the conversation
// mentioned by name, which is the wider set the subject resolver matches. The
// rest reach every piece, which is the honest behaviour for a memory asked
// about a stranger.
func (b Bench) Subjects() (speaker, known, asked int) {
	for _, c := range b {
		for _, ask := range c.Sample.Asks {
			asked++
			low := strings.ToLower(ask.Text)
			if mentions(low, strings.ToLower(c.Sample.Who[0])) ||
				mentions(low, strings.ToLower(c.Sample.Who[1])) {
				speaker++
			}
			if c.Know.subject(ask.Text) != "" {
				known++
			}
		}
	}
	return speaker, known, asked
}

// answer is what one memory said about one question, kept so that two readers
// of the same memory ask it once.
type answer struct {
	cites []Cite
	sure  float64
}

// Run puts every question of every conversation to every arm and returns what
// each scored and what each answered.
func (b Bench) Run(ctx context.Context, o Options) (map[string]*Tally, map[string][]Record, error) {
	tallies := map[string]*Tally{}
	records := map[string][]Record{}
	for _, c := range b {
		for _, ask := range c.Sample.Asks {
			// Two arms share the graph and differ only in how it is read, so
			// the question is put to each memory once.
			asked := map[string]answer{}
			for _, arm := range c.Arms {
				got, done := asked[arm.Memory.Name()]
				if !done {
					cites, err := arm.Memory.Recall(ctx, ask.Text, o.K)
					if err != nil {
						return nil, nil, err
					}
					got = answer{cites, Sure(c.Words, ask.Text, cites)}
					asked[arm.Memory.Name()] = got
				}
				reply := Reply(ask.Text, got.cites, got.sure, c.Words, o)
				if arm.Edge {
					reply = Recite(ask.Text, got.cites, got.sure, c.Words, o)
				}
				r := Record{
					Sample: c.Sample.ID, Question: ask.Text, Answer: ask.Answer,
					Kind: ask.Kind, Evidence: ask.Evidence,
					Reply: reply, Context: ids(got.cites), Sure: got.sure,
					Score:  grade(ask.Kind, reply, ask.Answer),
					Recall: found(ask.Evidence, ids(got.cites)),
				}
				if tallies[arm.Name] == nil {
					tallies[arm.Name] = NewTally()
				}
				tallies[arm.Name].Add(r)
				records[arm.Name] = append(records[arm.Name], r)
			}
		}
	}
	return tallies, records, nil
}

// trim drops the brackets the dataset puts round some of its evidence ids.
func trim(id string) string { return strings.Trim(id, "()") }

// found is the share of a question's evidence turns that a memory returned. It
// is the scorer's own recall measure, and it is the one number in the report
// that owes nothing to the reader.
func found(evidence, context []string) float64 {
	if len(evidence) == 0 {
		return 1
	}
	hit := 0
	for _, e := range evidence {
		if contains(context, trim(e)) {
			hit++
		}
	}
	return float64(hit) / float64(len(evidence))
}

// Table writes the results, one row per category and one for the whole set.
func Table(w io.Writer, arms []string, tallies map[string]*Tally) {
	fmt.Fprintf(w, "%-14s", "")
	for _, a := range arms {
		fmt.Fprintf(w, "  %20s", a)
	}
	fmt.Fprintf(w, "\n%-14s%6s", "category", "n")
	for range arms {
		fmt.Fprintf(w, "  %6s %6s %6s", "F1", "recall", "quiet")
	}
	fmt.Fprintln(w)

	row := func(name string, kinds ...int) {
		fmt.Fprintf(w, "%-14s%6d", name, tallies[arms[0]].Count(kinds...))
		for _, a := range arms {
			t := tallies[a]
			fmt.Fprintf(w, "  %6.3f %6.3f %5.0f%%",
				t.Mean(kinds...), t.Reach(kinds...), 100*t.Quiet(kinds...))
		}
		fmt.Fprintln(w)
	}
	for _, k := range Kinds {
		row(k.Name, k.Kind)
	}
	row("answerable", Answerable...)
	row("overall")
}

// Order is the arm names in the order they are reported.
func Order(tallies map[string]*Tally) []string {
	out := make([]string, 0, len(tallies))
	for a := range tallies {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// Show prints a few answered questions per category, side by side, because a
// table of means says which arm won and nothing about why.
func Show(w io.Writer, arms []string, records map[string][]Record, n int) {
	for _, k := range Kinds {
		fmt.Fprintf(w, "\n== %s ==\n", k.Name)
		shown := 0
		for i := range records[arms[0]] {
			if records[arms[0]][i].Kind != k.Kind || shown == n {
				continue
			}
			shown++
			r := records[arms[0]][i]
			fmt.Fprintf(w, "\n  Q %s\n  A %s\n", r.Question, r.Answer)
			for _, a := range arms {
				got := records[a][i]
				fmt.Fprintf(w, "  %-11s %.2f  %s\n", a, got.Score, got.Reply)
			}
		}
	}
}
