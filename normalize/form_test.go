package normalize

import (
	"fmt"
	"strings"
	"testing"
)

// runes writes a string as its codepoints, which is the only readable way to
// state what a normalization form did.
func runes(s string) string {
	out := make([]string, 0, len(s))
	for _, r := range s {
		out = append(out, fmt.Sprintf("%04X", r))
	}
	return strings.Join(out, " ")
}

// str builds a string from codepoints, so a test case says what it means
// rather than carrying invisible characters.
func str(rs ...rune) string { return string(rs) }

// TestForms checks the four normalization forms against the vectors UAX #15
// states, which are chosen to separate the forms from one another.
func TestForms(t *testing.T) {
	for _, c := range []struct {
		name                 string
		in                   string
		nfd, nfc, nfkd, nfkc string
	}{
		{
			// UAX #15, figure 6: the example that distinguishes all four
			// forms. Long s with dot above, followed by a dot below.
			name: "long s with two dots",
			in:   str(0x1E9B, 0x0323),
			nfd:  str(0x017F, 0x0323, 0x0307),
			nfc:  str(0x1E9B, 0x0323),
			nfkd: str(0x0073, 0x0323, 0x0307),
			nfkc: str(0x1E69),
		},
		{
			// A singleton decomposition never composes back, so the angstrom
			// sign becomes the letter and stays the letter.
			name: "angstrom sign",
			in:   str(0x212B),
			nfd:  str(0x0041, 0x030A),
			nfc:  str(0x00C5),
			nfkd: str(0x0041, 0x030A),
			nfkc: str(0x00C5),
		},
		{
			name: "ohm sign",
			in:   str(0x2126),
			nfd:  str(0x03A9), nfc: str(0x03A9), nfkd: str(0x03A9), nfkc: str(0x03A9),
		},
		{
			// A compatibility decomposition, so the canonical forms leave it.
			name: "fi ligature",
			in:   str(0xFB01),
			nfd:  str(0xFB01), nfc: str(0xFB01),
			nfkd: "fi", nfkc: "fi",
		},
		{
			name: "fullwidth A",
			in:   str(0xFF21),
			nfd:  str(0xFF21), nfc: str(0xFF21),
			nfkd: "A", nfkc: "A",
		},
		{
			name: "superscript two",
			in:   str(0x00B2),
			nfd:  str(0x00B2), nfc: str(0x00B2),
			nfkd: "2", nfkc: "2",
		},
		{
			// A no-break space is compatibility-equivalent to a space, and
			// canonically equivalent to nothing.
			name: "no-break space",
			in:   str(0x0061, 0x00A0, 0x0062),
			nfd:  str(0x0061, 0x00A0, 0x0062), nfc: str(0x0061, 0x00A0, 0x0062),
			nfkd: "a b", nfkc: "a b",
		},
		{
			// Hangul decomposes and composes arithmetically rather than by
			// table: a syllable with a trailing consonant exercises all three
			// jamo positions.
			name: "hangul",
			in:   str(0xD4DB),
			nfd:  str(0x1111, 0x1171, 0x11B6),
			nfc:  str(0xD4DB),
			nfkd: str(0x1111, 0x1171, 0x11B6),
			nfkc: str(0xD4DB),
		},
		{
			// Marks are put in canonical order by combining class — ogonek
			// (202) before acute (230) — whatever order they arrived in. The
			// pair then composes as far as a precomposed character exists,
			// which for a with ogonek and acute is one step.
			name: "marks reorder before composing",
			in:   str(0x0061, 0x0301, 0x0328),
			nfd:  str(0x0061, 0x0328, 0x0301),
			nfc:  str(0x0105, 0x0301),
			nfkd: str(0x0061, 0x0328, 0x0301),
			nfkc: str(0x0105, 0x0301),
		},
		{
			// A composition exclusion: the precomposed character decomposes,
			// and the pieces do not compose back into it.
			name: "devanagari composition exclusion",
			in:   str(0x0958),
			nfd:  str(0x0915, 0x093C), nfc: str(0x0915, 0x093C),
			nfkd: str(0x0915, 0x093C), nfkc: str(0x0915, 0x093C),
		},
		{
			name: "ascii is already in every form",
			in:   "plain text",
			nfd:  "plain text", nfc: "plain text", nfkd: "plain text", nfkc: "plain text",
		},
		{
			name: "empty",
			in:   "",
			nfd:  "", nfc: "", nfkd: "", nfkc: "",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			for _, f := range []struct {
				name string
				fn   func(string) string
				want string
			}{
				{"NFD", NFD, c.nfd},
				{"NFC", NFC, c.nfc},
				{"NFKD", NFKD, c.nfkd},
				{"NFKC", NFKC, c.nfkc},
			} {
				if got := f.fn(c.in); got != f.want {
					t.Errorf("%s(%s) = %s, want %s", f.name, runes(c.in), runes(got), runes(f.want))
				}
			}
		})
	}
}

// The forms are idempotent and agree with each other, which is what makes them
// usable as a comparison key. UAX #15 states these as invariants; they catch
// the ordering and composition mistakes a table of examples can miss.
func TestFormInvariants(t *testing.T) {
	for _, s := range []string{
		"plain",
		str(0x1E9B, 0x0323),
		str(0x0061, 0x0301, 0x0328, 0x0304),
		str(0xD4DB, 0x1100, 0x1161),
		str(0x212B, 0xFB01, 0xFF21),
		str(0x0958, 0x0915, 0x093C),
		"Crème Brûlée",
	} {
		t.Run(runes(s), func(t *testing.T) {
			for _, f := range []struct {
				name string
				fn   func(string) string
			}{{"NFC", NFC}, {"NFD", NFD}, {"NFKC", NFKC}, {"NFKD", NFKD}} {
				if once, twice := f.fn(s), f.fn(f.fn(s)); once != twice {
					t.Errorf("%s is not idempotent: %s then %s", f.name, runes(once), runes(twice))
				}
			}
			if got, want := NFC(NFD(s)), NFC(s); got != want {
				t.Errorf("NFC(NFD) = %s, want %s", runes(got), runes(want))
			}
			if got, want := NFD(NFC(s)), NFD(s); got != want {
				t.Errorf("NFD(NFC) = %s, want %s", runes(got), runes(want))
			}
			// The compatibility forms are the canonical ones over the
			// compatibility decomposition.
			if got, want := NFC(NFKD(s)), NFKC(s); got != want {
				t.Errorf("NFC(NFKD) = %s, want NFKC = %s", runes(got), runes(want))
			}
		})
	}
}

func TestMarks(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"Crème Brûlée", "Creme Brulee"},
		{"Ada", "Ada"},
		{str(0x0065, 0x0301), "e"}, // decomposed already
		{str(0x00E9), "e"},         // composed
		{str(0xD4DB), str(0xD4DB)}, // hangul jamo are not marks
		{"", ""},
	} {
		if got := Marks(c.in); got != c.want {
			t.Errorf("Marks(%s) = %s, want %s", runes(c.in), runes(got), runes(c.want))
		}
	}
}
