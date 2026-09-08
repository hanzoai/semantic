package extract

import (
	"regexp"
	"strings"
)

// Near finds the candidate most like text and how alike it is, in [0,1].
// It tries the cheap tests first and stops as soon as one is decisive: an
// exact match, then a known synonym, then containment scaled by how much of
// the longer string the shorter one covers, then the longest-matching-blocks
// ratio. It returns -1 when there are no candidates.
//
// The Python original inserts sentence-embedding and word-vector stages
// between containment and the ratio. Those need a model; the stages that do
// not are here, and an embedding-backed matcher belongs behind Model rather
// than in this function.
func Near(text string, candidates []string) (int, float64) {
	if text == "" || len(candidates) == 0 {
		return -1, 0
	}
	want := strings.ToLower(strings.TrimSpace(text))
	if want == "" {
		return -1, 0
	}

	low := make([]string, len(candidates))
	for i, c := range candidates {
		low[i] = strings.ToLower(strings.TrimSpace(c))
	}

	for i, c := range low {
		if c == want {
			return i, 1
		}
	}

	best, score := -1, 0.0
	if syns, ok := synonyms[want]; ok {
		for _, s := range syns {
			for i, c := range low {
				if c == s {
					return i, 0.95
				}
			}
		}
	}
	for i, c := range low {
		for _, s := range synonyms[c] {
			if s == want && score < 0.95 {
				best, score = i, 0.95
			}
		}
	}

	word, err := regexp.Compile(`\b` + regexp.QuoteMeta(want) + `\b`)
	if err != nil {
		word = nil
	}
	for i, c := range low {
		if c == "" {
			continue
		}
		var s float64
		if strings.Contains(c, want) || strings.Contains(want, c) {
			short, long := len(want), len(c)
			if short > long {
				short, long = long, short
			}
			s = 0.9*(float64(short)/float64(long)) + 0.1
			if word != nil && word.MatchString(c) && s < 0.88 {
				s = 0.88
			}
		}
		if s > score {
			best, score = i, s
		}
	}
	if score >= 0.85 {
		return best, score
	}

	for i, c := range low {
		if c == "" {
			continue
		}
		if s := Ratio(want, c); s > score {
			best, score = i, s
		}
	}
	return best, score
}

// Similar is how alike text and the closest candidate are, in [0,1], for a
// caller that wants the score without the candidate.
func Similar(text string, candidates []string) float64 {
	_, score := Near(text, candidates)
	return score
}

// Bind returns the entity that best matches text, or false when none reaches
// min. An exact, case-insensitive hit always wins.
func Bind(text string, known []Entity, min float64) (Entity, bool) {
	if text == "" || len(known) == 0 {
		return Entity{}, false
	}
	want := strings.ToLower(strings.TrimSpace(text))
	for _, e := range known {
		if strings.ToLower(strings.TrimSpace(e.Text)) == want {
			return e, true
		}
	}
	names := make([]string, len(known))
	for i, e := range known {
		names[i] = e.Text
	}
	if i, s := Near(text, names); i >= 0 && s >= min {
		return known[i], true
	}
	return Entity{}, false
}

// Weigh blends an extractor's own confidence with how close the item is to the
// types a caller asked for, half from each. It returns score unchanged when
// the caller named no types, since there is then nothing to compare against.
func Weigh(label string, score float64, want []string, text string) float64 {
	if len(want) == 0 {
		return score
	}
	_, byLabel := Near(label, want)
	byText := 0.0
	if text != "" {
		_, byText = Near(text, want)
	}
	if byText > byLabel {
		byLabel = byText
	}
	return clamp(0.5*score + 0.5*byLabel)
}

// Ratio is the share of two strings that lies in matching blocks: twice the
// matched length over the summed length, the similarity Python's difflib
// reports. Identical strings give 1, strings with nothing in common give 0.
func Ratio(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	total := len(ra) + len(rb)
	if total == 0 {
		return 1
	}
	return 2 * float64(blocks(ra, rb)) / float64(total)
}

// blocks counts the characters covered by the longest common run and, either
// side of it, by the longest common run of what remains.
func blocks(a, b []rune) int {
	i, j, n := longest(a, b)
	if n == 0 {
		return 0
	}
	return n + blocks(a[:i], b[:j]) + blocks(a[i+n:], b[j+n:])
}

// longest returns the start in a, the start in b, and the length of the
// longest run the two share, preferring the earliest such run in a and then in
// b so the result does not depend on scan order.
func longest(a, b []rune) (int, int, int) {
	if len(a) == 0 || len(b) == 0 {
		return 0, 0, 0
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	bi, bj, best := 0, 0, 0
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			if a[i-1] == b[j-1] {
				cur[j] = prev[j-1] + 1
				if cur[j] > best {
					best, bi, bj = cur[j], i-cur[j], j-cur[j]
				}
			} else {
				cur[j] = 0
			}
		}
		prev, cur = cur, prev
		for j := range cur {
			cur[j] = 0
		}
	}
	return bi, bj, best
}

func clamp(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

// synonyms are the type and predicate names that mean the same thing, so a
// label an extractor produced can be recognised as one a caller asked for.
var synonyms = map[string][]string{
	"person":       {"people", "human", "name", "individual", "artist", "actor", "author", "politician"},
	"org":          {"company", "organization", "business", "institution", "agency", "brand", "corporation"},
	"organization": {"company", "business", "institution", "agency", "brand", "corporation"},
	"gpe":          {"location", "place", "city", "country", "state", "nation", "region"},
	"loc":          {"location", "place", "region", "area"},
	"date":         {"time", "year", "day", "month", "period", "duration"},
	"money":        {"cost", "price", "value", "currency", "amount"},
	"product":      {"item", "object", "commodity", "goods", "device", "tool", "vehicle", "software", "app"},
	"event":        {"incident", "occasion", "activity", "happening", "ceremony"},
	"drug":         {"medication", "medicine", "pharmaceutical", "chemical", "treatment", "therapy"},
	"chemical":     {"drug", "substance", "compound", "element"},
	"disease":      {"condition", "illness", "sickness", "disorder", "syndrome", "ailment"},

	"founded_by":      {"founder", "creator", "established_by", "started_by", "originator"},
	"acquired":        {"bought", "purchased", "acquisition", "takeover", "ownership", "merged_with"},
	"subsidiary_of":   {"owned_by", "parent_company", "part_of", "division_of", "unit_of"},
	"works_for":       {"employee_of", "employed_by", "staff_of", "team_member", "employs", "hired_by"},
	"located_in":      {"based_in", "headquartered_in", "situated_in", "found_in", "operates_in"},
	"ceo_of":          {"leader_of", "head_of", "director_of", "president_of", "chief_executive", "managed_by"},
	"invested_in":     {"funded", "financed", "backed", "shareholder_of", "venture_capital"},
	"partner_with":    {"collaborate_with", "joint_venture", "alliance", "deal_with", "partnership"},
	"competitor_of":   {"rival", "competes_with", "opponent", "nemesis"},
	"manufacturer_of": {"producer_of", "maker_of", "creator_of", "builder_of"},
	"treats":          {"cures", "heals", "remedy_for", "used_for", "prescribed_for"},
	"causes":          {"leads_to", "results_in", "triggers", "produces", "creates"},
	"diagnosed_with":  {"suffers_from", "has_condition", "patient_of", "victim_of"},
}
