package dedupe

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/hanzoai/semantic"
)

// Kind says what sort of disagreement a Conflict is.
type Kind string

const (
	// Value is two documents giving one property different values.
	Value Kind = "value"
	// Type is two documents calling one thing different kinds.
	Type Kind = "type"
	// Edge is one subject given more than one object for a relation that
	// admits only one.
	Edge Kind = "edge"
	// Time is dates that cannot both hold.
	Time Kind = "time"
	// Logic is kinds that exclude one another: a person is not a company.
	Logic Kind = "logic"
	// Range is a value outside the bounds declared for its property.
	Range Kind = "range"
)

// Level says how much a disagreement matters.
type Level string

const (
	// Low is a disagreement of spelling: the values name one thing.
	Low Level = "low"
	// Medium is a plain disagreement about a value.
	Medium Level = "medium"
	// High is a disagreement wide enough to change what the value means.
	High Level = "high"
	// Critical is a disagreement about what the thing is, or one that cannot
	// be true at all.
	Critical Level = "critical"
)

// Claim is one value as one document stated it. Values and their sources are
// held together because every way of settling a disagreement needs both: what
// was said and who said it.
type Claim struct {
	Value any
	From  Source
}

// Conflict is one disagreement, whole. It carries every value asserted, the
// document behind each, how sure the disagreement is real and how much it
// matters. It is a value the caller reads, ranks and settles — never an
// error, because refusing to return an assertion is how a graph loses what a
// document said.
type Conflict struct {
	ID      string
	Kind    Kind
	Subject string // the thing, or the subject of the relation, disagreed about
	Prop    string
	Values  []Claim
	Score   float64 // confidence the disagreement is real
	Level   Level
	Advice  string
}

// Limit is the range a property's values may take.
type Limit struct{ Lo, Hi float64 }

// Disjoint names the kinds that cannot both describe one thing, each kind
// against the kinds it excludes. Keys and values are compared folded.
type Disjoint map[string][]string

// Apart is the default exclusion table: a thing is not both a person and an
// organisation, and not both a place and either.
var Apart = Disjoint{
	"person":       {"organization", "organisation", "company", "institution", "location", "place"},
	"organization": {"person", "location", "place"},
	"organisation": {"person", "location", "place"},
	"company":      {"person", "location", "place"},
	"institution":  {"person"},
	"location":     {"person", "organization", "organisation", "company"},
	"place":        {"person", "organization", "organisation", "company"},
}

// Dates are the properties read as points in time when Times is given none.
var Dates = []string{
	"founded", "founded_year", "established", "created",
	"timestamp", "date", "start", "end", "start_date", "end_date",
}

// year finds a four-digit year in text, which is how a date written as prose
// is compared with one written as a number.
var year = regexp.MustCompile(`\b(?:19|20)\d{2}\b`)

// Values reports the entities whose documents give prop different values.
// One document stating a value is not a disagreement, so an entity described
// only once is never reported.
func Values(es []Entity, prop string) []Conflict {
	by := pack(es)
	var out []Conflict
	for _, id := range order(es) {
		g := by[id]
		if len(g) < 2 {
			continue
		}
		cs := state(g, prop)
		if distinct(cs, nil) < 2 {
			continue
		}
		out = append(out, found(Value, id, prop, cs))
	}
	return out
}

// Types reports the entities their documents call different kinds. Any two
// kinds disagree here; whether they can both be true is what Excludes asks.
func Types(es []Entity) []Conflict {
	var out []Conflict
	by := pack(es)
	for _, id := range order(es) {
		g := by[id]
		if len(g) < 2 {
			continue
		}
		var cs []Claim
		for _, e := range g {
			if e.Kind != "" {
				cs = append(cs, Claim{Value: e.Kind, From: e.From})
			}
		}
		if distinct(cs, nil) < 2 {
			continue
		}
		out = append(out, found(Type, id, "kind", cs))
	}
	return out
}

