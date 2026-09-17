package extract

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/hanzoai/semantic"
)

// Rules extracts without a model. Entities come from a gazetteer of known
// names and from regexes over the shape of the text; relations come from
// patterns that read a verb phrase between two entities already found. Every
// answer is a function of the input, so it is reproducible and testable
// offline, and it is the sensible floor under an LLM extractor rather than a
// replacement for it.
//
// The zero Rules works: it uses the built-in patterns, no gazetteer, and keeps
// everything it finds.
type Rules struct {
	// Patterns maps an entity label to the regexp that finds it. A nil map
	// uses the built-in set. The first capturing group, when there is one, is
	// the entity text.
	Patterns map[string]*regexp.Regexp

	// Names is a gazetteer: a surface form to the label it carries. Matching
	// is case-insensitive and whole-word. A gazetteer hit outranks a pattern
	// hit over the same span, since the caller stated it and the pattern only
	// guessed.
	Names map[string]string

	// Min drops anything scoring below it. Zero keeps everything.
	Min float64
}

// score of each source of an entity, from most to least certain.
const (
	scoreName    = 0.9  // named in the gazetteer
	scorePattern = 0.75 // matched a pattern for its label
	scoreLast    = 0.5  // a capitalised word and nothing better
	scoreRel     = 0.7  // a relation pattern joined two entities
)

// Entities finds every entity in text. Overlapping finds are resolved in
// favour of the more certain and then the longer, so no two returned entities
// cover the same character. When nothing matches at all it falls back to
// capitalised words labelled UNKNOWN, which is worth more to a downstream
// stage than an empty answer.
func (r Rules) Entities(text string) []Entity {
	var found []Entity

	for surface, label := range r.Names {
		pat, err := regexp.Compile(`(?i)\b` + regexp.QuoteMeta(surface) + `\b`)
		if err != nil {
			continue
		}
		for _, at := range pat.FindAllStringIndex(text, -1) {
			found = append(found, Entity{
				Text: text[at[0]:at[1]], Label: label,
				Start: at[0], End: at[1], Score: scoreName,
				Meta: map[string]any{"by": "name"},
			})
		}
	}

	pats := r.Patterns
	if pats == nil {
		pats = shapes
	}
	labels := make([]string, 0, len(pats))
	for label := range pats {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	for _, label := range labels {
		found = append(found, hits(text, label, pats[label])...)
	}

	found = sift(found)
	if len(found) == 0 {
		for _, at := range capitalised.FindAllStringIndex(text, -1) {
			found = append(found, Entity{
				Text: text[at[0]:at[1]], Label: "UNKNOWN",
				Start: at[0], End: at[1], Score: scoreLast,
				Meta: map[string]any{"by": "shape"},
			})
		}
	}

	kept := found[:0]
	for _, e := range found {
		if e.Score >= r.Min {
			kept = append(kept, e)
		}
	}
	return kept
}

// hits runs one label's pattern and, for PERSON, trims the corporate suffix a
// name pattern will otherwise swallow. Go's regexp has no negative lookahead,
// which is how the Python pattern keeps "Inc" out of "Apple Inc"; trimming
// after the match gets to the same place.
func hits(text, label string, pat *regexp.Regexp) []Entity {
	if pat == nil {
		return nil
	}
	var out []Entity
	for _, at := range pat.FindAllStringSubmatchIndex(text, -1) {
		start, end := at[0], at[1]
		if len(at) > 3 && at[2] >= 0 {
			start, end = at[2], at[3]
		}
		span := text[start:end]
		if label == "PERSON" {
			span, end = shed(span, start)
			if len(strings.Fields(span)) < 2 {
				continue
			}
		}
		out = append(out, Entity{
			Text: span, Label: label,
			Start: start, End: end, Score: scorePattern,
			Meta: map[string]any{"by": "shape"},
		})
	}
	return out
}

// shed drops trailing company words from a name-shaped match and returns what
// is left with its new end offset.
func shed(span string, start int) (string, int) {
	words := strings.Fields(span)
	for len(words) > 0 && corporate[words[len(words)-1]] {
		words = words[:len(words)-1]
	}
	out := strings.Join(words, " ")
	return out, start + len(out)
}

// sift keeps the strongest find over each stretch of text: higher score
// first, then longer span, then earlier, so the result does not depend on the
// order patterns ran in.
func sift(found []Entity) []Entity {
	sort.SliceStable(found, func(i, j int) bool {
		a, b := found[i], found[j]
		switch {
		case a.Score != b.Score:
			return a.Score > b.Score
		case a.End-a.Start != b.End-b.Start:
			return a.End-a.Start > b.End-b.Start
		case a.Start != b.Start:
			return a.Start < b.Start
		default:
			return a.Label < b.Label
		}
	})
	var kept []Entity
	for _, e := range found {
		clash := false
		for _, k := range kept {
			if e.Start < k.End && k.Start < e.End {
				clash = true
				break
			}
		}
		if !clash {
			kept = append(kept, e)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].Start < kept[j].Start })
	return kept
}

