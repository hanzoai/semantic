package export

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/hanzoai/semantic"
)

// ns is the namespace to mint into: the one a format was given, or [Base].
func ns(s string) string {
	if s == "" {
		return Base
	}
	return s
}

// iri turns a term into an IRI. A term that carries a scheme is one already
// and only has the characters an IRI reference may not hold encoded; anything
// else is a name, and is minted into base whole, so a space or a slash inside
// a name cannot change what the IRI points at.
func iri(base, term string) string {
	if scheme(term) {
		return safe(term)
	}
	return base + escape(term)
}

// name reads an IRI back as the term it was minted from. A name in base comes
// back decoded; an IRI from anywhere else stands for itself. A term that was
// already an IRI in base comes back as its local part, which mints to the IRI
// it came from, so writing what this returns writes the same document again.
func name(base, term string) string {
	if base != "" && strings.HasPrefix(term, base) {
		if v, err := url.PathUnescape(term[len(base):]); err == nil {
			return v
		}
	}
	return term
}

// scheme reports whether term begins with a URI scheme of two letters or
// more. One letter is left out on purpose: "c:\graphs" is a path.
func scheme(term string) bool {
	i := strings.IndexByte(term, ':')
	if i < 2 {
		return false
	}
	for j := range i {
		c := term[j]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case j > 0 && (c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'):
		default:
			return false
		}
	}
	return true
}

