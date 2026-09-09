package main

import (
	"strings"
	"sync"
)

// Porter stemming, in the variant NLTK ships as its default. LoCoMo scores an
// answer by token overlap after stemming, so "allergies" and "allergy" have to
// count as one word or a correct answer loses points for its inflection. NLTK
// is what the official scorer calls, and NLTK's stemmer is not the published
// 1980 algorithm: it carries a table of irregular forms, a different y-to-i
// condition, and its own reading of -ies and -ied. Reproducing those here is
// what makes the Go numbers and the Python numbers the same numbers;
// testdata/stems.tsv holds NLTK's answer for every word in the corpus and the
// test asserts this agrees with all 6,597 of them.

// stem reduces a lowercase word to its stem. The answer is kept, because a
// benchmark stems the same few thousand words several million times.
func stem(w string) string {
	known.RLock()
	s, seen := known.m[w]
	known.RUnlock()
	if seen {
		return s
	}
	s = reduce(w)
	known.Lock()
	known.m[w] = s
	known.Unlock()
	return s
}

var known = struct {
	sync.RWMutex
	m map[string]string
}{m: map[string]string{}}

func reduce(w string) string {
	if s, odd := irregular[w]; odd {
		return s
	}
	if len(w) <= 2 {
		return w
	}
	w = step1a(w)
	w = step1b(w)
	w = step1c(w)
	w = step2(w)
	w = step3(w)
	w = step4(w)
	w = step5a(w)
	return step5b(w)
}

// rule removes a suffix and puts another in its place, when the condition
// holds of what is left. A nil condition always holds. The suffix "*d" stands
// for a doubled final consonant rather than for letters of its own.
type rule struct {
	suffix string
	with   string
	ok     func(stem string) bool
}

// doubled is the suffix that means "ends in a doubled consonant".
const doubled = "*d"

// apply runs the first rule that fires and stops, whether or not its condition
// held: a suffix that matched is the answer to this step even when its
// condition refuses the change.
func apply(w string, rules []rule) string {
	for _, r := range rules {
		var s string
		switch {
		case r.suffix == doubled:
			if !endsDouble(w) {
				continue
			}
			s = w[:len(w)-2]
		case strings.HasSuffix(w, r.suffix):
			s = w[:len(w)-len(r.suffix)]
		default:
			continue
		}
		if r.ok == nil || r.ok(s) {
			return s + r.with
		}
		return w
	}
	return w
}

// step1a takes the plural off.
func step1a(w string) string {
	if strings.HasSuffix(w, "ies") && len(w) == 4 {
		return w[:1] + "ie"
	}
	return apply(w, []rule{
		{"sses", "ss", nil},
		{"ies", "i", nil},
		{"ss", "ss", nil},
		{"s", "", nil},
	})
}

// step1b takes -eed, -ed and -ing off, and repairs the stem left behind.
func step1b(w string) string {
	if strings.HasSuffix(w, "ied") {
		if len(w) == 4 {
			return w[:1] + "ie"
		}
		return w[:len(w)-3] + "i"
	}
	if strings.HasSuffix(w, "eed") {
		if s := w[:len(w)-3]; measure(s) > 0 {
			return s + "ee"
		}
		return w
	}
	var mid string
	var cut bool
	for _, suffix := range [2]string{"ed", "ing"} {
		if !strings.HasSuffix(w, suffix) {
			continue
		}
		if s := w[:len(w)-len(suffix)]; hasVowel(s) {
			mid, cut = s, true
			break
		}
	}
	if !cut {
		return w
	}
	last := ""
	if mid != "" {
		last = mid[len(mid)-1:]
	}
	return apply(mid, []rule{
		{"at", "ate", nil},
		{"bl", "ble", nil},
		{"iz", "ize", nil},
		{doubled, last, func(string) bool { return last != "l" && last != "s" && last != "z" }},
		{"", "e", func(s string) bool { return measure(s) == 1 && endsCVC(s) }},
	})
}

// step1c turns a final y into i after a consonant.
func step1c(w string) string {
	return apply(w, []rule{
		{"y", "i", func(s string) bool { return len(s) > 1 && consonant(s, len(s)-1) }},
	})
}

