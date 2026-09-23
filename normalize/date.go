package normalize

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Span is the stretch of time a date names, half-open: From is its first
// instant and Until the first instant after it. "March 2023" is
// [2023-03-01, 2023-04-01), so a month contains its days, two spans overlap
// exactly when the times they name do, and a day is never mistaken for its
// midnight.
//
// A span of a day or longer is a calendar date, and names days, not instants
// in some zone: its bounds are midnights in UTC standing for those dates. A
// span of a minute or shorter is a time, and its bounds carry the offset it
// was written with or the one Anchor.Zone gave it.
type Span struct {
	From, Until time.Time
	Grain       Grain
}

// Grain is the unit a date was written to. The zero Grain is a span given by
// its bounds alone, as an ISO 8601 interval.
type Grain int

// The grains a date can be written to. A time with a fraction of a second is
// an instant, and spans one nanosecond.
const (
	Year Grain = iota + 1
	Month
	Day
	Minute
	Second
	Nano
)

// Date reads a date or a time into the Span it names.
//
// It reads ISO 8601 and RFC 3339 — 2023, 2023-03, 2023-03-01,
// 2023-03-01T10:00, 2023-03-01T10:00:00.5+09:00 — an interval of two RFC 3339
// times joined by "/", English month names — March 2023, 1 March 2023,
// March 1, 2023, Mar. 2023 — all-numeric dates with /, . or - between the
// parts, and today, yesterday and tomorrow.
//
// It refuses, with ErrAmbiguous, what it would otherwise have to guess: an
// all-numeric date that does not begin with its year when Anchor.Order is
// unset, a two-digit year, a date with no year, a time with no offset when
// Anchor.Zone is unset or names a zone where that clock time happens twice,
// and a relative date when Anchor.At is zero.
var Date = Kind[Span]{Name: "date", Parse: date, Canon: when}

var (
	iso     = regexp.MustCompile(`^(\d{4})(?:-(\d{2})(?:-(\d{2})(?:[T ](\d{2}):(\d{2})(?::(\d{2})(\.\d{1,9})?)?(Z|[+-]\d{2}:\d{2})?)?)?)?$`)
	numeric = regexp.MustCompile(`^(\d{1,4})([/.-])(\d{1,4})(?:([/.-])(\d{1,4}))?$`)
	named   = regexp.MustCompile(`^(?:(\d{1,2})(?:st|nd|rd|th)? )?([a-z]+)\.?(?: (\d{1,2})(?:st|nd|rd|th)?)?,? ?(\d{4})?$`)
)

// months are the English month names and their abbreviations.
var months = map[string]time.Month{
	"january": 1, "february": 2, "march": 3, "april": 4, "may": 5, "june": 6,
	"july": 7, "august": 8, "september": 9, "october": 10, "november": 11, "december": 12,
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "jun": 6, "jul": 7, "aug": 8,
	"sep": 9, "sept": 9, "oct": 10, "nov": 11, "dec": 12,
}

func date(a Anchor, s string) (Span, error) {
	t := strings.Join(strings.Fields(s), " ")
	low := strings.ToLower(t)
	switch low {
	case "today", "yesterday", "tomorrow":
		if a.At.IsZero() {
			return Span{}, refuse("date", s, ErrAmbiguous, "a relative date needs Anchor.At, the source's own date")
		}
		y, m, d := a.At.Date()
		shift := map[string]int{"yesterday": -1, "today": 0, "tomorrow": 1}[low]
		return civil(y, m, d+shift, Day), nil
	}
	if from, until, ok := strings.Cut(t, "/"); ok && strings.Contains(from, "T") {
		f, err1 := time.Parse(time.RFC3339Nano, from)
		u, err2 := time.Parse(time.RFC3339Nano, until)
		if err1 != nil || err2 != nil || !f.Before(u) {
			return Span{}, refuse("date", s, ErrSyntax, "an interval is two RFC 3339 times, the first before the second")
		}
		return Span{From: f, Until: u}, nil
	}
	if m := iso.FindStringSubmatch(strings.ToUpper(t)); m != nil {
		return stamp(a, s, m)
	}
	if m := numeric.FindStringSubmatch(t); m != nil {
		return numerals(a, s, m)
	}
	if m := named.FindStringSubmatch(low); m != nil {
		if mo, ok := months[m[2]]; ok && (m[1] == "" || m[3] == "") {
			day := m[1] + m[3]
			switch {
			case m[4] == "":
				return Span{}, refuse("date", s, ErrAmbiguous, "no year")
			case day == "":
				return check(s, int(num(m[4])), mo, 1, Month)
			}
			return check(s, int(num(m[4])), mo, int(num(day)), Day)
		}
	}
	return Span{}, refuse("date", s, ErrSyntax, "not a date this package reads")
}

