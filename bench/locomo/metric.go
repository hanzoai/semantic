package main

import (
	"strings"
	"unicode"
)

// The LoCoMo metric, as task_eval/evaluation.py defines it. Nothing here is a
// choice: an answer is normalised, stemmed, and compared as a bag of tokens,
// and each question category is compared its own way. The point of porting it
// rather than inventing one is that a number produced here means the same
// thing as a number in the paper. ref/ holds the Python it was ported from and
// score.py runs that Python over the same predictions, so the two can be
// diffed rather than trusted.

// grade scores one answer the way its category is scored.
//
//	1 open list      each part of the answer takes the closest part of the reply
//	2 temporal       token overlap
//	3 open domain    token overlap, against the first of several phrasings
//	4 single hop     token overlap
//	5 adversarial    a point for declining to answer, nothing for answering
func grade(kind int, reply, answer string) float64 {
	switch kind {
	case 1:
		return parts(reply, answer)
	case 3:
		return f1(reply, cut(answer, ";"))
	case 2, 4:
		return f1(reply, answer)
	case 5:
		if declined(reply) {
			return 1
		}
		return 0
	}
	return 0
}

// declined reports whether a reply says it has no answer. The wording is the
// scorer's, not ours: these are the two phrases it looks for.
func declined(reply string) bool {
	low := strings.ToLower(reply)
	return strings.Contains(low, "no information available") ||
		strings.Contains(low, "not mentioned")
}

// Decline is the reply that scores on an adversarial question, in the wording
// the scorer recognises.
const Decline = "No information available"

// f1 is the harmonic mean of precision and recall over stemmed tokens.
func f1(reply, answer string) float64 {
	got, want := stems(reply), stems(answer)
	if len(got) == 0 || len(want) == 0 {
		return 0
	}
	have := map[string]int{}
	for _, w := range got {
		have[w]++
	}
	same := 0
	for _, w := range want {
		if have[w] > 0 {
			have[w]--
			same++
		}
	}
	if same == 0 {
		return 0
	}
	p := float64(same) / float64(len(got))
	r := float64(same) / float64(len(want))
	return 2 * p * r / (p + r)
}

// parts scores an answer that is a list. Each part of the reference takes the
// best-matching part of the reply, and the score is the mean over the
// reference. Reply parts nothing matches cost nothing, which is worth knowing
// when reading a category 1 number: the metric rewards recall and does not
// punish a long list.
func parts(reply, answer string) float64 {
	got, want := strings.Split(reply, ","), strings.Split(answer, ",")
	total := 0.0
	for _, w := range want {
		best := 0.0
		for _, g := range got {
			if s := f1(strings.TrimSpace(g), strings.TrimSpace(w)); s > best {
				best = s
			}
		}
		total += best
	}
	return total / float64(len(want))
}

// stems is the comparable form of a text: normalised, split, and stemmed.
func stems(s string) []string {
	fields := strings.Fields(normalise(s))
	out := make([]string, len(fields))
	for i, f := range fields {
		out[i] = stem(f)
	}
	return out
}

// normalise strips everything the comparison must not depend on: commas,
// case, punctuation, the articles a, an, the and and, and the spacing.
func normalise(s string) string {
	s = strings.ReplaceAll(s, ",", "")
	s = strings.ToLower(s)

	var kept strings.Builder
	for _, r := range s {
		if r < unicode.MaxASCII && strings.ContainsRune(punctuation, r) {
			continue
		}
		kept.WriteRune(r)
	}

	// Articles go as whole words, so "a" in "5a" stays and "and" in "sand"
	// stays. Scanning word runs is the same rule the Python writes as \b.
	var out strings.Builder
	word := strings.Builder{}
	flush := func() {
		if w := word.String(); w != "" {
			if !articles[w] {
				out.WriteString(w)
			} else {
				out.WriteByte(' ')
			}
			word.Reset()
		}
	}
	for _, r := range kept.String() {
		if letter(r) {
			word.WriteRune(r)
			continue
		}
		flush()
		out.WriteRune(r)
	}
	flush()

	return strings.Join(strings.Fields(out.String()), " ")
}

// cut is the text before the first separator, which is how a category 3
// answer that offers several phrasings is reduced to the first.
func cut(s, sep string) string {
	if i := strings.Index(s, sep); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// letter reports whether a rune belongs to a word, matching what the Python
// regexp counts as \w.
func letter(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) || r == '_'
}

// punctuation is Python's string.punctuation, the set the scorer removes.
const punctuation = "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~"

var articles = map[string]bool{"a": true, "an": true, "the": true, "and": true}