// Relations reads the relation patterns over text, joining only entities the
// caller already found. Requiring both endpoints to be known entities is what
// keeps a pattern from matching a long run of prose that merely ends in the
// right verb.
func (r Rules) Relations(text string, known []Entity) []Relation {
	if len(known) == 0 || r.Min > scoreRel {
		return nil
	}
	alt := alternation(known)
	if alt == "" {
		return nil
	}
	byText := map[string]Entity{}
	for _, e := range known {
		byText[strings.ToLower(e.Text)] = e
	}

	var out []Relation
	for _, name := range verbOrder {
		for _, form := range verbs[name] {
			pat, err := regexp.Compile("(?i)" + strings.ReplaceAll(form, "%s", alt))
			if err != nil {
				continue
			}
			subj := pat.SubexpIndex("subject")
			obj := pat.SubexpIndex("object")
			if subj < 0 || obj < 0 {
				continue
			}
			for _, at := range pat.FindAllStringSubmatchIndex(text, -1) {
				s, ok := byText[strings.ToLower(strings.TrimSpace(text[at[2*subj]:at[2*subj+1]]))]
				if !ok {
					continue
				}
				o, ok := byText[strings.ToLower(strings.TrimSpace(text[at[2*obj]:at[2*obj+1]]))]
				if !ok {
					continue
				}
				out = append(out, Relation{
					Subject: s, Predicate: name, Object: o, Score: scoreRel,
					Context: window(text, at[0], at[1], 50),
					Meta:    map[string]any{"by": "pattern"},
				})
			}
		}
	}
	return out
}

// Find returns everything Rules can say about a text.
func (r Rules) Find(text string) Set {
	es := r.Entities(text)
	return Set{Entities: es, Relations: r.Relations(text, es)}
}

// Extract makes Rules a pipeline stage.
func (r Rules) Extract(_ context.Context, c semantic.Chunk) ([]semantic.Triple, error) {
	return r.Find(c.Text).Triples(c), nil
}

// alternation builds the regexp branch that matches any known entity, longest
// first so "Apple Inc." wins over "Apple".
func alternation(known []Entity) string {
	names := make([]string, 0, len(known))
	for _, e := range known {
		if e.Text != "" {
			names = append(names, e.Text)
		}
	}
	sort.SliceStable(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })
	seen := map[string]bool{}
	var alts []string
	for _, n := range names {
		q := regexp.QuoteMeta(n)
		if !seen[q] {
			seen[q] = true
			alts = append(alts, q)
		}
	}
	if len(alts) == 0 {
		return ""
	}
	return "(?:" + strings.Join(alts, "|") + ")"
}

// window is the text around a match, for evidence.
func window(text string, start, end, pad int) string {
	from := max(start-pad, 0)
	to := min(end+pad, len(text))
	return text[from:to]
}