// stamp reads the ISO 8601 and RFC 3339 forms.
func stamp(a Anchor, s string, m []string) (Span, error) {
	y := int(num(m[1]))
	switch {
	case m[2] == "":
		return check(s, y, 1, 1, Year)
	case m[3] == "":
		return check(s, y, time.Month(num(m[2])), 1, Month)
	case m[4] == "":
		return check(s, y, time.Month(num(m[2])), int(num(m[3])), Day)
	}
	mo, d := time.Month(num(m[2])), int(num(m[3]))
	h, mi, sec := int(num(m[4])), int(num(m[5])), int(num(m[6]))
	grain, ns := Minute, 0
	if m[6] != "" {
		grain = Second
	}
	if m[7] != "" {
		frac := (m[7][1:] + "000000000")[:9]
		ns, grain = int(num(frac)), Nano
	}
	loc := a.Zone
	switch off := m[8]; {
	case off == "Z":
		loc = time.UTC
	case off != "":
		hh, mm := int(num(off[1:3])), int(num(off[4:6]))
		if hh > 23 || mm > 59 {
			return Span{}, refuse("date", s, ErrSyntax, "an offset out of range")
		}
		sign := 1
		if off[0] == '-' {
			sign = -1
		}
		loc = time.FixedZone("", sign*(hh*3600+mm*60))
	case loc == nil:
		return Span{}, refuse("date", s, ErrAmbiguous, "a time with no offset needs Anchor.Zone")
	}
	from, err := place(s, loc, y, mo, d, h, mi, sec, ns)
	if err != nil {
		return Span{}, err
	}
	step := map[Grain]time.Duration{Minute: time.Minute, Second: time.Second, Nano: time.Nanosecond}[grain]
	return Span{From: from, Until: from.Add(step), Grain: grain}, nil
}

// numerals reads an all-numeric date that is not ISO 8601: one that begins
// with its year, which settles the rest, a month and a year, or a day and a
// month in the order the Anchor gives. A year is all four digits, since which
// century "23" means is a guess.
func numerals(a Anchor, s string, m []string) (Span, error) {
	p1, sep, p2, sep2, p3 := m[1], m[2], m[3], m[4], m[5]
	if p3 == "" {
		switch {
		case len(p1) == 4 && len(p2) <= 2: // 2023/03
			return check(s, int(num(p1)), time.Month(num(p2)), 1, Month)
		case len(p1) <= 2 && len(p2) == 4: // 03/2023
			return check(s, int(num(p2)), time.Month(num(p1)), 1, Month)
		}
		return Span{}, refuse("date", s, ErrAmbiguous, "no four-digit year")
	}
	switch {
	case sep != sep2:
		return Span{}, refuse("date", s, ErrSyntax, "mixed separators")
	case len(p1) == 4 && len(p2) <= 2 && len(p3) <= 2:
		return check(s, int(num(p1)), time.Month(num(p2)), int(num(p3)), Day)
	case len(p1) > 2 || len(p2) > 2:
		return Span{}, refuse("date", s, ErrSyntax, "not a date this package reads")
	case len(p3) != 4:
		return Span{}, refuse("date", s, ErrAmbiguous, "no four-digit year")
	}
	switch a.Order {
	case DMY:
		return check(s, int(num(p3)), time.Month(num(p2)), int(num(p1)), Day)
	case MDY:
		return check(s, int(num(p3)), time.Month(num(p1)), int(num(p2)), Day)
	}
	return Span{}, refuse("date", s, ErrAmbiguous, "day and month order needs Anchor.Order")
}

