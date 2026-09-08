package normalize

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Encodings this package names. Detection is deliberately shallow: it tells
// UTF-8 and the UTF-16s apart exactly, and otherwise decides between the two
// single-byte encodings that account for nearly all Western legacy text.
const (
	UTF8    = "utf-8"
	UTF16LE = "utf-16le"
	UTF16BE = "utf-16be"
	CP1252  = "windows-1252"
	Latin1  = "iso-8859-1"
)

// BOM reports the length in bytes of the byte order mark at the front of b, or
// zero if there is none. Slicing b past it removes the mark.
func BOM(b []byte) int {
	switch {
	case len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF:
		return 3
	case len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE:
		return 2
	case len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF:
		return 2
	}
	return 0
}

// Guess names the encoding of b and how far to trust the answer, from 0 to 1. A
// byte order mark settles it; otherwise valid UTF-8 is taken as UTF-8, a
// regular pattern of zero bytes as UTF-16, and anything else as one of the
// single-byte encodings, chosen by whether the bytes reserved for C1 controls
// are in use.
func Guess(b []byte) (string, float64) {
	if len(b) == 0 {
		return UTF8, 0
	}
	switch n := BOM(b); {
	case n == 3:
		return UTF8, 1
	case n == 2 && b[0] == 0xFF:
		return UTF16LE, 1
	case n == 2:
		return UTF16BE, 1
	}
	if utf8.Valid(b) {
		for _, c := range b {
			if c >= 0x80 {
				return UTF8, 0.95
			}
		}
		return UTF8, 1
	}
	if enc, ok := wide(b); ok {
		return enc, 0.8
	}
	for _, c := range b {
		if c >= 0x80 && c <= 0x9F {
			return CP1252, 0.6
		}
	}
	return Latin1, 0.6
}

// wide looks for the alternating zero bytes that mark UTF-16 text without a
// byte order mark. Latin text in UTF-16 is half zeroes; nothing else is.
func wide(b []byte) (string, bool) {
	n := len(b) &^ 1
	if n < 4 {
		return "", false
	}
	var even, odd int
	for i := 0; i < n; i += 2 {
		if b[i] == 0 {
			even++
		}
		if b[i+1] == 0 {
			odd++
		}
	}
	half := n / 4
	switch {
	case odd > half && even == 0:
		return UTF16LE, true
	case even > half && odd == 0:
		return UTF16BE, true
	}
	return "", false
}

// Decode turns bytes into a Go string: byte order mark removed, encoding
// guessed, bytes that cannot be decoded replaced with U+FFFD. It never fails,
// because a pipeline that stops on one bad byte is worse than one that keeps a
// replacement character.
func Decode(b []byte) string {
	enc, _ := Guess(b)
	return DecodeAs(b, enc)
}

// DecodeAs turns bytes into a string using the named encoding, for callers who
// know it from a Content-Type header or an XML declaration. An unknown name
// falls back to the guess.
func DecodeAs(b []byte, enc string) string {
	b = b[BOM(b):]
	switch strings.ToLower(enc) {
	case UTF8, "utf8":
		return strings.ToValidUTF8(string(b), "�")
	case UTF16LE, UTF16BE:
		u := make([]uint16, 0, len(b)/2)
		for i := 0; i+1 < len(b); i += 2 {
			if enc == UTF16LE {
				u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
			} else {
				u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
			}
		}
		return string(utf16.Decode(u))
	case Latin1, "latin-1", "latin1", "iso8859-1":
		return single(b, nil)
	case CP1252, "cp1252", "windows1252":
		return single(b, cp1252[:])
	}
	if guess, _ := Guess(b); !strings.EqualFold(guess, enc) {
		return DecodeAs(b, guess)
	}
	return strings.ToValidUTF8(string(b), "�")
}

// single decodes a single-byte encoding whose upper half is Latin-1 except for
// the codes listed in high, which covers Latin-1 itself when high is nil.
func single(b []byte, high []rune) string {
	var sb strings.Builder
	sb.Grow(len(b))
	for _, c := range b {
		r := rune(c)
		if high != nil && c >= 0x80 && c <= 0x9F {
			r = high[c-0x80]
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// cp1252 is the windows-1252 upper range, the 32 codes where it differs from
// Latin-1. They are the source of most mojibake, because these are exactly the
// bytes a UTF-8 sequence lands on when it is read as legacy text.
var cp1252 = [32]rune{
	'€', '', '‚', 'ƒ', '„', '…', '†', '‡',
	'ˆ', '‰', 'Š', '‹', 'Œ', '', 'Ž', '',
	'', '‘', '’', '“', '”', '•', '–', '—',
	'˜', '™', 'š', '›', 'œ', '', 'ž', 'Ÿ',
}

// Repair undoes mojibake: text that was UTF-8, read as Latin-1 or windows-1252,
// and stored that way, so "café" is sitting in the string as "cafÃ©". It
// re-encodes the characters back to the bytes they came from and reads those as
// UTF-8, and only keeps the result if it decodes cleanly — text that was never
// damaged comes back untouched.
func Repair(s string) string {
	for i := 0; i < 3; i++ {
		fixed, ok := unmangle(s)
		if !ok {
			return s
		}
		s = fixed
	}
	return s
}

func unmangle(s string) (string, bool) {
	if ascii(s) {
		return s, false
	}
	b := make([]byte, 0, len(s))
	suspect := false
	for _, r := range s {
		switch {
		case r < 0x100:
			b = append(b, byte(r))
			if r >= 0xC2 && r <= 0xF4 {
				suspect = true
			}
		default:
			c, ok := reverse[r]
			if !ok {
				return s, false // not representable as legacy bytes: not mojibake
			}
			b = append(b, c)
		}
	}
	if !suspect || !utf8.Valid(b) {
		return s, false
	}
	out := string(b)
	if out == s || strings.ContainsRune(out, utf8.RuneError) {
		return s, false
	}
	return out, true
}

// reverse maps the windows-1252 upper range back to its byte, so a mangled
// string can be turned back into the bytes it was made from.
var reverse = func() map[rune]byte {
	m := make(map[rune]byte, len(cp1252))
	for i, r := range cp1252 {
		m[r] = byte(0x80 + i)
	}
	return m
}()
