package export

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/hanzoai/semantic"
)

func TestNT(t *testing.T) {
	want := "<https://semantica.dev/ns#Alice> <https://semantica.dev/ns#knows> <https://semantica.dev/ns#Bob> .\n" +
		"<https://semantica.dev/ns#Bob> <https://semantica.dev/ns#works_at> <https://semantica.dev/ns#Acme%20Inc.> .\n" +
		"<https://semantica.dev/ns#Alice> <http://schema.org/knows> <https://example.org/carol> .\n"
	if got := write(t, "nt"); got != want {
		t.Errorf("nt wrote\n%s\nwant\n%s", got, want)
	}
}

func TestTurtle(t *testing.T) {
	want := "@prefix sem: <https://semantica.dev/ns#> .\n\n" +
		"sem:Alice sem:knows sem:Bob .\n" +
		"sem:Bob sem:works_at <https://semantica.dev/ns#Acme%20Inc.> .\n" +
		"sem:Alice <http://schema.org/knows> <https://example.org/carol> .\n"
	if got := write(t, "ttl"); got != want {
		t.Errorf("ttl wrote\n%s\nwant\n%s", got, want)
	}
}

func TestRDFXML(t *testing.T) {
	want := `<?xml version="1.0" encoding="UTF-8"?>
<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
  <rdf:Description rdf:about="https://semantica.dev/ns#Alice">
    <p:knows xmlns:p="https://semantica.dev/ns#" rdf:resource="https://semantica.dev/ns#Bob"/>
  </rdf:Description>
  <rdf:Description rdf:about="https://semantica.dev/ns#Bob">
    <p:works_at xmlns:p="https://semantica.dev/ns#" rdf:resource="https://semantica.dev/ns#Acme%20Inc."/>
  </rdf:Description>
  <rdf:Description rdf:about="https://semantica.dev/ns#Alice">
    <p:knows xmlns:p="http://schema.org/" rdf:resource="https://example.org/carol"/>
  </rdf:Description>
</rdf:RDF>
`
	if got := write(t, "rdfxml"); got != want {
		t.Errorf("rdfxml wrote\n%s\nwant\n%s", got, want)
	}
}

// A property element needs an XML name, and a predicate whose IRI does not end
// in one has none. RDF/XML is the only format here with that limit, and it
// says so rather than dropping the assertion.
func TestRDFXMLNamelessPredicate(t *testing.T) {
	for _, p := range []string{"works at", "1st", "a/b c"} {
		src := Triples([]semantic.Triple{{Subject: "a", Predicate: p, Object: "b"}})
		err := RDFXML{}.Write(context.Background(), new(bytes.Buffer), src)
		if !errors.Is(err, ErrName) {
			t.Errorf("predicate %q gave %v, want ErrName", p, err)
		}
		if err != nil && !strings.Contains(err.Error(), p) {
			t.Errorf("error %q does not name the predicate %q", err, p)
		}
	}
	// The other formats write it without complaint.
	src := Triples([]semantic.Triple{{Subject: "a", Predicate: "works at", Object: "b"}})
	for _, format := range []string{"nt", "ttl", "jsonld", "graphml", "dot", "cypher"} {
		if err := Default.Write(context.Background(), new(bytes.Buffer), src, format); err != nil {
			t.Errorf("%s with a spaced predicate = %v", format, err)
		}
	}
}

func TestIRI(t *testing.T) {
	const b = "https://ex.org/ns#"
	for _, c := range []struct{ term, want string }{
		{"Alice", b + "Alice"},
		{"Ada Lovelace", b + "Ada%20Lovelace"},
		{"a/b", b + "a%2Fb"},
		{"", b},
		{"héllo", b + "h%C3%A9llo"},
		{"100%", b + "100%25"},
		// Already an IRI: kept, whatever the scheme.
		{"http://x.example/a", "http://x.example/a"},
		{"urn:isbn:0451450523", "urn:isbn:0451450523"},
		{"mailto:a@b.example", "mailto:a@b.example"},
		// One letter before the colon is a drive, not a scheme.
		{"c:\\graphs", b + "c%3A%5Cgraphs"},
		// A minted IRI keeps its escapes rather than gaining more.
		{"http://x.example/a%20b", "http://x.example/a%20b"},
		// What an IRI reference may not hold is encoded, so a term cannot
		// close the reference and have the rest read as further RDF.
		{"http://x.example/a>b", "http://x.example/a%3Eb"},
		{"http://x.example/a b", "http://x.example/a%20b"},
	} {
		if got := iri(b, c.term); got != c.want {
			t.Errorf("iri(%q) = %q, want %q", c.term, got, c.want)
		}
	}
}