// check builds the civil span of a year, month or day, refusing a date the
// calendar does not have.
func check(s string, y int, m time.Month, d int, g Grain) (Span, error) {
	if y < 1 || m < 1 || m > 12 || d < 1 || d > 31 {
		return Span{}, refuse("date", s, ErrSyntax, "no such date")
	}
	t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	if t.Month() != m {
		return Span{}, refuse("date", s, ErrSyntax, "no such date")
	}
	return civil(y, m, d, g), nil
}

// civil is the span of a calendar year, month or day, bounded by midnights in
// UTC standing for the dates.
func civil(y int, m time.Month, d int, g Grain) Span {
	from := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	switch g {
	case Year:
		return Span{From: from, Until: from.AddDate(1, 0, 0), Grain: g}
	case Month:
		return Span{From: from, Until: from.AddDate(0, 1, 0), Grain: g}
	}
	return Span{From: from, Until: from.AddDate(0, 0, 1), Grain: Day}
}

// place finds the instant a clock time names in loc. A clock time that a
// change of offset skips names none, and one it repeats names two; the
// first is not a time and the second is ambiguous, and neither is guessed at
// the way time.Date would.
func place(s string, loc *time.Location, y int, mo time.Month, d, h, mi, sec, ns int) (time.Time, error) {
	if mo < 1 || mo > 12 || d < 1 || d > 31 || h > 23 || mi > 59 || sec > 59 {
		return time.Time{}, refuse("date", s, ErrSyntax, "no such time")
	}
	guess := time.Date(y, mo, d, h, mi, sec, ns, loc)
	var found []time.Time
	seen := map[int]bool{}
	for _, near := range []time.Time{guess.Add(-12 * time.Hour), guess, guess.Add(12 * time.Hour)} {
		_, off := near.Zone()
		if seen[off] {
			continue
		}
		seen[off] = true
		t := time.Date(y, mo, d, h, mi, sec, ns, time.FixedZone("", off)).In(loc)
		if _, o := t.Zone(); o == off && same(t, y, mo, d, h, mi, sec) {
			found = append(found, t)
		}
	}
	switch len(found) {
	case 0:
		return time.Time{}, refuse("date", s, ErrSyntax, "no such time in "+loc.String())
	case 1:
		return found[0], nil
	}
	return time.Time{}, refuse("date", s, ErrAmbiguous, "that clock time happens twice in "+loc.String())
}

// same reports whether t reads as the given clock time.
func same(t time.Time, y int, mo time.Month, d, h, mi, sec int) bool {
	ty, tm, td := t.Date()
	th, tmi, ts := t.Clock()
	return ty == y && tm == mo && td == d && th == h && tmi == mi && ts == sec
}

// when writes a span in its canonical form: as short as its grain allows,
// with the offset for a time, and as an interval when it has no grain.
func when(s Span) string {
	switch s.Grain {
	case Year:
		return s.From.Format("2006")
	case Month:
		return s.From.Format("2006-01")
	case Day:
		return s.From.Format("2006-01-02")
	case Minute:
		return s.From.Format("2006-01-02T15:04Z07:00")
	case Second:
		return s.From.Format(time.RFC3339)
	case Nano:
		return s.From.Format("2006-01-02T15:04:05.000000000Z07:00")
	}
	return s.From.Format(time.RFC3339Nano) + "/" + s.Until.Format(time.RFC3339Nano)
}

// num reads a run of ASCII digits the patterns above have already matched.
func num(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}
