package export

import (
	"bytes"
	"context"
	"encoding/xml"
	"strings"
	"testing"

	"github.com/hanzoai/semantic"
)

func TestGraphML(t *testing.T) {
	want := `<?xml version="1.0" encoding="UTF-8"?>
<graphml xmlns="http://graphml.graphdrawing.org/xmlns"
         xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
         xsi:schemaLocation="http://graphml.graphdrawing.org/xmlns
         http://graphml.graphdrawing.org/xmlns/1.0/graphml.xsd">
  <key id="label" for="all" attr.name="label" attr.type="string"/>
  <key id="score" for="edge" attr.name="score" attr.type="double"/>
  <graph id="G" edgedefault="directed">
    <node id="Alice"><data key="label">Alice</data></node>
    <node id="Bob"><data key="label">Bob</data></node>
    <edge source="Alice" target="Bob"><data key="label">knows</data><data key="score">0.9</data></edge>
    <node id="Acme Inc."><data key="label">Acme Inc.</data></node>
    <edge source="Bob" target="Acme Inc."><data key="label">works_at</data></edge>
    <node id="https://example.org/carol"><data key="label">https://example.org/carol</data></node>
    <edge source="Alice" target="https://example.org/carol"><data key="label">http://schema.org/knows</data></edge>
  </graph>
</graphml>
`
	if got := write(t, "graphml"); got != want {
		t.Errorf("graphml wrote\n%s\nwant\n%s", got, want)
	}
}

func TestGEXF(t *testing.T) {
	want := `<?xml version="1.0" encoding="UTF-8"?>
<gexf xmlns="http://www.gexf.net/1.2draft" version="1.2">
  <graph mode="static" defaultedgetype="directed">
    <nodes>
      <node id="Alice" label="Alice"/>
      <node id="Bob" label="Bob"/>
      <node id="Acme Inc." label="Acme Inc."/>
      <node id="https://example.org/carol" label="https://example.org/carol"/>
    </nodes>
    <edges>
      <edge id="0" source="Alice" target="Bob" label="knows" weight="0.9"/>
      <edge id="1" source="Bob" target="Acme Inc." label="works_at"/>
      <edge id="2" source="Alice" target="https://example.org/carol" label="http://schema.org/knows"/>
    </edges>
  </graph>
</gexf>
`
	if got := write(t, "gexf"); got != want {
		t.Errorf("gexf wrote\n%s\nwant\n%s", got, want)
	}
}

// GEXF walks the source twice, and says so by walking it twice.
func TestGEXFWalksTwice(t *testing.T) {
	walks := 0
	src := func(ctx context.Context, yield func(semantic.Triple) error) error {
		walks++
		return Triples(graph())(ctx, yield)
	}
	if err := (GEXF{}).Write(context.Background(), new(bytes.Buffer), src); err != nil {
		t.Fatal(err)
	}
	if walks != 2 {
		t.Errorf("gexf walked the source %d times, want 2", walks)
	}
}

func TestDOT(t *testing.T) {
	want := `digraph "G" {
  rankdir=LR;
  "Alice" [label="Alice"];
  "Bob" [label="Bob"];
  "Alice" -> "Bob" [label="knows", score=0.9];
  "Acme Inc." [label="Acme Inc."];
  "Bob" -> "Acme Inc." [label="works_at"];
  "https://example.org/carol" [label="https://example.org/carol"];
  "Alice" -> "https://example.org/carol" [label="http://schema.org/knows"];
}
`
	if got := write(t, "dot"); got != want {
		t.Errorf("dot wrote\n%s\nwant\n%s", got, want)
	}
	var b bytes.Buffer
	if err := (DOT{Name: "kg"}).Write(context.Background(), &b, Triples(graph())); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(b.String(), `digraph "kg" {`) {
		t.Errorf("the graph was not named: %s", b.String())
	}
}

func TestMermaid(t *testing.T) {
	want := `flowchart LR
  n0["Alice"]
  n1["Bob"]
  n0 -->|"knows"| n1
  n2["Acme Inc."]
  n1 -->|"works_at"| n2
  n3["https://example.org/carol"]
  n0 -->|"http://schema.org/knows"| n3
`
	if got := write(t, "mermaid"); got != want {
		t.Errorf("mermaid wrote\n%s\nwant\n%s", got, want)
	}
	var b bytes.Buffer
	if err := (Mermaid{Dir: "TD"}).Write(context.Background(), &b, Triples(graph())); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(b.String(), "flowchart TD\n") {
		t.Errorf("the direction was ignored: %s", b.String())
	}
}