// step2 folds the long derivational endings onto shorter ones.
func step2(w string) string {
	if strings.HasSuffix(w, "alli") && measure(w[:len(w)-4]) > 0 {
		return step2(w[:len(w)-4] + "al")
	}
	return apply(w, []rule{
		{"ational", "ate", grown},
		{"tional", "tion", grown},
		{"enci", "ence", grown},
		{"anci", "ance", grown},
		{"izer", "ize", grown},
		{"bli", "ble", grown},
		{"alli", "al", grown},
		{"entli", "ent", grown},
		{"eli", "e", grown},
		{"ousli", "ous", grown},
		{"ization", "ize", grown},
		{"ation", "ate", grown},
		{"ator", "ate", grown},
		{"alism", "al", grown},
		{"iveness", "ive", grown},
		{"fulness", "ful", grown},
		{"ousness", "ous", grown},
		{"aliti", "al", grown},
		{"iviti", "ive", grown},
		{"biliti", "ble", grown},
		{"fulli", "ful", grown},
		// NLTK keeps the l of -logi with the stem, so that a short stem such
		// as "geo" or "theo" still measures as grown.
		{"logi", "log", func(string) bool { return measure(w[:len(w)-3]) > 0 }},
	})
}

// step3 folds the remaining derivational endings away.
func step3(w string) string {
	return apply(w, []rule{
		{"icate", "ic", grown},
		{"ative", "", grown},
		{"alize", "al", grown},
		{"iciti", "ic", grown},
		{"ical", "ic", grown},
		{"ful", "", grown},
		{"ness", "", grown},
	})
}

// step4 strips the endings a twice-grown stem can lose outright.
func step4(w string) string {
	return apply(w, []rule{
		{"al", "", twice},
		{"ance", "", twice},
		{"ence", "", twice},
		{"er", "", twice},
		{"ic", "", twice},
		{"able", "", twice},
		{"ible", "", twice},
		{"ant", "", twice},
		{"ement", "", twice},
		{"ment", "", twice},
		{"ent", "", twice},
		{"ion", "", func(s string) bool {
			return measure(s) > 1 && s != "" && (s[len(s)-1] == 's' || s[len(s)-1] == 't')
		}},
		{"ou", "", twice},
		{"ism", "", twice},
		{"ate", "", twice},
		{"iti", "", twice},
		{"ous", "", twice},
		{"ive", "", twice},
		{"ize", "", twice},
	})
}

// step5a drops a final e. Both conditions are tried, unlike every other step,
// because the second is redundant otherwise.
func step5a(w string) string {
	if !strings.HasSuffix(w, "e") {
		return w
	}
	s := w[:len(w)-1]
	if m := measure(s); m > 1 || (m == 1 && !endsCVC(s)) {
		return s
	}
	return w
}

// step5b reduces a doubled l.
func step5b(w string) string {
	return apply(w, []rule{
		{"ll", "l", func(string) bool { return measure(w[:len(w)-1]) > 1 }},
	})
}

// grown reports that a stem has at least one vowel-consonant pair behind it.
func grown(s string) bool { return measure(s) > 0 }

// twice reports that a stem has at least two.
func twice(s string) bool { return measure(s) > 1 }

// consonant reports whether the letter at i is a consonant. A y is a
// consonant when what precedes it is not, which for a run of them alternates.
func consonant(w string, i int) bool {
	if vowel(w[i]) {
		return false
	}
	if w[i] != 'y' {
		return true
	}
	flip := false
	for i > 0 && w[i] == 'y' {
		flip = !flip
		i--
	}
	return !vowel(w[i]) != flip
}

func vowel(b byte) bool {
	return b == 'a' || b == 'e' || b == 'i' || b == 'o' || b == 'u'
}

// measure counts the vowel-consonant pairs in a stem, Porter's m.
func measure(s string) int {
	m, wasVowel := 0, false
	for i := range len(s) {
		c := consonant(s, i)
		if c && wasVowel {
			m++
		}
		wasVowel = !c
	}
	return m
}

func hasVowel(s string) bool {
	for i := range len(s) {
		if !consonant(s, i) {
			return true
		}
	}
	return false
}

func endsDouble(w string) bool {
	return len(w) >= 2 && w[len(w)-1] == w[len(w)-2] && consonant(w, len(w)-1)
}

// endsCVC reports Porter's *o: consonant, vowel, consonant, where the last is
// not w, x or y. NLTK admits a two-letter vowel-consonant word as well.
func endsCVC(w string) bool {
	n := len(w)
	if n >= 3 && consonant(w, n-3) && !consonant(w, n-2) && consonant(w, n-1) {
		if last := w[n-1]; last != 'w' && last != 'x' && last != 'y' {
			return true
		}
	}
	return n == 2 && !consonant(w, 0) && consonant(w, 1)
}

// irregular is the table NLTK carries of forms the rules get wrong: twenty
// years of corrections sent to Martin Porter.
var irregular = map[string]string{
	"sky": "sky", "skies": "sky",
	"dying": "die", "lying": "lie", "tying": "tie",
	"news":     "news",
	"innings":  "inning",
	"inning":   "inning",
	"outings":  "outing",
	"outing":   "outing",
	"cannings": "canning",
	"canning":  "canning",
	"howe":     "howe",
	"proceed":  "proceed",
	"exceed":   "exceed",
	"succeed":  "succeed",
}
