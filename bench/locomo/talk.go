package main

import (
	"strings"

	"github.com/hanzoai/semantic/extract"
)

// Reading conversation. The library's rule extractor knows how to find a
// company being founded or a person being hired; a friend saying "I went to a
// support group yesterday" is a different grammar, and this is its reader.
//
// One idea does most of the work: in a two-party conversation the subject of a
// sentence is almost always determined by who is speaking. "I" is the speaker,
// "you" is the other one, a name is that person, and a sentence that names
// nobody is about the speaker. Bind the subject that way and every phrase in
// the sentence becomes something known about a particular person rather than a
// loose bag of words — which is the whole difference between a graph and an
// index.
//
// Two rules keep false claims out, and both are grammar rather than tuning:
//
//   - A question asserts nothing. "How's it going with yoga?" does not make
//     the asker a person who does yoga. Dropping interrogatives is what stops
//     a topic leaking from the person who raised it to the person who
//     answered.
//   - A negated sentence asserts its negation. "I don't eat dairy" is kept,
//     with the negation on the predicate, so it can be found and is not
//     mistaken for "I eat dairy".

// Claim is one link a sentence asserted, and the turn that asserted it. The
// link carries the date as its From, so a claim knows when it was made without
// anything else having to look it up.
type Claim struct {
	extract.Link
	Turn int
	// Said is the sentence the claim was read from. A claim is a reduction of
	// it, and a reduction loses words; keeping the sentence is what lets the
	// graph point at what was actually said rather than at what it managed to
	// parse.
	Said string
}

// Read turns one utterance into what it claims. speaker is who is talking and
// other is who they are talking to; those two names are what the first and
// second person resolve to. names is every other person the conversation has
// mentioned, so that a third party named in a sentence binds to themselves.
func Read(t Turn, at int, speaker, other string, names map[string]bool) []Claim {
	var out []Claim
	for _, s := range sentences(t.Text) {
		if strings.HasSuffix(strings.TrimSpace(s), "?") {
			continue
		}
		ws := words(s)
		if len(ws) == 0 {
			continue
		}
		subject, from := who(ws, speaker, other, names)
		verb, rest := predicate(ws[from:])
		for _, phrase := range phrases(rest) {
			out = append(out, Claim{
				Subject:   subject,
				Predicate: verb,
				Object:    phrase,
				Score:     told,
				From:      Day(t.Date),
				Cite:      t.ID,
				Turn:      at,
				Said:      s,
			})
		}
		for _, n := range naming(ws) {
			n.From, n.Cite = Day(t.Date), t.ID
			out = append(out, Claim{Link: n, Turn: at, Said: s})
		}
	}
	return out
}

// told is the confidence in a claim somebody stated about themselves, which is
// every claim this extractor makes.
const told = 0.9

// who resolves the subject of a sentence and reports where in the sentence it
// was found, so the verb search can start after it. A sentence that names
// nobody is about whoever is speaking.
//
// Binding the third person as well — running extract.Coref over each session
// so that "she" reaches the name an earlier turn gave it — was tried and is a
// wash: it reaches 3.1% of sentences, moves the temporal questions by +0.009
// and the adversarial ones by -0.006, and moves the multi-hop questions it was
// meant for by nothing at all. A name folds to one node whichever session it
// was said in, so the aggregation those questions need was already there.
func who(words []string, speaker, other string, names map[string]bool) (string, int) {
	for i, w := range words {
		switch {
		case first[w]:
			return speaker, i + 1
		case second[w]:
			return other, i + 1
		case strings.EqualFold(w, speaker):
			return speaker, i + 1
		case strings.EqualFold(w, other):
			return other, i + 1
		case names[strings.ToLower(w)]:
			return strings.ToLower(w), i + 1
		}
	}
	return speaker, 0
}