// escape percent-encodes every byte an IRI does not leave alone, which is
// everything outside the unreserved set. A name is encoded whole because its
// punctuation is text, not structure.
func escape(s string) string {
	var b strings.Builder
	for i := range len(s) {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

// safe percent-encodes the characters an IRI reference may not hold between
// its angle brackets. Left unencoded, a ">" in a caller's IRI would close the
// reference early and let the rest of the string be read as further RDF.
func safe(s string) string {
	if !strings.ContainsAny(s, "\x00\x01\x02\x03\x04\x05\x06\a\b\t\n\v\f\r\x0e\x0f"+
		"\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f <>\"{}|^`\\") {
		return s
	}
	var b strings.Builder
	for i := range len(s) {
		c := s[i]
		if c <= 0x20 || strings.IndexByte("<>\"{}|^`\\", c) >= 0 {
			fmt.Fprintf(&b, "%%%02X", c)
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// short returns the part of an IRI that follows base, when that part is a
// name a Turtle or JSON-LD reader splits back off in one piece. It returns
// empty when the IRI has to be written out in full.
func short(base, term string) string {
	if base == "" || !strings.HasPrefix(term, base) {
		return ""
	}
	local := term[len(base):]
	if local == "" || local[len(local)-1] == '.' {
		return ""
	}
	for i := range len(local) {
		c := local[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_':
		case i > 0 && (c == '-' || c == '.'):
		default:
			return ""
		}
	}
	return local
}

// NT writes N-Triples: one <subject> <predicate> <object> . to a line. It is
// the format every RDF tool reads, the cheapest to write, and the only RDF
// format here that reads back, since a line is a whole statement.
//
// A triple has nowhere to put a confidence or the span a claim came from, so
// those are not written. A graph that has to keep them goes out as ndjson.
type NT struct{ Base string }

// Write serializes src to w as N-Triples.
func (n NT) Write(ctx context.Context, w io.Writer, src Source) error {
	b := ns(n.Base)
	p := ink(w)
	i := 0
	err := src(ctx, func(t semantic.Triple) error {
		if p.err != nil {
			return p.err
		}
		if err := whole(t, i); err != nil {
			return err
		}
		i++
		p.put("<", iri(b, t.Subject), "> <", iri(b, t.Predicate), "> <", iri(b, t.Object), "> .\n")
		return p.err
	})
	if err != nil {
		return err
	}
	return p.done()
}

// Read parses N-Triples whose terms are all IRI references, which is what
// [NT.Write] produces. A blank line and a comment are skipped; a literal
// object is reported rather than guessed at, since a literal and a name are
// different claims and this package writes only the second.
func (n NT) Read(r io.Reader) Source {
	b := ns(n.Base)
	return once(func(ctx context.Context, yield func(semantic.Triple) error) error {
		s := bufio.NewScanner(r)
		s.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
		for line := 1; s.Scan(); line++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			text := strings.TrimSpace(s.Text())
			if text == "" || strings.HasPrefix(text, "#") {
				continue
			}
			terms, err := refs(text)
			if err != nil {
				return fmt.Errorf("nt line %d: %w", line, err)
			}
			t := semantic.Triple{
				Subject:   name(b, terms[0]),
				Predicate: name(b, terms[1]),
				Object:    name(b, terms[2]),
			}
			if err := yield(t); err != nil {
				return err
			}
		}
		if err := s.Err(); err != nil {
			return fmt.Errorf("nt: %w", err)
		}
		return nil
	})
}

// refs reads the three IRI references of an N-Triples statement.
func refs(line string) ([3]string, error) {
	var out [3]string
	rest := line
	for i := range out {
		rest = strings.TrimLeft(rest, " \t")
		if !strings.HasPrefix(rest, "<") {
			return out, fmt.Errorf("term %d is not an IRI: %s", i+1, line)
		}
		end := strings.IndexByte(rest, '>')
		if end < 0 {
			return out, fmt.Errorf("term %d has no closing >: %s", i+1, line)
		}
		out[i], rest = rest[1:end], rest[end+1:]
	}
	if strings.TrimSpace(rest) != "." {
		return out, fmt.Errorf("statement does not end in a period: %s", line)
	}
	return out, nil
}

// Turtle writes the same statements as [NT] with a prefix declared for the
// namespace, so a name minted here is written sem:alice rather than in full.
// It is the format to hand a person; nt is the one to hand a program.
type Turtle struct{ Base string }

// Write serializes src to w as Turtle.
func (u Turtle) Write(ctx context.Context, w io.Writer, src Source) error {
	b := ns(u.Base)
	p := ink(w)
	p.put("@prefix sem: <", safe(b), "> .\n\n")
	i := 0
	err := src(ctx, func(t semantic.Triple) error {
		if p.err != nil {
			return p.err
		}
		if err := whole(t, i); err != nil {
			return err
		}
		i++
		p.put(ref(b, t.Subject), " ", ref(b, t.Predicate), " ", ref(b, t.Object), " .\n")
		return p.err
	})
	if err != nil {
		return err
	}
	return p.done()
}

// ref writes a term the way Turtle names it: as a prefixed name where the
// local part allows, and as a full IRI reference where it does not.
func ref(base, term string) string {
	full := iri(base, term)
	if local := short(base, full); local != "" {
		return "sem:" + local
	}
	return "<" + full + ">"
}

// RDFXML writes RDF/XML: an rdf:Description per assertion, carrying the
// predicate as a property element.
//
// One description per assertion rather than one per subject, for the reason
// JSON-LD writes one node per assertion: grouping means holding the graph. The
// namespace of each predicate is declared on the property element itself,
// which is what lets the document be written in one pass.
//
// RDF/XML is the one format here that cannot write every predicate: a property
// element needs an XML name, so a predicate whose IRI does not end in one is
// reported as [ErrName] rather than dropped.
type RDFXML struct{ Base string }

// Write serializes src to w as RDF/XML.
func (x RDFXML) Write(ctx context.Context, w io.Writer, src Source) error {
	b := ns(x.Base)
	p := ink(w)
	p.put("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n",
		"<rdf:RDF xmlns:rdf=\"http://www.w3.org/1999/02/22-rdf-syntax-ns#\">\n")
	i := 0
	err := src(ctx, func(t semantic.Triple) error {
		if p.err != nil {
			return p.err
		}
		if err := whole(t, i); err != nil {
			return err
		}
		space, local := split(iri(b, t.Predicate))
		if local == "" {
			return fmt.Errorf("assertion %d predicate %q: %w", i, t.Predicate, ErrName)
		}
		i++
		p.put("  <rdf:Description rdf:about=\"", tag(iri(b, t.Subject)), "\">\n",
			"    <p:", local, " xmlns:p=\"", tag(space), "\" rdf:resource=\"", tag(iri(b, t.Object)), "\"/>\n",
			"  </rdf:Description>\n")
		return p.err
	})
	if err != nil {
		return err
	}
	p.put("</rdf:RDF>\n")
	return p.done()
}

// split cuts an IRI into the namespace and the XML name a property element
// needs. It returns an empty name when no cut yields one.
func split(term string) (string, string) {
	for _, sep := range []byte{'#', '/'} {
		if i := strings.LastIndexByte(term, sep); i >= 0 && i+1 < len(term) {
			if local := term[i+1:]; xname(local) {
				return term[:i+1], local
			}
		}
	}
	return "", ""
}

// xname reports whether s can be an XML name, checked over ASCII. The rule
// admits more than this, so the check refuses names it could have taken and
// never takes one a parser would reject.
func xname(s string) bool {
	if s == "" {
		return false
	}
	if c := s[0]; !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_') {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '.' || c == '-' || c == '_' {
			continue
		}
		return false
	}
	return true
}

// tag escapes a string for XML, in an attribute as well as in text. The
// quotes matter: an attribute carrying one would close early and leave a
// document no parser reads.
var tags = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")

func tag(s string) string { return tags.Replace(s) }
