package normalize

import (
	"maps"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// The four Unicode normalization forms, as defined by UAX #15. NFC and NFD
// differ only in how a character and its marks are spelled; NFKC and NFKD also
// fold compatibility characters — ligatures, fullwidth Latin, superscripts —
// onto their plain equivalents. Use NFC to make text comparable, NFKC to make
// it comparable and plain.

// NFC returns s with characters composed: "e" plus a combining acute becomes
// "é". Text that is already NFC is returned unchanged.
func NFC(s string) string { return form(s, false, true) }

// NFD returns s with characters decomposed: "é" becomes "e" plus a combining
// acute, and marks are put in canonical order.
func NFD(s string) string { return form(s, false, false) }

// NFKC returns s with compatibility characters folded to their plain
// equivalents and the result composed: "ﬁ" becomes "fi", "１" becomes "1".
func NFKC(s string) string { return form(s, true, true) }

// NFKD returns s folded like NFKC but left decomposed.
func NFKD(s string) string { return form(s, true, false) }

// Marks returns s without combining marks, so "Crème Brûlée" becomes
// "Creme Brulee". Letters that carry no mark are untouched.
func Marks(s string) string {
	if ascii(s) {
		return s
	}
	rs := decompose(s, false)
	out := rs[:0]
	for _, r := range rs {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		out = append(out, r)
	}
	return string(compose(out))
}

func form(s string, kompat, join bool) string {
	// ASCII is in every normalization form already, and most text is ASCII.
	if ascii(s) {
		return s
	}
	rs := decompose(s, kompat)
	if join {
		rs = compose(rs)
	}
	return string(rs)
}

func ascii(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// Hangul syllables decompose and compose arithmetically rather than by table.
// The constants are from UAX #15, section 16.
const (
	lBase, vBase, tBase = 0x1100, 0x1161, 0x11A7
	sBase               = 0xAC00
	lCount, vCount      = 19, 21
	tCount              = 28
	nCount              = vCount * tCount
	sCount              = lCount * nCount
)

func decompose(s string, kompat bool) []rune {
	tables()
	m := canon
	if kompat {
		m = compat
	}
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= sBase && r < sBase+sCount:
			i := r - sBase
			out = append(out, lBase+i/nCount, vBase+(i%nCount)/tCount)
			if t := i % tCount; t != 0 {
				out = append(out, tBase+t)
			}
		default:
			if d, ok := m[r]; ok {
				out = append(out, d...)
			} else {
				out = append(out, r)
			}
		}
	}
	order(out)
	return out
}

// order puts each run of combining marks into canonical order: a stable sort by
// combining class, with starters acting as barriers.
func order(rs []rune) {
	for i := 1; i < len(rs); i++ {
		c := class(rs[i])
		if c == 0 {
			continue
		}
		for j := i; j > 0 && class(rs[j-1]) > c; j-- {
			rs[j], rs[j-1] = rs[j-1], rs[j]
		}
	}
}

// compose recombines a decomposed sequence. A mark joins the starter before it
// unless another mark of the same or higher class stands between them.
func compose(rs []rune) []rune {
	if len(rs) == 0 {
		return rs
	}
	tables()
	out := make([]rune, 1, len(rs))
	out[0] = rs[0]
	star := 0
	last := -1
	if class(rs[0]) != 0 {
		last = int(class(rs[0]))
		star = -1
	}
	for _, r := range rs[1:] {
		c := int(class(r))
		if star >= 0 && last < c {
			if j, ok := join(out[star], r); ok {
				out[star] = j
				continue
			}
		}
		if c == 0 {
			star = len(out)
			last = -1
		} else {
			last = c
		}
		out = append(out, r)
	}
	return out
}

// join returns the single character that a and b compose to, if any.
func join(a, b rune) (rune, bool) {
	if l := a - lBase; l >= 0 && l < lCount {
		if v := b - vBase; v >= 0 && v < vCount {
			return sBase + (l*vCount+v)*tCount, true
		}
	}
	if s := a - sBase; s >= 0 && s < sCount && s%tCount == 0 {
		if t := b - tBase; t > 0 && t < tCount {
			return a + t, true
		}
	}
	r, ok := pairs[[2]rune{a, b}]
	return r, ok
}

func class(r rune) uint8 { return classes[r] }

var (
	once    sync.Once
	canon   map[rune][]rune  // full canonical decomposition
	compat  map[rune][]rune  // full compatibility decomposition
	classes map[rune]uint8   // canonical combining class, absent means zero
	pairs   map[[2]rune]rune // primary composites, exclusions removed
)

// tables parses the Unicode data on first use. Parsing costs a few
// milliseconds, so a program that only touches ASCII never pays it.
func tables() { once.Do(build) }

func build() {
	step := parse(canonData)
	kompat := parse(compatData)

	// A compatibility decomposition may name characters that decompose
	// further, canonically or compatibly, so expand over both tables.
	both := make(map[rune][]rune, len(step)+len(kompat))
	maps.Copy(both, step)
	maps.Copy(both, kompat)
	canon = make(map[rune][]rune, len(step))
	for r := range step {
		canon[r] = expand(step, []rune{r})
	}
	compat = make(map[rune][]rune, len(both))
	for r := range both {
		compat[r] = expand(both, []rune{r})
	}

	classes = make(map[rune]uint8)
	for _, line := range lines(classData) {
		span, c, _ := strings.Cut(line, ":")
		lo, hi, _ := strings.Cut(span, "-")
		n, _ := strconv.ParseUint(c, 10, 8)
		for r := code(lo); r <= code(hi); r++ {
			classes[r] = uint8(n)
		}
	}

	blocked := map[rune]bool{}
	for f := range strings.FieldsSeq(blockData) {
		blocked[code(f)] = true
	}
	pairs = make(map[[2]rune]rune, len(step))
	for r, d := range step {
		if len(d) == 2 && !blocked[r] {
			pairs[[2]rune{d[0], d[1]}] = r
		}
	}
}

// expand rewrites a sequence until no character in it decomposes further.
func expand(m map[rune][]rune, rs []rune) []rune {
	out := make([]rune, 0, len(rs))
	for _, r := range rs {
		if d, ok := m[r]; ok {
			out = append(out, expand(m, d)...)
			continue
		}
		out = append(out, r)
	}
	return out
}

func parse(data string) map[rune][]rune {
	m := make(map[rune][]rune, 4096)
	for _, line := range lines(data) {
		src, dst, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(dst)
		rs := make([]rune, len(f))
		for i, x := range f {
			rs[i] = code(x)
		}
		m[code(src)] = rs
	}
	return m
}

func lines(data string) []string {
	var out []string
	for l := range strings.SplitSeq(data, "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func code(hex string) rune {
	n, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return 0
	}
	return rune(n)
}