func TestCypher(t *testing.T) {
	want := "CREATE (n0:`Node` {id: 'Alice'});\n" +
		"CREATE (n1:`Node` {id: 'Bob'});\n" +
		"MATCH (a {id: 'Alice'}), (b {id: 'Bob'}) CREATE (a)-[r:`knows` {score: 0.9}]->(b);\n" +
		"CREATE (n2:`Node` {id: 'Acme Inc.'});\n" +
		"MATCH (a {id: 'Bob'}), (b {id: 'Acme Inc.'}) CREATE (a)-[r:`works_at`]->(b);\n" +
		"CREATE (n3:`Node` {id: 'https://example.org/carol'});\n" +
		"MATCH (a {id: 'Alice'}), (b {id: 'https://example.org/carol'}) CREATE (a)-[r:`http://schema.org/knows`]->(b);\n"
	if got := write(t, "cypher"); got != want {
		t.Errorf("cypher wrote\n%s\nwant\n%s", got, want)
	}
	var b bytes.Buffer
	if err := (Cypher{Label: "Thing"}).Write(context.Background(), &b, Triples(graph())); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "(n0:`Thing` {id: 'Alice'})") {
		t.Errorf("the label was ignored:\n%s", b.String())
	}
}

// A term that carries the punctuation of the language is written so that it
// stays a term. The backslash is escaped before the quote: doing it the other
// way round escapes the backslash the quote's own escape added, which is the
// bug in the Python this is ported from.
func TestCypherEscaping(t *testing.T) {
	raw := []semantic.Triple{{Subject: `it's`, Predicate: "a`b", Object: `back\slash`}}
	var b bytes.Buffer
	if err := (Cypher{}).Write(context.Background(), &b, Triples(raw)); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`{id: 'it\'s'}`, "[r:`a``b`", `{id: 'back\\slash'}`} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("output does not hold %s:\n%s", want, b.String())
		}
	}
	if strings.Contains(b.String(), `\\'`) {
		t.Errorf("the quote's escape was itself escaped:\n%s", b.String())
	}
}

func TestDOTEscaping(t *testing.T) {
	raw := []semantic.Triple{{Subject: `say "hi"`, Predicate: `a\b`, Object: "two\nlines"}}
	var b bytes.Buffer
	if err := (DOT{}).Write(context.Background(), &b, Triples(raw)); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"say \"hi\""`, `[label="a\\b"`, `"two\nlines"`} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("output does not hold %s:\n%s", want, b.String())
		}
	}
}

func TestMermaidEscaping(t *testing.T) {
	raw := []semantic.Triple{{Subject: `say "hi"`, Predicate: "a#b", Object: "two\nlines"}}
	var b bytes.Buffer
	if err := (Mermaid{}).Write(context.Background(), &b, Triples(raw)); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`n0["say #quot;hi#quot;"]`, `-->|"a#35;b"|`, `n1["two<br/>lines"]`} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("output does not hold %s:\n%s", want, b.String())
		}
	}
}

// The XML formats escape what would otherwise close an attribute or open an
// element, and the result parses.
func TestXMLEscaping(t *testing.T) {
	raw := []semantic.Triple{{Subject: `a"<b>&`, Predicate: "knows", Object: "c'd"}}
	for _, f := range []Format{GraphML{}, GEXF{}} {
		var b bytes.Buffer
		if err := f.Write(context.Background(), &b, Triples(raw)); err != nil {
			t.Fatalf("%T: %v", f, err)
		}
		if strings.Contains(b.String(), `id="a"<b>&"`) {
			t.Errorf("%T left the markup unescaped:\n%s", f, b.String())
		}
		if err := xml.Unmarshal(b.Bytes(), new(struct{})); err != nil {
			t.Errorf("%T wrote XML that does not parse: %v\n%s", f, err, b.String())
		}
	}
	var b bytes.Buffer
	if err := (RDFXML{}).Write(context.Background(), &b, Triples(raw)); err != nil {
		t.Fatal(err)
	}
	if err := xml.Unmarshal(b.Bytes(), new(struct{})); err != nil {
		t.Errorf("rdfxml does not parse: %v\n%s", err, b.String())
	}
}

// A node named by several assertions is declared once, which is what keeps a
// node format costing the graph's nodes rather than its edges.
func TestNodeDeclaredOnce(t *testing.T) {
	raw := []semantic.Triple{
		{Subject: "a", Predicate: "p", Object: "b"},
		{Subject: "a", Predicate: "q", Object: "b"},
		{Subject: "b", Predicate: "r", Object: "a"},
	}
	for _, c := range []struct {
		format string
		node   string
	}{
		{"graphml", `<node id="a">`},
		{"gexf", `<node id="a" `},
		{"dot", `"a" [label="a"];`},
		{"mermaid", `n0["a"]`},
		{"cypher", "CREATE (n0:`Node` {id: 'a'});"},
	} {
		var b bytes.Buffer
		if err := Default.Write(context.Background(), &b, Triples(raw), c.format); err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(b.String(), c.node); n != 1 {
			t.Errorf("%s declared node a %d times:\n%s", c.format, n, b.String())
		}
	}
}