// Times reports the entities whose documents date them differently. Values
// are compared by the year they name, so "founded 1998" and 1998 agree while
// 1998 and 2004 do not. Given no properties it reads Dates.
func Times(es []Entity, props ...string) []Conflict {
	if len(props) == 0 {
		props = Dates
	}
	by := pack(es)
	var out []Conflict
	for _, id := range order(es) {
		g := by[id]
		if len(g) < 2 {
			continue
		}
		for _, p := range props {
			cs := state(g, p)
			if distinct(cs, stamp) < 2 {
				continue
			}
			out = append(out, found(Time, id, p, cs))
		}
	}
	return out
}

// Overlaps reports the entities whose documents give spans that run over one
// another without agreeing. Two documents that date a fact to separate spans
// may both be right — the fact held twice. Two that date it to spans sharing
// a day cannot: one of them is wrong about when.
func Overlaps(es []Entity, from, to string) []Conflict {
	by := pack(es)
	var out []Conflict
	for _, id := range order(es) {
		g := by[id]
		var spans []Claim
		var bounds [][2]time.Time
		for _, e := range g {
			lo, okLo := when(prop(e, from))
			hi, okHi := when(prop(e, to))
			if !okLo || !okHi || hi.Before(lo) {
				continue
			}
			spans = append(spans, Claim{Value: lo.Format(time.RFC3339) + "/" + hi.Format(time.RFC3339), From: e.From})
			bounds = append(bounds, [2]time.Time{lo, hi})
		}
		for i := range bounds {
			clash := false
			for j := i + 1; j < len(bounds); j++ {
				if bounds[i] == bounds[j] {
					continue
				}
				if !bounds[i][1].Before(bounds[j][0]) && !bounds[j][1].Before(bounds[i][0]) {
					clash = true
				}
			}
			if clash {
				c := found(Time, id, from+".."+to, spans)
				c.Advice = "the spans run over one another; at most one can hold"
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// Limits reports values outside the range declared for their property. This
// is the one disagreement a single document can hold on its own: it is at
// odds with what the property means, not with another document.
func Limits(es []Entity, lim map[string]Limit) []Conflict {
	var out []Conflict
	for _, e := range es {
		for _, p := range slices.Sorted(maps.Keys(lim)) {
			v, ok := prop(e, p)
			if !ok {
				continue
			}
			n, ok := number(v)
			if !ok || (n >= lim[p].Lo && n <= lim[p].Hi) {
				continue
			}
			c := found(Range, e.key(), p, []Claim{{Value: v, From: e.From}})
			c.Level = High
			c.Score = sure(e.From)
			c.Advice = fmt.Sprintf("%v is outside %v to %v", v, lim[p].Lo, lim[p].Hi)
			out = append(out, c)
		}
	}
	return out
}

// Excludes reports entities their documents call two kinds that cannot both
// hold. Given no table it uses Apart. One conflict is reported per entity:
// the first excluded pair found is enough to say the classification is wrong.
func Excludes(es []Entity, d Disjoint) []Conflict {
	if d == nil {
		d = Apart
	}
	by := pack(es)
	var out []Conflict
	for _, id := range order(es) {
		g := by[id]
		if len(g) < 2 {
			continue
		}
		var cs []Claim
		for _, e := range g {
			if e.Kind != "" {
				cs = append(cs, Claim{Value: e.Kind, From: e.From})
			}
		}
		if p, ok := excluded(cs, d); ok {
			c := found(Logic, id, "kind", []Claim{cs[p[0]], cs[p[1]]})
			c.Level = Critical
			c.Score = 1
			c.Advice = fmt.Sprintf("%v and %v exclude one another", cs[p[0]].Value, cs[p[1]].Value)
			out = append(out, c)
		}
	}
	return out
}

// Edges reports subjects given more than one object for one relation. A
// predicate named in many may hold several objects and is not checked; with
// no such table every predicate is read as admitting one object, which is the
// strict reading and the one to start from.
func Edges(ts []semantic.Triple, c Canon, many map[string]bool) []Conflict {
	type link struct{ subject, predicate string }
	var seen []link
	by := map[link][]Claim{}
	for _, t := range ts {
		k := c.Key(t)
		l := link{fold(k[0]), k[1]}
		if many[l.predicate] {
			continue
		}
		if _, ok := by[l]; !ok {
			seen = append(seen, l)
		}
		by[l] = append(by[l], Claim{
			Value: t.Object,
			From:  Source{Doc: t.From.DocID, Score: t.Score},
		})
	}
	var out []Conflict
	for _, l := range seen {
		cs := by[l]
		if distinct(cs, nil) < 2 {
			continue
		}
		out = append(out, found(Edge, l.subject, l.predicate, cs))
	}
	return out
}

// Watch says what to compare when looking for disagreement. The zero value
// compares every property any document states, every kind, and the properties
// in Dates, against the exclusions in Apart.
type Watch struct {
	Props  []string // properties to compare; nil compares every one stated
	Times  []string // properties read as dates; nil reads Dates
	Limits map[string]Limit
	Apart  Disjoint
}

// Conflicts runs every check Watch asks for and returns what they found, in
// one list. Each check names its conflicts differently, so a value and a date
// disagreeing about the same property are two findings, not one repeated.
func Conflicts(es []Entity, w Watch) []Conflict {
	props := w.Props
	if props == nil {
		props = stated(es)
	}
	var out []Conflict
	for _, p := range props {
		out = append(out, Values(es, p)...)
	}
	out = append(out, Types(es)...)
	out = append(out, Times(es, w.Times...)...)
	if len(w.Limits) > 0 {
		out = append(out, Limits(es, w.Limits)...)
	}
	return append(out, Excludes(es, w.Apart)...)
}

// Claims returns every value stated for prop, with the document that stated
// it, keyed by the thing it was stated about. It is the record of who said
// what, which is what a disagreement is settled from.
func Claims(es []Entity, prop string) map[string][]Claim {
	out := map[string][]Claim{}
	by := pack(es)
	for _, id := range order(es) {
		if cs := state(by[id], prop); len(cs) > 0 {
			out[id] = cs
		}
	}
	return out
}

// Tally counts a set of conflicts along the four ways a reader asks about
// them: what kind they are, how much they matter, which documents are
// involved, and which properties keep disagreeing.
type Tally struct {
	Total int
	Kind  map[Kind]int
	Level map[Level]int
	Doc   map[string]int
	Prop  map[string]int
}

// Count tallies conflicts. A document is counted once per conflict it appears
// in, however many of the conflicting values it stated.
func Count(cs []Conflict) Tally {
	t := Tally{
		Total: len(cs),
		Kind:  map[Kind]int{},
		Level: map[Level]int{},
		Doc:   map[string]int{},
		Prop:  map[string]int{},
	}
	for _, c := range cs {
		t.Kind[c.Kind]++
		t.Level[c.Level]++
		if c.Prop != "" {
			t.Prop[c.Prop]++
		}
		once := map[string]bool{}
		for _, v := range c.Values {
			if v.From.Doc == "" || once[v.From.Doc] {
				continue
			}
			once[v.From.Doc] = true
			t.Doc[v.From.Doc]++
		}
	}
	return t
}

// found builds a conflict and works out how sure and how grave it is.
func found(k Kind, subject, p string, cs []Claim) Conflict {
	return Conflict{
		ID:      subject + "." + p + "#" + string(k),
		Kind:    k,
		Subject: subject,
		Prop:    p,
		Values:  cs,
		Score:   trust(cs),
		Level:   grade(p, cs),
		Advice:  advice(cs),
	}
}

// trust is how sure the disagreement is real: how much the documents behind
// it are believed, raised by how far apart their values are. Documents that
// state no confidence are given half.
func trust(cs []Claim) float64 {
	if len(cs) == 0 {
		return 0
	}
	var sum float64
	for _, c := range cs {
		sum += sure(c.From)
	}
	avg := sum / float64(len(cs))
	spread := float64(distinct(cs, nil)) / float64(len(cs))
	if v := avg * (1 + spread); v < 1 {
		return v
	}
	return 1
}

// grade weighs how much a disagreement matters. Values that differ only in
// spelling matter least; what a thing is called or is matters most; numbers
// far apart matter more than numbers close together.
func grade(p string, cs []Claim) Level {
	if distinct(cs, fold) < 2 {
		return Low
	}
	switch p {
	case "id", "name", "kind", "type", "class":
		return Critical
	}
	lo, hi, ok := span(cs)
	if ok && hi-lo > 1000 {
		return High
	}
	return Medium
}

// advice says what to do next, in the terms the disagreement is in. A check
// that knows more about its own finding than this says so instead.
func advice(cs []Claim) string {
	switch n := distinct(cs, nil); {
	case n < 2:
		return ""
	case n == 2:
		return "two values are stated; take the newer or the more trusted"
	default:
		return "more than two values are stated; read the documents before choosing"
	}
}

// pack buckets records by the thing they describe.
func pack(es []Entity) map[string][]Entity {
	out := map[string][]Entity{}
	for _, e := range es {
		out[e.key()] = append(out[e.key()], e)
	}
	return out
}

// order lists the things described, first mentioned first, so that a run over
// them does not depend on map iteration.
func order(es []Entity) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range es {
		if k := e.key(); !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// stated lists every property any record states, in a fixed order.
func stated(es []Entity) []string {
	seen := map[string]bool{}
	for _, e := range es {
		for k := range e.Props {
			seen[k] = true
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

// state collects what a group of records says about one property.
func state(es []Entity, p string) []Claim {
	var out []Claim
	for _, e := range es {
		if v, ok := prop(e, p); ok {
			out = append(out, Claim{Value: v, From: e.From})
		}
	}
	return out
}

// prop reads a property, skipping the records that state nothing for it.
func prop(e Entity, p string) (any, bool) {
	v, ok := e.Value(p)
	if !ok || v == nil {
		return nil, false
	}
	return v, true
}

// distinct counts how many different things were said, after norm. A nil norm
// compares the values as written, which is what tells a disagreement from a
// repetition.
func distinct(cs []Claim, norm func(string) string) int {
	seen := map[string]bool{}
	for _, c := range cs {
		s := fmt.Sprint(c.Value)
		if norm != nil {
			s = norm(s)
		}
		seen[s] = true
	}
	return len(seen)
}

// stamp reduces a date to the year it names, so that dates written differently
// are compared by what they mean.
func stamp(s string) string {
	if y := year.FindString(s); y != "" {
		return y
	}
	return fold(s)
}

// span is the lowest and highest numbers among the values, if they are numbers.
func span(cs []Claim) (float64, float64, bool) {
	var lo, hi float64
	found := false
	for _, c := range cs {
		n, ok := number(c.Value)
		if !ok {
			continue
		}
		if !found || n < lo {
			lo = n
		}
		if !found || n > hi {
			hi = n
		}
		found = true
	}
	return lo, hi, found
}

// number reads a value as a number, whether it was stated as one or written
// out. It reports false for anything that is not a number at all.
func number(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	case string:
		f, err := strconv.ParseFloat(fold(n), 64)
		return f, err == nil
	}
	return 0, false
}

// when reads a value as a point in time: a time, a date, or a year.
func when(v any, ok bool) (time.Time, bool) {
	if !ok {
		return time.Time{}, false
	}
	if t, is := v.(time.Time); is {
		return t, true
	}
	s := fold(fmt.Sprint(v))
	for _, layout := range [...]string{time.RFC3339, "2006-01-02", "2006-01", "2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	if y := year.FindString(s); y != "" {
		t, err := time.Parse("2006", y)
		return t, err == nil
	}
	return time.Time{}, false
}

// excluded finds the first pair of kinds the table forbids together.
func excluded(cs []Claim, d Disjoint) ([2]int, bool) {
	for i := range cs {
		a := fold(fmt.Sprint(cs[i].Value))
		for j := i + 1; j < len(cs); j++ {
			b := fold(fmt.Sprint(cs[j].Value))
			if slices.Contains(d[a], b) || slices.Contains(d[b], a) {
				return [2]int{i, j}, true
			}
		}
	}
	return [2]int{}, false
}