var (
	// shapes are the built-in entity patterns. PERSON has no negative
	// lookahead here — see hits.
	//
	// The space inside a name is horizontal, never \s: a name does not cross
	// a line break, and reading one that does turns a heading and the
	// sentence under it into a single entity that is neither. The Python this
	// is ported from uses \s and does exactly that.
	shapes = map[string]*regexp.Regexp{
		"PERSON":  regexp.MustCompile(`\b([A-Z][a-z]+(?:[ \t]+[A-Z][a-z]+)+)\b`),
		"ORG":     regexp.MustCompile(`\b([A-Z][a-zA-Z]+(?:[ \t]+[A-Z][a-zA-Z]+)*[ \t]+(?:Inc|Corp|LLC|Ltd|Company|Corporation))\b`),
		"GPE":     regexp.MustCompile(`\b([A-Z][a-z]+[ \t]*(?:City|State|Country|Nation))\b`),
		"DATE":    regexp.MustCompile(`\b(\d{1,2}[/-]\d{1,2}[/-]\d{2,4}|\d{4})\b`),
		"MONEY":   regexp.MustCompile(`(\$[\d,]+(?:\.\d{2})?)`),
		"PERCENT": regexp.MustCompile(`\b(\d+(?:\.\d+)?%)`),
	}

	capitalised = regexp.MustCompile(`\b[A-Z][a-z]{2,}\b`)

	corporate = map[string]bool{
		"Inc": true, "Inc.": true, "Corp": true, "Corp.": true,
		"LLC": true, "Ltd": true, "Ltd.": true, "Company": true, "Corporation": true,
	}

	// verbOrder fixes the order relations are reported in, so two runs over
	// the same text agree.
	verbOrder = []string{"founded_by", "located_in", "works_for", "born_in", "acquired_by"}

	// verbs are the surface forms of each relation. %s stands for "any known
	// entity"; both endpoints must be one.
	verbs = map[string][]string{
		"founded_by": {
			`(?P<subject>%s)(?:[.,])?\s+(?:was\s+)?founded\s+by\s+(?P<object>%s)`,
			`(?P<object>%s)(?:[.,])?\s+founded\s+(?P<subject>%s)`,
			`(?P<subject>%s)(?:[.,])?\s+(?:was\s+)?established\s+by\s+(?P<object>%s)`,
			`(?P<object>%s)(?:[.,])?\s+established\s+(?P<subject>%s)`,
			`(?P<subject>%s)(?:[.,])?\s+(?:was\s+)?created\s+by\s+(?P<object>%s)`,
			`(?P<object>%s)(?:[.,])?\s+created\s+(?P<subject>%s)`,
			`(?P<subject>%s)(?:[.,])?\s+(?:was\s+)?started\s+by\s+(?P<object>%s)`,
			`(?P<object>%s)(?:[.,])?\s+started\s+(?P<subject>%s)`,
			`(?P<subject>%s)(?:[.,])?\s+(?:was\s+)?co-founded\s+by\s+(?P<object>%s)`,
			`(?P<object>%s)(?:[.,])?\s+co-founded\s+(?P<subject>%s)`,
			`(?P<object>%s)(?:[.,])?\s+is\s+(?:the\s+)?founder\s+of\s+(?P<subject>%s)`,
		},
		"located_in": {
			`(?P<subject>%s)\s+is\s+located\s+in\s+(?P<object>%s)`,
			`(?P<subject>%s)\s+(?:is\s+)?headquartered\s+in\s+(?P<object>%s)`,
			`(?P<subject>%s)\s+(?:is\s+)?based\s+in\s+(?P<object>%s)`,
			`(?P<subject>%s)\s+has\s+(?:its\s+)?headquarters\s+in\s+(?P<object>%s)`,
			`(?P<subject>%s)\s+operates\s+(?:out\s+of|from)\s+(?P<object>%s)`,
			`(?P<subject>%s)\s+has\s+offices\s+in\s+(?P<object>%s)`,
			`(?P<subject>%s)\s+in\s+(?P<object>%s)`,
		},
		"works_for": {
			`(?P<subject>%s)\s+works?\s+for\s+(?P<object>%s)`,
			`(?P<subject>%s)\s+works?\s+at\s+(?P<object>%s)`,
			`(?P<subject>%s)\s+is\s+an?\s+employee\s+of\s+(?P<object>%s)`,
			`(?P<subject>%s)\s+is\s+(?:the\s+)?(?:CEO|CFO|CTO|COO|director|manager|president|founder)\s+of\s+(?P<object>%s)`,
			`(?P<subject>%s)\s+joined\s+(?P<object>%s)`,
			`(?P<subject>%s)\s+was\s+hired\s+by\s+(?P<object>%s)`,
			`(?P<subject>%s)\s+serves\s+at\s+(?P<object>%s)`,
		},
		"born_in": {
			`(?P<subject>%s)\s+was\s+born\s+in\s+(?P<object>%s)`,
			`(?P<subject>%s)\s+born\s+in\s+(?P<object>%s)`,
			`(?P<subject>%s)\s+is\s+a\s+native\s+of\s+(?P<object>%s)`,
			`(?P<subject>%s)\s+hails\s+from\s+(?P<object>%s)`,
			`(?P<subject>%s)\s+is\s+originally\s+from\s+(?P<object>%s)`,
		},
		"acquired_by": {
			`(?P<subject>%s)\s+(?:was\s+)?acquired\s+by\s+(?P<object>%s)`,
			`(?P<object>%s)\s+acquired\s+(?P<subject>%s)`,
			`(?P<subject>%s)\s+(?:was\s+)?bought\s+by\s+(?P<object>%s)`,
			`(?P<object>%s)\s+bought\s+(?P<subject>%s)`,
			`(?P<subject>%s)\s+is\s+a\s+subsidiary\s+of\s+(?P<object>%s)`,
		},
	}
)
