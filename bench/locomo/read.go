package main

import (
	"context"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The reader: evidence in, a short answer out. It is deliberately the dullest
// part of the benchmark and deliberately the same for every arm. It sees the
// question and the turns a memory returned, and it never sees the question's
// category or which memory produced the evidence — so when two columns of the
// results differ, they differ because one memory found better turns, not
// because it was read differently.
//
// Reply and Recite have no model behind them, which is what makes a run
// repeatable without a credential and what the deterministic columns of the
// report are. It is also a ceiling: handed the dataset's own annotated
// evidence, Reply reaches 0.202 on the answerable questions, so those columns
// measure the reader at least as much as they measure the memory. model.go
// puts a model behind the same interface to say how much.

// Reader writes the answer to a question from the evidence a memory returned.
// It is a value an arm carries rather than a branch inside the run, so a new
// way of reading is a new arm and not a new flag. The context is here for the
// one reader that leaves the machine; the two below ignore it.
type Reader func(ctx context.Context, question string, cites []Cite, sure float64, w *Words, o Options) string

// Reply writes the answer to a question from the evidence a memory returned.
// Below floor the memory is saying it does not have the answer, and the reply
// says so — which is the correct reply to an adversarial question and the
// wrong one to every other kind, so the floor is a real trade and is swept
// rather than assumed.
func Reply(ctx context.Context, question string, cites []Cite, sure float64, w *Words, o Options) string {
	if len(cites) == 0 || sure < o.Floor {
		return Decline
	}
	if asking(question) == "when" {
		return dated(cites[0], w, question)
	}

	focus := w.Focus(question, "")
	asked := unique(stems(question))
	var out []string
	for _, c := range cites {
		if c.Score < cites[0].Score*o.Band || len(out) == o.Parts {
			break
		}
		if s := span(c.Turn.Text, focus, asked, w, o.Span); s != "" && !contains(out, s) {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return Decline
	}
	return strings.Join(out, ", ")
}

// span is the answer inside a turn: the sentence that covers most of what the
// question asked, less the words the question already contained — those are
// what was given, not what is being asked — and of what is left, the words
// that carry the most information.
//
// Taking the rarest words rather than the first ones is the whole of it. An
// answer is the part of a sentence the rest of the conversation does not
// already say, which is what a low document frequency measures; the first few
// words of a sentence are just its beginning.
func span(text string, focus []string, asked map[string]bool, w *Words, width int) string {
	best, top := "", -1.0
	for _, s := range sentences(text) {
		if strings.HasSuffix(s, "?") {
			continue
		}
		if c := w.Cover(focus, Terms(s)); c > top {
			best, top = s, c
		}
	}

	kept := words(best)
	rank := make([]int, 0, len(kept))
	for i, word := range kept {
		if stop[word] || asked[stem(word)] {
			continue
		}
		rank = append(rank, i)
	}
	sort.SliceStable(rank, func(a, b int) bool {
		return w.Weight(stem(kept[rank[a]])) > w.Weight(stem(kept[rank[b]]))
	})
	if len(rank) > width {
		rank = rank[:width]
	}
	sort.Ints(rank)

	out := make([]string, 0, len(rank))
	for _, i := range rank {
		out = append(out, kept[i])
	}
	return strings.Join(out, " ")
}

// dated answers a question about when something happened. The session's own
// date is the anchor; a month named in the evidence overrides it, and a month
// later in the year than the session's is the year before, because that is
// what "back in October" means in May. This is the one place the reader reads
// the calendar, and both arms get it.
func dated(c Cite, w *Words, question string) string {
	when := c.Turn.Date
	focus := w.Focus(question, "")
	best, top := "", -1.0
	for _, s := range sentences(c.Turn.Text) {
		if cover := w.Cover(focus, Terms(s)); cover > top {
			best, top = s, cover
		}
	}
	said := words(best)
	for i, word := range said {
		month, ok := months[word]
		if !ok {
			continue
		}
		year := when.Year()
		if month > when.Month() {
			year--
		}
		day := number(said, i+1)
		if day == 0 {
			day = number(said, i-1)
		}
		if day < 1 || day > 31 {
			return month.String() + " " + strconv.Itoa(year)
		}
		return Day(time.Date(year, month, day, 0, 0, 0, 0, time.UTC))
	}
	if contains(said, "yesterday") {
		return Day(when.AddDate(0, 0, -1))
	}
	return Day(when)
}

// Recite answers from what the graph matched rather than from the sentence it
// was read out of: the objects of the best edges are what the graph holds. It
// is the same memory as the strict arm and a different way of reading it, so
// the difference between the two columns is worth exactly what an answer read
// off an edge is worth over an answer read off a turn.
func Recite(ctx context.Context, question string, cites []Cite, sure float64, w *Words, o Options) string {
	return recite(question, cites, sure, w, o, o.Parts, o.Band)
}

// Gather states everything the memory returned, one piece per turn, and is the
// reader for a question whose answer is a list.
//
// Reply and Recite keep an answer short with two settings of their own — at
// most parts pieces, and none below band of the best one's score — so the
// width of an answer is decided twice, once by the memory choosing what to
// return and again by the reader choosing what to use. Gather decides it once.
// The memory says what the evidence is and the reader says all of it, which
// puts the question of how wide an answer should run where it belongs: a
// memory that hands back five turns for a question with one answer has made
// the mistake, and the reader no longer hides it.
//
// The metric is not symmetric about this. A category 1 answer is marked part
// by part — each part of the reference takes the closest part of the reply,
// and a reply part that matches nothing costs nothing — while every other
// answerable category is marked as one bag of tokens, where a part that
// matches nothing costs precision. So the trade has a known sign in both
// directions and both are reported.
func Gather(ctx context.Context, question string, cites []Cite, sure float64, w *Words, o Options) string {
	return recite(question, cites, sure, w, o, len(cites), 0)
}

// recite writes the answer from what the memory matched inside each turn, up
// to parts of them and down to band of the best one's score.
//
// A memory that indexes turns has nothing matched to offer, so the sentence
// that covers most of the question stands in for it. That fallback is what
// lets the same reader run over every arm: the reader asks each memory what it
// matched and takes the turn's own words when the answer is nothing.
func recite(question string, cites []Cite, sure float64, w *Words, o Options, parts int, band float64) string {
	if len(cites) == 0 || sure < o.Floor {
		return Decline
	}
	if asking(question) == "when" {
		return dated(cites[0], w, question)
	}
	focus := w.Focus(question, "")
	asked := unique(stems(question))
	var out []string
	for _, c := range cites {
		if c.Score < cites[0].Score*band || len(out) == parts {
			break
		}
		say := c.Say
		if say == "" {
			say = span(c.Turn.Text, focus, asked, w, o.Span)
		}
		var kept []string
		for _, word := range words(say) {
			if stop[word] || asked[stem(word)] {
				continue
			}
			kept = append(kept, word)
		}
		s := strings.Join(kept, " ")
		if s == "" {
			// Everything the graph matched was a word the question already
			// held. What it matched is still the answer it has.
			s = say
		}
		if s != "" && !contains(out, s) {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return Decline
	}
	return strings.Join(out, ", ")
}

// asking is the kind of thing a question wants: its leading word, which is
// what says whether the answer is a date, a place, a person or a phrase.
func asking(question string) string {
	for _, w := range words(question) {
		switch w {
		case "when", "what", "where", "who", "why", "how", "which":
			return w
		}
	}
	return ""
}

// number is the word at i read as a day of the month, or zero when there is no
// word there or it is not a number.
func number(said []string, i int) int {
	if i < 0 || i >= len(said) {
		return 0
	}
	n, err := strconv.Atoi(said[i])
	if err != nil {
		return 0
	}
	return n
}

func contains(list []string, s string) bool {
	return slices.Contains(list, s)
}

var months = map[string]time.Month{
	"january": time.January, "jan": time.January,
	"february": time.February, "feb": time.February,
	"march": time.March, "mar": time.March,
	"april": time.April, "apr": time.April,
	"may":  time.May,
	"june": time.June, "jun": time.June,
	"july": time.July, "jul": time.July,
	"august": time.August, "aug": time.August,
	"september": time.September, "sep": time.September, "sept": time.September,
	"october": time.October, "oct": time.October,
	"november": time.November, "nov": time.November,
	"december": time.December, "dec": time.December,
}
