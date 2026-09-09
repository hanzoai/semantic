// Command locomo runs the LoCoMo long-conversation recall benchmark over two
// memories built from the same package: turns in a vector store, and a graph
// of who said what about whom. Both are asked the dataset's own questions and
// marked by the dataset's own metric.
//
//	go run ./bench/locomo                    the table
//	go run ./bench/locomo -show 5            answers, side by side
//	go run ./bench/locomo -sweep floor       how the answer moves with a setting
//	go run ./bench/locomo -factor            index unit against subject scope
//	go run ./bench/locomo -out bench/locomo/out/answers.json
//
// Run it from the repository root; the default paths are relative to there.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Task is one invocation: what to read, what to vary, and what to write.
type Task struct {
	Options
	Data   string
	Out    string
	Sweep  string
	Show   int
	Only   int
	Quiet  bool
	Factor bool
}

func main() {
	var t Task
	flag.StringVar(&t.Data, "data", "bench/locomo/data/locomo10.json", "the dataset, fetched on first use")
	flag.IntVar(&t.K, "k", 5, "turns a memory may return for one question")
	flag.Float64Var(&t.Floor, "floor", 0.3, "coverage below which a memory declines to answer")
	flag.IntVar(&t.Span, "span", 4, "words the reader may take out of a sentence")
	flag.Float64Var(&t.Band, "band", 0.9, "how close to the best a further piece of evidence must be")
	flag.IntVar(&t.Parts, "parts", 2, "pieces of evidence one answer may draw on")
	flag.StringVar(&t.Out, "out", "", "write predictions here, in the shape the official scorer reads")
	flag.StringVar(&t.Sweep, "sweep", "", "k, floor, span, band or parts: run the whole benchmark once per value")
	flag.IntVar(&t.Show, "show", 0, "print this many answered questions per category")
	flag.IntVar(&t.Only, "only", 0, "use only the first N conversations, for a quick look")
	flag.BoolVar(&t.Quiet, "quiet", false, "leave out the per-conversation build lines")
	flag.BoolVar(&t.Factor, "factor", false, "report the 2x2 of what is indexed against what is reachable")
	flag.Parse()

	if err := t.Run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "locomo:", err)
		os.Exit(1)
	}
}

// Run reads the dataset, builds both memories over every conversation, and
// either reports one setting or sweeps one.
func (t Task) Run(ctx context.Context) error {
	samples, err := Load(t.Data)
	if err != nil {
		return err
	}
	if t.Only > 0 && t.Only < len(samples) {
		samples = samples[:t.Only]
	}

	turns, asks := 0, 0
	for _, s := range samples {
		turns += len(s.Turns)
		asks += len(s.Asks)
	}
	fmt.Printf("%d conversations, %d turns, %d questions\n\n", len(samples), turns, asks)

	var log io.Writer = os.Stdout
	if t.Quiet || t.Sweep != "" {
		log = io.Discard
	}
	bench, err := Build(ctx, samples, log, t.Factor)
	if err != nil {
		return err
	}
	if t.Sweep != "" {
		return t.spread(ctx, bench)
	}

	tallies, records, err := bench.Run(ctx, t.Options)
	if err != nil {
		return err
	}
	fmt.Printf("\nk=%d floor=%.2f span=%d band=%.2f parts=%d\n\n",
		t.K, t.Floor, t.Span, t.Band, t.Parts)
	Table(os.Stdout, Order(tallies), tallies)
	if t.Show > 0 {
		Show(os.Stdout, Order(tallies), records, t.Show)
	}
	if t.Out == "" {
		return nil
	}
	return write(t.Out, samples, records)
}

// steps are the values each setting is swept over.
var steps = map[string][]float64{
	"k":     {1, 2, 5, 10, 20, 50},
	"floor": {0, 0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.8},
	"span":  {2, 3, 4, 5, 6, 8, 12},
	"band":  {0.5, 0.7, 0.8, 0.9, 0.95, 1},
	"parts": {1, 2, 3, 4},
}

// spread runs the benchmark once per value of one setting, reporting the
// answerable categories and the adversarial one apart, since the two are
// scored in opposite directions and a mean over both hides the trade. A
// benchmark with a constant in it should show what the constant was worth.
func (t Task) spread(ctx context.Context, bench Bench) error {
	values, known := steps[t.Sweep]
	if !known {
		return fmt.Errorf("sweep over %q: try k, floor, span, band or parts", t.Sweep)
	}

	var arms []string
	for _, v := range values {
		switch t.Sweep {
		case "k":
			t.K = int(v)
		case "floor":
			t.Floor = v
		case "span":
			t.Span = int(v)
		case "band":
			t.Band = v
		case "parts":
			t.Parts = int(v)
		}
		tallies, _, err := bench.Run(ctx, t.Options)
		if err != nil {
			return err
		}
		if arms == nil {
			arms = Order(tallies)
			fmt.Printf("%8s", "")
			for _, a := range arms {
				fmt.Printf("  %26s", a)
			}
			fmt.Printf("\n%8s", t.Sweep)
			for range arms {
				fmt.Printf("  %6s %6s %6s %5s", "1-4", "adv", "all", "quiet")
			}
			fmt.Println()
		}
		fmt.Printf("%8g", v)
		for _, a := range arms {
			m := tallies[a]
			fmt.Printf("  %6.3f %6.3f %6.3f %4.0f%%",
				m.Mean(Answerable...), m.Mean(5), m.Mean(), 100*m.Quiet())
		}
		fmt.Println()
	}
	return nil
}

// write saves the predictions in the shape task_eval/evaluate_qa.py writes and
// task_eval/evaluation.py reads, so the official Python can be run over the
// same answers and the two scorers compared rather than trusted. The
// confidence each arm had in its evidence goes out alongside, since whether
// that number separates an answerable question from an adversarial one is a
// property of the memory and cannot be recovered from the reply.
func write(path string, samples []Sample, records map[string][]Record) error {
	type conversation struct {
		ID string           `json:"sample_id"`
		QA []map[string]any `json:"qa"`
	}
	at := map[string]int{}
	file := make([]conversation, 0, len(samples))
	for _, s := range samples {
		at[s.ID] = len(file)
		qa := make([]map[string]any, 0, len(s.Asks))
		for _, a := range s.Asks {
			qa = append(qa, map[string]any{
				"question": a.Text, "answer": a.Answer,
				"category": a.Kind, "evidence": a.Evidence,
			})
		}
		file = append(file, conversation{ID: s.ID, QA: qa})
	}
	arms := make([]string, 0, len(records))
	for arm, rs := range records {
		arms = append(arms, arm)
		seen := map[string]int{}
		for _, r := range rs {
			qa := file[at[r.Sample]].QA[seen[r.Sample]]
			seen[r.Sample]++
			qa[arm+"_prediction"] = r.Reply
			qa[arm+"_prediction_context"] = r.Context
			qa[arm+"_prediction_confidence"] = r.Sure
		}
	}
	sort.Strings(arms)

	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", " ")
	if err := enc.Encode(file); err != nil {
		return err
	}
	fmt.Printf("\npredictions: %s (%s)\n", path, strings.Join(arms, ", "))
	return nil
}
