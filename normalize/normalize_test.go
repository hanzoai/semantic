package normalize

import (
	"reflect"
	"testing"
)

func TestLines(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"a\r\nb\rc\nd", "a\nb\nc\nd"},
		{"already\nfine", "already\nfine"},
		{"", ""},
	} {
		if got := Lines(c.in); got != c.want {
			t.Errorf("Lines(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestControl(t *testing.T) {
	// Tab and newline carry text; the rest of the control codes, the byte
	// order mark and the soft hyphen do not.
	in := "a\x00b" + str(0xFEFF) + "c" + str(0x00AD) + "de\x1bf\tg\nh"
	if got, want := Control(in), "abcdef\tg\nh"; got != want {
		t.Errorf("Control = %q, want %q", got, want)
	}
}

func TestSpace(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"runs collapse", "a  b\t\tc", "a b c"},
		{"trailing space goes", "a \n b ", "a\nb"},
		{"a run of blank lines is one blank line", "a\n\n\n\nb", "a\n\nb"},
		{"a paragraph break survives", "a\n\nb", "a\n\nb"},
		{"a line break survives", "a\nb", "a\nb"},
		{"every kind of space is a space", "a" + str(0x00A0) + "b" + str(0x2003) + "c", "a b c"},
		{"CRLF blank lines are blank lines", "a\r\n\r\n\r\n\r\nb", "a\n\nb"},
		{"CRLF closes a fence", "a  b\r\n```\r\ncode  x\r\n```\r\n\r\n\r\nafter   text  \r\n", "a b\n```\ncode  x\n```\n\nafter text"},
		{"empty", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := Space(c.in); got != c.want {
				t.Errorf("Space(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestSpaceCode holds Space off code. Indentation is the meaning of code —
// Python's blocks, a Makefile's tabs, the alignment in a table of numbers —
// so a CommonMark fenced block, from its opening fence to its closing one,
// and an indented block are left byte for byte, blank lines, trailing spaces
// and all. The prose around them is still cleaned.
func TestSpaceCode(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{
			"fenced",
			"Text  here \n\n\n```go\nfunc a() {\n\tif x  {\n\t\treturn  \n\t}\n\n\n}\n```\n\n\nafter   text  ",
			"Text here\n\n```go\nfunc a() {\n\tif x  {\n\t\treturn  \n\t}\n\n\n}\n```\n\nafter text",
		},
		{
			"tildes, and a shorter fence inside does not close",
			"~~~~\n  a  b\n~~~\n  c\n~~~~\nd  e",
			"~~~~\n  a  b\n~~~\n  c\n~~~~\nd e",
		},
		{
			"a fence closes only on its own character",
			"```\nx  y\n~~~\nz  w\n```\nv  w",
			"```\nx  y\n~~~\nz  w\n```\nv w",
		},
		{
			"an indented fence, up to three spaces",
			"   ```\n  x  \n   ```\n  y  ",
			"   ```\n  x  \n   ```\ny",
		},
		{
			"an unclosed fence runs to the end",
			"a  b\n```\n  x  \n\n\n  y\n",
			"a b\n```\n  x  \n\n\n  y\n",
		},
		{
			"indented code, with the blank lines inside it",
			"Para  one\n\n    code  line\n\tmore\t code\n\n\n    after  blank\n\n\n\nnext   para",
			"Para one\n\n    code  line\n\tmore\t code\n\n\n    after  blank\n\nnext para",
		},
		{
			"indented code at the top",
			"    x  =  1\n    y  =  2\nz  =  3",
			"    x  =  1\n    y  =  2\nz = 3",
		},
		{
			"indented code after a heading",
			"# Title  \n    x  =  1",
			"# Title\n    x  =  1",
		},
		{
			"an indented line continuing a paragraph is prose",
			"Para\n    continued   here",
			"Para\ncontinued here",
		},
		{
			"four spaces before a fence make code, not a fence",
			"a\n\n    ```\n    x  y\n\nb  c",
			"a\n\n    ```\n    x  y\n\nb c",
		},
		{
			"a non-breaking space in code is the author's",
			"```\na" + str(0x00A0) + "b\n```",
			"```\na" + str(0x00A0) + "b\n```",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := Space(c.in); got != c.want {
				t.Errorf("Space(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
			if got := Space(Space(c.in)); got != Space(c.in) {
				t.Errorf("Space is not idempotent: %q then %q", Space(c.in), got)
			}
		})
	}
}

func TestFlat(t *testing.T) {
	if got, want := Flat("  a  b\n\nc\t d  "), "a b c d"; got != want {
		t.Errorf("Flat = %q, want %q", got, want)
	}
}

func TestQuotes(t *testing.T) {
	if got, want := Quotes("“a’b”—c…d–e"), `"a'b"--c...d-e`; got != want {
		t.Errorf("Quotes = %q, want %q", got, want)
	}
}

func TestTitle(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"hello WORLD", "Hello World"},
		{"o'brien", "O'Brien"}, // an apostrophe ends a word
		{"", ""},
	} {
		if got := Title(c.in); got != c.want {
			t.Errorf("Title(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTags(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"elements leave a space behind", "<p>a</p><p>b</p>", " a  b "},
		{"script goes with its contents", "a<script>x=1</script>b", "a b"},
		{"style goes with its contents", "a<style>p{}</style>b", "a b"},
		{"comments go", "a<!-- note -->b", "a b"},
		{"references decode", "plain &amp; simple &lt;", "plain & simple <"},
		{"no markup, still decoded", "&amp;", "&"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := Tags(c.in); got != c.want {
				t.Errorf("Tags(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// Text keeps the shape of prose; Clean flattens it and strips markup. They are
// the two chains worth having by default, and they differ in exactly that.
func TestChains(t *testing.T) {
	in := "<p>a</p>  “b”\r\n\r\n\r\nc  "
	if got, want := Text(in), "<p>a</p> \"b\"\n\nc"; got != want {
		t.Errorf("Text = %q, want %q", got, want)
	}
	if got, want := Clean(in), `a "b" c`; got != want {
		t.Errorf("Clean = %q, want %q", got, want)
	}
	// A chain is a value: a caller assembles the one its corpus needs.
	if got, want := (Chain{Lower, Flat}).Run("  A  B "), "a b"; got != want {
		t.Errorf("Chain = %q, want %q", got, want)
	}
	if got, want := (Chain{}).Run("unchanged"), "unchanged"; got != want {
		t.Errorf("the zero Chain changed its input: %q", got)
	}
}

// Repeats finds running heads and page numbers by what recurs, not by where it
// sits, so a footer moves with the text and is still found.
func TestRepeats(t *testing.T) {
	pages := []string{
		"ACME Report\nPage 1 of 3\nbody one",
		"ACME Report\nPage 2 of 3\nbody two",
		"ACME Report\nPage 3 of 3\nbody three",
	}
	want := []string{"body one", "body two", "body three"}
	if got := Repeats(pages, 0); !reflect.DeepEqual(got, want) {
		t.Errorf("Repeats = %q, want %q", got, want)
	}
	// A line has to recur on enough pages. At a share of 1 every page must
	// carry it, and "body one" never recurs at all.
	if got := Repeats(pages, 1); !reflect.DeepEqual(got, want) {
		t.Errorf("Repeats at share 1 = %q, want %q", got, want)
	}
	// One page has nothing to recur against, so nothing is furniture.
	if got := Repeats([]string{"a\nb"}, 0); !reflect.DeepEqual(got, []string{"a\nb"}) {
		t.Errorf("Repeats over one page = %q", got)
	}
}

func TestBOM(t *testing.T) {
	for _, c := range []struct {
		name string
		b    []byte
		want int
	}{
		{"utf-8", []byte{0xEF, 0xBB, 0xBF, 'a'}, 3},
		{"utf-16le", []byte{0xFF, 0xFE, 'a', 0}, 2},
		{"utf-16be", []byte{0xFE, 0xFF, 0, 'a'}, 2},
		{"none", []byte("a"), 0},
		{"empty", nil, 0},
	} {
		if got := BOM(c.b); got != c.want {
			t.Errorf("BOM(%s) = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestGuess(t *testing.T) {
	for _, c := range []struct {
		name string
		b    []byte
		enc  string
		conf float64
	}{
		{"a mark settles it", []byte{0xEF, 0xBB, 0xBF, 'a'}, UTF8, 1},
		{"utf-16le mark", []byte{0xFF, 0xFE, 'a', 0}, UTF16LE, 1},
		{"utf-16be mark", []byte{0xFE, 0xFF, 0, 'a'}, UTF16BE, 1},
		{"ascii is utf-8, certainly", []byte("hello"), UTF8, 1},
		{"valid utf-8 with accents", []byte("café"), UTF8, 0.95},
		{"a byte utf-8 cannot hold", []byte{'C', 'a', 'f', 0xE9}, Latin1, 0.6},
		{"the windows-1252 range in use", []byte{'a', 0x92, 'b'}, CP1252, 0.6},
		{"nothing to go on", nil, UTF8, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			enc, conf := Guess(c.b)
			if enc != c.enc || conf != c.conf {
				t.Errorf("Guess = (%q, %v), want (%q, %v)", enc, conf, c.enc, c.conf)
			}
		})
	}
}

func TestDecode(t *testing.T) {
	for _, c := range []struct {
		name, want string
		b          []byte
	}{
		{name: "mark removed", b: []byte{0xEF, 0xBB, 0xBF, 'a'}, want: "a"},
		{name: "utf-16le", b: []byte{0xFF, 0xFE, 'a', 0}, want: "a"},
		{name: "utf-16be", b: []byte{0xFE, 0xFF, 0, 'a'}, want: "a"},
		{name: "utf-8", b: []byte("café"), want: "café"},
		{name: "latin-1", b: []byte{'C', 'a', 'f', 0xE9}, want: "Café"},
		{name: "windows-1252", b: []byte{'a', 0x92, 'b'}, want: "a’b"},
		{name: "empty", b: nil, want: ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := Decode(c.b); got != c.want {
				t.Errorf("Decode = %q, want %q", got, c.want)
			}
		})
	}
}

func TestDecodeAs(t *testing.T) {
	// The 0xE9 byte is é in Latin-1 and ’ in windows-1252's own range only
	// above 0x7F; naming the encoding is what settles it.
	if got, want := DecodeAs([]byte{0xE9}, Latin1), "é"; got != want {
		t.Errorf("latin-1 = %q, want %q", got, want)
	}
	if got, want := DecodeAs([]byte{0x92}, CP1252), "’"; got != want {
		t.Errorf("windows-1252 = %q, want %q", got, want)
	}
	// Spellings a Content-Type header actually uses all resolve.
	for _, name := range []string{"UTF-8", "utf8", "iso-8859-1", "latin1", "cp1252"} {
		if got := DecodeAs([]byte("ok"), name); got != "ok" {
			t.Errorf("DecodeAs(%q) = %q", name, got)
		}
	}
	// A name nobody has heard of falls back to the guess rather than failing.
	if got := DecodeAs([]byte("ok"), "klingon"); got != "ok" {
		t.Errorf("unknown encoding = %q, want the guess", got)
	}
	// A byte that cannot be decoded is replaced, never fatal.
	if got := DecodeAs([]byte{0xFF, 'a'}, UTF8); got != "�a" {
		t.Errorf("bad byte = %q, want a replacement character", got)
	}
}

// Repair undoes mojibake and leaves everything else alone. Leaving it alone is
// the harder half: a repair that fires on undamaged text is worse than none.
func TestRepair(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"utf-8 read as latin-1", "cafÃ©", "café"},
		{"mangled twice", "cafÃƒÂ©", "café"},
		{"a smart quote that went through windows-1252", "theyâ€™re", "they’re"},
		{"undamaged text is untouched", "café", "café"},
		{"ascii is untouched", "plain", "plain"},
		{"empty", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := Repair(c.in); got != c.want {
				t.Errorf("Repair(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