// predicate finds the verb the sentence turns on and returns it with what
// follows. An auxiliary yields to a content verb close behind it, so "have
// been going to the gym" is going rather than have. A negation moves onto the
// verb, because "not eating dairy" and "eating dairy" are different claims and
// a graph that stores them as one is wrong rather than imprecise.
func predicate(words []string) (string, []string) {
	no := false
	for i, w := range words {
		if negation[w] {
			no = true
		}
		if !verbal(w) {
			continue
		}
		verb := w
		rest := words[i+1:]
		if auxiliary[w] {
			// An auxiliary yields only to a verb right behind it: "have been
			// going" is going, but "have a snake named Susie" is have — the
			// noun in the way ends the verb phrase, and named there modifies
			// the snake rather than saying what anyone did.
			for j := i + 1; j < len(words); j++ {
				if negation[words[j]] {
					no = true
					continue
				}
				if !verbal(words[j]) {
					break
				}
				if !auxiliary[words[j]] {
					verb, rest = words[j], words[j+1:]
					break
				}
			}
		}
		if no {
			verb = "not " + verb
		}
		return verb, rest
	}
	return "about", words
}

// verbal reports whether a word can head a verb phrase: an auxiliary, one of
// the verbs conversation runs on, or a word long enough to carry an
// inflection.
func verbal(w string) bool {
	if verbs[w] || auxiliary[w] {
		return true
	}
	return len(w) > 4 && (strings.HasSuffix(w, "ing") || strings.HasSuffix(w, "ed"))
}

// phrases are the runs of content words left in a sentence. Each run is one
// thing the sentence was about; the stop words between them are the seams.
func phrases(words []string) []string {
	var out []string
	var run []string
	flush := func() {
		if len(run) > 0 {
			out = append(out, strings.Join(run, " "))
			run = nil
		}
	}
	for _, w := range words {
		if stop[w] || len(w) == 0 {
			flush()
			continue
		}
		run = append(run, w)
		if len(run) == 6 {
			flush()
		}
	}
	flush()
	return out
}

// naming reads "a snake named Susie" and "my dog called Rex": the one pattern
// where a thing in the sentence, rather than a person, is the subject. It is
// what lets a name reach the person who owns the thing, one hop further out
// than the sentence itself says.
func naming(words []string) []extract.Link {
	var out []extract.Link
	for i, w := range words {
		if w != "named" && w != "called" {
			continue
		}
		thing := back(words[:i])
		name := forward(words[i+1:])
		if thing == "" || name == "" {
			continue
		}
		out = append(out, extract.Link{
			Subject: thing, Predicate: "named", Object: name, Score: told,
		})
	}
	return out
}

// back is the content run ending at the last word given, reached over any
// stop words in the way: in "the dog is called Rex" the thing named is the
// dog, not the "is" that stands between them.
func back(words []string) string {
	i := len(words) - 1
	for i >= 0 && stop[words[i]] {
		i--
	}
	var run []string
	for ; i >= 0 && len(run) < 3; i-- {
		if stop[words[i]] {
			break
		}
		run = append([]string{words[i]}, run...)
	}
	return strings.Join(run, " ")
}

// forward is the content run beginning at the first word given.
func forward(words []string) string {
	var run []string
	for _, w := range words {
		if stop[w] || len(run) == 3 {
			break
		}
		run = append(run, w)
	}
	return strings.Join(run, " ")
}

// sentences cuts a turn where a sentence ends. The mark is kept on the piece
// so that a question can still be told apart from a statement.
func sentences(text string) []string {
	var out []string
	start := 0
	for i, r := range text {
		switch r {
		case '.', '!', '?', '\n':
			if s := strings.TrimSpace(text[start : i+1]); s != "" {
				out = append(out, s)
			}
			start = i + 1
		}
	}
	if s := strings.TrimSpace(text[start:]); s != "" {
		out = append(out, s)
	}
	return out
}

// words cuts a text into the words the scorer counts. It is the scorer's own
// tokenisation, reused: normalise lowercases, drops punctuation without
// splitting on it, and drops the articles. Sharing it is what keeps
// "self-care" one word all the way from the turn it was said in to the answer
// it is marked against — tokenising it any other way spells the answer
// "self care" and scores nothing against a reference that spells it
// "selfcare".
func words(s string) []string {
	var out []string
	for w := range strings.FieldsSeq(normalise(s)) {
		if spelled(w) {
			out = append(out, w)
		}
	}
	return out
}

