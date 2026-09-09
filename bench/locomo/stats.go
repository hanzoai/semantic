package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// Three things a table of means cannot show, all read back out of the
// predictions file rather than recomputed from the memories: what a system
// that answers nothing scores, how far an arm's confidence tells an answerable
// question from an adversarial one, and how much of the difference between two
// arms survives resampling the conversations.
//
//	go run ./bench/locomo -out bench/locomo/out/answers.json
//	go run ./bench/locomo -stats bench/locomo/out/answers.json

// Answered is one question as the predictions file keeps it: what was asked,
// what the dataset holds, and what each arm replied and how sure it was.
type Answered struct {
	Sample string
	Kind   int
	Answer string
	Reply  map[string]string
	Sure   map[string]float64
}

// Answers reads a predictions file back, with the arms it holds.
func Answers(path string) ([]Answered, []string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var file []struct {
		ID string           `json:"sample_id"`
		QA []map[string]any `json:"qa"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(file) == 0 || len(file[0].QA) == 0 {
		return nil, nil, fmt.Errorf("%s: no questions", path)
	}

	var arms []string
	for key := range file[0].QA[0] {
		if name, cut := strings.CutSuffix(key, "_prediction"); cut {
			arms = append(arms, name)
		}
	}
	sort.Strings(arms)

	var out []Answered
	for _, conv := range file {
		for _, qa := range conv.QA {
			kind, _ := qa["category"].(float64)
			answer, _ := qa["answer"].(string)
			a := Answered{
				Sample: conv.ID, Kind: int(kind), Answer: answer,
				Reply: map[string]string{}, Sure: map[string]float64{},
			}
			for _, arm := range arms {
				a.Reply[arm], _ = qa[arm+"_prediction"].(string)
				a.Sure[arm], _ = qa[arm+"_prediction_confidence"].(float64)
			}
			out = append(out, a)
		}
	}
	return out, arms, nil
}

// Stats writes the three reports.
func Stats(w io.Writer, path string) error {
	answered, arms, err := Answers(path)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "%s: %d questions, %s\n", path, len(answered), strings.Join(arms, ", "))
	silent(w, answered)
	apart(w, answered, arms)
	return paired(w, answered, arms)
}

// silent scores the system that replies that it has no answer, to every
// question, and does nothing else. A quarter of the questions are adversarial
// and the metric awards a point for declining them, so this is the number any
// mean over all five categories has to beat before it means anything.
func silent(w io.Writer, answered []Answered) {
	n, score := map[int]int{}, map[int]float64{}
	for _, a := range answered {
		n[a.Kind]++
		score[a.Kind] += grade(a.Kind, Decline, a.Answer)
	}
	sum := func(kinds []int) (int, float64) {
		count, total := 0, 0.0
		for _, k := range kinds {
			count, total = count+n[k], total+score[k]
		}
		return count, total
	}
	all := append([]int{5}, Answerable...)

	fmt.Fprintf(w, "\nconstant abstention, %q to everything\n", Decline)
	fmt.Fprintf(w, "%-14s%6s  %6s\n", "category", "n", "F1")
	row := func(name string, kinds ...int) {
		count, total := sum(kinds)
		fmt.Fprintf(w, "%-14s%6d  %6.3f\n", name, count, total/float64(count))
	}
	for _, k := range Kinds {
		row(k.Name, k.Kind)
	}
	row("answerable", Answerable...)
	row("overall", all...)
}

// apart reports how far each arm's confidence separates an answerable question
// from an adversarial one, as the area under the ROC curve — the probability
// that a randomly chosen answerable question is given a higher confidence than
// a randomly chosen adversarial one. It is a property of the memory and does
// not depend on where the abstention floor is put. The rest of the row is the
// decision the floor actually produced, which does: the same measure over the
// realised choice, and how often each half was declined.
func apart(w io.Writer, answered []Answered, arms []string) {
	fmt.Fprintf(w, "\nanswerable against adversarial, area under the ROC curve\n")
	fmt.Fprintf(w, "%-14s%8s%9s%11s%11s\n", "arm", "sure", "decided", "quiet 1-4", "quiet adv")
	for _, arm := range arms {
		var sure, spoke [2][]float64
		for _, a := range answered {
			at := 0
			if a.Kind == 5 {
				at = 1
			}
			sure[at] = append(sure[at], a.Sure[arm])
			answer := 1.0
			if declined(a.Reply[arm]) {
				answer = 0
			}
			spoke[at] = append(spoke[at], answer)
		}
		fmt.Fprintf(w, "%-14s%8.3f%9.3f%10.1f%%%10.1f%%\n", arm,
			auc(sure[0], sure[1]), auc(spoke[0], spoke[1]),
			100*quiet(spoke[0]), 100*quiet(spoke[1]))
	}
}

// quiet is the share of a half the arm declined to answer.
func quiet(spoke []float64) float64 {
	silent := 0.0
	for _, answered := range spoke {
		silent += 1 - answered
	}
	return silent / float64(len(spoke))
}

// auc is the Mann-Whitney statistic with mid-ranked ties, which is the area
// under the ROC curve: the share of answerable-adversarial pairs the first
// score orders correctly, a tie counting a half.
func auc(pos, neg []float64) float64 {
	all := make([]float64, 0, len(pos)+len(neg))
	all = append(append(all, pos...), neg...)
	sort.Float64s(all)

	total := 0.0
	for _, v := range pos {
		lo := sort.SearchFloat64s(all, v)
		hi := sort.Search(len(all), func(i int) bool { return all[i] > v })
		total += float64(lo+hi+1) / 2 // the mid-rank of a run of equal scores
	}
	n, m := float64(len(pos)), float64(len(neg))
	return (total - n*(n+1)/2) / (n * m)
}

// paired reports the difference between every pair of arms on each half of the
// benchmark, with a 95% percentile interval. Questions within a conversation
// are answered out of one memory and are not independent, so the resampling
// unit is the conversation.
func paired(w io.Writer, answered []Answered, arms []string) error {
	convs := []string{}
	at := map[string]int{}
	for _, a := range answered {
		if _, seen := at[a.Sample]; !seen {
			at[a.Sample] = len(convs)
			convs = append(convs, a.Sample)
		}
	}

	// Per conversation: how many questions of each half it holds, and the sum
	// of the per-question differences over them. The statistic is the pooled
	// mean difference, so a resample carries both.
	half := [][]int{Answerable, {5}}
	names := []string{"answerable", "adversarial"}
	size := make([][]float64, 2)
	for h := range size {
		size[h] = make([]float64, len(convs))
		for _, a := range answered {
			for _, k := range half[h] {
				if a.Kind == k {
					size[h][at[a.Sample]]++
				}
			}
		}
	}

	fmt.Fprintf(w, "\npaired difference, 95%% percentile bootstrap over %d conversations\n", len(convs))
	fmt.Fprintf(w, "%-24s", "")
	for h := 1; h >= 0; h-- {
		fmt.Fprintf(w, "  %26s", names[h])
	}
	fmt.Fprintln(w)

	for i, one := range arms {
		for _, two := range arms[i+1:] {
			fmt.Fprintf(w, "%-10s - %-11s", one, two)
			for h := 1; h >= 0; h-- {
				diff := make([]float64, len(convs))
				for _, a := range answered {
					for _, k := range half[h] {
						if a.Kind != k {
							continue
						}
						diff[at[a.Sample]] += grade(a.Kind, a.Reply[one], a.Answer) -
							grade(a.Kind, a.Reply[two], a.Answer)
					}
				}
				got, lo, hi := interval(diff, size[h], 0.025)
				fmt.Fprintf(w, "  %+6.3f [%+6.3f, %+6.3f]", got, lo, hi)
			}
			fmt.Fprintln(w)
		}
	}
	return nil
}

// interval is the paired difference and its percentile interval. The
// resampling distribution is enumerated rather than sampled: there are 92,378
// ways to draw ten conversations from ten with replacement, and each is
// weighted by how often that draw comes up, so the interval is exact and does
// not depend on a seed or on how many resamples someone had patience for.
func interval(diff, size []float64, p float64) (got, lo, hi float64) {
	type point struct{ at, weight float64 }
	dist := []point{}
	total := 0.0
	draws(len(diff), func(count []int, weight float64) {
		sum, n := 0.0, 0.0
		for i, c := range count {
			sum += float64(c) * diff[i]
			n += float64(c) * size[i]
		}
		if n == 0 {
			return
		}
		dist = append(dist, point{sum / n, weight})
		total += weight
	})
	sort.Slice(dist, func(i, j int) bool { return dist[i].at < dist[j].at })

	quantile := func(q float64) float64 {
		seen := 0.0
		for _, d := range dist {
			if seen += d.weight; seen >= q*total {
				return d.at
			}
		}
		return dist[len(dist)-1].at
	}

	sum, n := 0.0, 0.0
	for i := range diff {
		sum, n = sum+diff[i], n+size[i]
	}
	return sum / n, quantile(p), quantile(1 - p)
}

// draws calls f for every way of drawing n conversations from n with
// replacement, with the multinomial weight of that draw: how many of the n^n
// ordered samples produce it.
func draws(n int, f func(count []int, weight float64)) {
	count := make([]int, n)
	var walk func(i, left int, ways float64)
	walk = func(i, left int, ways float64) {
		if i == n-1 {
			count[i] = left
			f(count, ways/factorial(left))
			return
		}
		for c := 0; c <= left; c++ {
			count[i] = c
			walk(i+1, left-c, ways/factorial(c))
		}
	}
	walk(0, n, factorial(n))
}

func factorial(n int) float64 {
	out := 1.0
	for i := 2; i <= n; i++ {
		out *= float64(i)
	}
	return out
}
