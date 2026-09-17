package extract

import "strings"

// Gap relates entities that occur close together. It is what is left when no
// pattern fits: two things named a few words apart are usually about each
// other, even when the sentence joining them says how in a way no rule
// anticipated. On its own it claims only that much — the predicate is
// related_to — but a caller who names the predicates it is looking for gets
// the words between the two matched against them, and the best match names
// the relation.
//
// Handing it the linking verbs at a Min of 1 is how an attribute is read:
// Gap{Preds: []string{"is", "was", "has"}, Min: 1} over "Apple is a
// corporation" states what the sentence states, and states nothing where the
// sentence used a verb it was not given.
//
// The zero Gap works: entities within 100 bytes of each other, related_to.
type Gap struct {
	// Max is the most bytes that may separate two entities for them to count
	// as near. Zero uses 100, past which two mentions are rarely about each
	// other.
	Max int

	// Preds are the predicates to name a relation from. Empty says
	// related_to and nothing more.
	Preds []string

	// Min is the least a predicate must resemble the words between two
	// entities to be used. Zero uses 0.6. It has no effect without Preds,
	// since related_to is not a guess about which relation holds.
	Min float64
}

// Relations pairs every two entities near enough to each other in text.
// Endpoints are ordered as the text reads them, not as the caller listed
// them, so two runs over the same text agree.
func (g Gap) Relations(text string, known []Entity) []Relation {
	reach := g.Max
	if reach == 0 {
		reach = 100
	}
	least := g.Min
	if least == 0 {
		least = 0.6
	}

	var out []Relation
	for i, a := range known {
		for _, b := range known[i+1:] {
			// Two mentions of one name are not a relation between two
			// things. What links them is coreference, which Coref reads.
			if strings.EqualFold(a.Text, b.Text) {
				continue
			}
			lo, hi := a, b
			if hi.Start < lo.Start {
				lo, hi = hi, lo
			}
			apart := max(hi.Start-lo.End, 0)
			if apart > reach {
				continue
			}

			pred, score := "related_to", 0.6
			meta := map[string]any{"by": "gap", "apart": apart}
			if len(g.Preds) > 0 {
				between := strings.TrimSpace(clip(text, lo.End, hi.Start))
				if between == "" {
					continue
				}
				p, s := fit(between, g.Preds)
				if s < least {
					continue
				}
				pred, score = p, s
				meta["between"] = between
			}

			out = append(out, Relation{
				Subject:   lo,
				Predicate: pred,
				Object:    hi,
				Score:     score,
				Context:   clip(text, lo.Start-30, hi.End+30),
				Meta:      meta,
			})
		}
	}
	return out
}

// fit picks the predicate that the words between two entities most resemble.
// A predicate the text names outright, as a whole word, fits exactly — "is"
// inside "this" names nothing. Otherwise it is how much of the two strings
// lies in matching blocks, which is a resemblance and not a statement, so a
// caller who wants only what the text actually said sets Min to 1. Ties go to
// the first predicate listed.
func fit(between string, preds []string) (string, float64) {
	low := strings.ToLower(between)
	best, score := "", 0.0
	for _, p := range preds {
		want := strings.ToLower(p)
		if want == "" {
			continue
		}
		s := Ratio(want, low)
		if names(low, want) {
			s = 1
		}
		if s > score {
			best, score = p, s
		}
	}
	return best, score
}

// names reports whether s says want as a whole word or phrase.
func names(s, want string) bool {
	for i := 0; i+len(want) <= len(s); {
		j := strings.Index(s[i:], want)
		if j < 0 {
			return false
		}
		j += i
		if edge(s, j-1) && edge(s, j+len(want)) {
			return true
		}
		i = j + 1
	}
	return false
}

// edge reports whether index i of s falls outside a word.
func edge(s string, i int) bool {
	if i < 0 || i >= len(s) {
		return true
	}
	c := s[i]
	return !(c == '_' || '0' <= c && c <= '9' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z')
}

// clip is text[from:to], with offsets an extractor may have got wrong made
// harmless.
func clip(text string, from, to int) string {
	if from < 0 {
		from = 0
	}
	if to > len(text) {
		to = len(text)
	}
	if from >= to {
		return ""
	}
	return text[from:to]
}