// spelled reports whether a field is a word at all. Normalising removes the
// punctuation the scorer knows about, which is the ASCII of it; a dash that
// came in as an em dash survives as a field of its own and has no business in
// an answer.
func spelled(w string) bool {
	for _, r := range w {
		if letter(r) {
			return true
		}
	}
	return false
}

var (
	// first and second person, in every spelling an apostrophe survives being
	// dropped: i'm becomes im, you've becomes youve. "we're" is left out,
	// because dropping its apostrophe spells the auxiliary "were" and the
	// auxiliary is much the commoner word.
	first = set("i im ive id ill me my mine myself we weve our ours us")

	second = set("you youre youve youd youll your yours yourself")

	negation = set("not nt no never none nothing dont didnt doesnt cant cannot " +
		"wont wouldnt isnt arent wasnt werent havent hasnt hadnt couldnt shouldnt")

	auxiliary = set("is am are was were be been being have has had do does did " +
		"will would can could should may might must got get")

	verbs = set("went go goes going gone come came comes coming get got getting " +
		"gotten make made makes making take took takes taking see saw sees " +
		"seeing know knew knows think thought thinks want wanted wants like " +
		"liked likes love loved loves need needed needs try tried tries feel " +
		"felt feels find found finds give gave gives tell told tells say said " +
		"says work worked works play played plays start started starts keep " +
		"kept keeps let lets put puts mean meant read write wrote run ran " +
		"move moved live lived lives learn learnt learned teach taught buy " +
		"bought sell sold eat ate eats drink drank meet met hear heard hearing " +
		"help helped helps spend spent visit visited join joined build built " +
		"send sent bring brought leave left stay stayed adopt adopted plan " +
		"planned finish finished win won lose lost hope hoped wish " +
		"wished miss missed remember remembered enjoy enjoyed share shared " +
		"volunteer volunteered")

	// stop words mark the seams between the content runs of a sentence.
	stop = set("a an the and or but so if then than that this these those there " +
		"here it its of to in on at by for with about from into over under " +
		"up down out off again once very just too also really quite such more " +
		"most much many some any all both each few other another as is am are was " +
		"were be been being have has had do does did will would can could should " +
		"may might must not no nor only own same s t don dont didnt doesnt cant " +
		"wont im ive id ill me my mine myself we our ours us you your yours " +
		"yourself he him his she her hers they them their theirs i what which who " +
		"whom when where why how while during before after because since until " +
		"yeah yes ok okay oh wow hey hi hello thanks thank well um uh like lol " +
		"haha sure right now still even ever never always " +
		// named and called join a thing to its name rather than adding to
		// either, so they end a run instead of joining it: "a snake named
		// Susie" is a claim about a snake and a claim about Susie.
		"named called")
)

// set builds a lookup from a space-separated list, which is how every word
// list in this file is written.
func set(words string) map[string]bool {
	out := map[string]bool{}
	for w := range strings.FieldsSeq(words) {
		out[w] = true
	}
	return out
}

// Named harvests the people a conversation mentions besides its two speakers.
// A word counts as a name when it is capitalised somewhere other than the
// start of a sentence and is never written in lower case anywhere in the
// conversation — the ordinary test for a proper noun, and one that reads only
// the conversation, never the questions.
func Named(turns []Turn) map[string]bool {
	upper, lower := map[string]int{}, map[string]bool{}
	for _, t := range turns {
		for _, s := range sentences(t.Text) {
			for i, w := range strings.Fields(s) {
				bare := strings.TrimFunc(w, func(r rune) bool { return !letter(r) })
				if len(bare) < 3 {
					continue
				}
				low := strings.ToLower(bare)
				switch {
				case bare == low:
					lower[low] = true
				case i > 0:
					upper[low]++
				}
			}
		}
	}
	out := map[string]bool{}
	for w, n := range upper {
		if n >= 2 && !lower[w] && !stop[w] {
			out[w] = true
		}
	}
	return out
}