// name is the inverse of iri for everything iri could have been given.
func TestName(t *testing.T) {
	const b = "https://ex.org/ns#"
	for _, term := range []string{"Alice", "Ada Lovelace", "a/b", "héllo", "100%", "http://x.example/a", "urn:isbn:1"} {
		if got := name(b, iri(b, term)); got != term {
			t.Errorf("name(iri(%q)) = %q", term, got)
		}
	}
	// An IRI already in the namespace comes back as its local part, which
	// mints to the IRI it came from.
	if got := name(b, b+"Alice"); got != "Alice" {
		t.Errorf("name of a term in the namespace = %q, want Alice", got)
	}
}

func TestShort(t *testing.T) {
	const b = "https://ex.org/ns#"
	for _, c := range []struct{ term, want string }{
		{b + "Alice", "Alice"},
		{b + "works_at", "works_at"},
		{b + "a-b.c", "a-b.c"},
		{b + "0th", "0th"},
		{b + "a%20b", ""},  // an escape is not a name
		{b + "Acme.", ""},  // a trailing period would end the statement
		{b + "-lead", ""},  // a name does not open with a hyphen
		{b, ""},            // nothing follows the namespace
		{"http://x/a", ""}, // a different namespace
		{b + "a b", ""},    // a space is not a name
	} {
		if got := short(b, c.term); got != c.want {
			t.Errorf("short(%q) = %q, want %q", c.term, got, c.want)
		}
	}
}

// Writing N-Triples and reading them back gives the assertions that were
// written. A triple has nowhere to carry a confidence or a source span, so
// those do not survive, and the test says which fields it is claiming.
func TestNTRoundTrip(t *testing.T) {
	var b bytes.Buffer
	if err := (NT{}).Write(context.Background(), &b, Triples(graph())); err != nil {
		t.Fatal(err)
	}
	got := collect(t, NT{}.Read(&b))
	var want []semantic.Triple
	for _, tr := range graph() {
		want = append(want, semantic.Triple{Subject: tr.Subject, Predicate: tr.Predicate, Object: tr.Object})
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("nt round trip gave\n%v\nwant\n%v", got, want)
	}
}

// Whatever the terms were, writing what was read writes the same document, so
// a graph converted through N-Triples does not drift on each pass.
func TestNTFixedPoint(t *testing.T) {
	ctx := context.Background()
	odd := append(graph(), semantic.Triple{
		Subject:   "https://semantica.dev/ns#Alice", // already in the namespace
		Predicate: "http://x.example/a b",           // an IRI needing an escape
		Object:    "100%",
	})
	var first bytes.Buffer
	if err := (NT{}).Write(ctx, &first, Triples(odd)); err != nil {
		t.Fatal(err)
	}
	var second bytes.Buffer
	if err := (NT{}).Write(ctx, &second, NT{}.Read(bytes.NewReader(first.Bytes()))); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() {
		t.Errorf("writing what was read gave\n%s\nwant\n%s", second.String(), first.String())
	}
}

func TestNTReadRejects(t *testing.T) {
	for _, c := range []struct{ line, why string }{
		{`<a> <b> "Bob" .`, "a literal object"},
		{`<a> <b> <c>`, "no closing period"},
		{`<a> <b> <c .`, "no closing angle"},
		{`a b c .`, "bare terms"},
		{`<a> <b> <c> <d> .`, "a fourth term"},
	} {
		err := NT{}.Read(strings.NewReader(c.line))(context.Background(), func(semantic.Triple) error { return nil })
		if err == nil {
			t.Errorf("%s was accepted: %s", c.why, c.line)
		}
	}
	// Blank lines and comments are not statements and are not errors.
	got := collect(t, NT{}.Read(strings.NewReader("# a note\n\n<http://a> <http://b> <http://c> .\n\n")))
	if len(got) != 1 {
		t.Errorf("a file with a comment gave %d assertions, want 1", len(got))
	}
}

// A writer minting into its own namespace reads back through the same one.
func TestBase(t *testing.T) {
	const b = "http://x.example/"
	var buf bytes.Buffer
	src := Triples([]semantic.Triple{{Subject: "Alice", Predicate: "knows", Object: "Bob"}})
	if err := (NT{Base: b}).Write(context.Background(), &buf, src); err != nil {
		t.Fatal(err)
	}
	if want := "<http://x.example/Alice> <http://x.example/knows> <http://x.example/Bob> .\n"; buf.String() != want {
		t.Errorf("wrote %q, want %q", buf.String(), want)
	}
	got := collect(t, NT{Base: b}.Read(strings.NewReader(buf.String())))
	if len(got) != 1 || got[0].Subject != "Alice" {
		t.Errorf("read back %v", got)
	}
	// Read against the default namespace, the terms stay the IRIs they are.
	got = collect(t, NT{}.Read(strings.NewReader(buf.String())))
	if len(got) != 1 || got[0].Subject != b+"Alice" {
		t.Errorf("read against another namespace gave %v", got)
	}
}
