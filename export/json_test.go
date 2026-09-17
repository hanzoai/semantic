package export

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/hanzoai/semantic"
)

func TestJSON(t *testing.T) {
	want := "[\n" +
		`{"subject":"Alice","predicate":"knows","object":"Bob","score":0.9,"doc":"d1","chunk":2,"text":"Alice knows Bob."}` + ",\n" +
		`{"subject":"Bob","predicate":"works_at","object":"Acme Inc.","doc":"d1","chunk":3,"text":"Bob works at Acme Inc."}` + ",\n" +
		`{"subject":"Alice","predicate":"http://schema.org/knows","object":"https://example.org/carol"}` + "\n" +
		"]\n"
	if got := write(t, "json"); got != want {
		t.Errorf("json wrote\n%s\nwant\n%s", got, want)
	}
}

func TestJSONEmpty(t *testing.T) {
	var b bytes.Buffer
	if err := (JSON{}).Write(context.Background(), &b, Triples(nil)); err != nil {
		t.Fatal(err)
	}
	if b.String() != "[]\n" {
		t.Errorf("an empty graph wrote %q, want %q", b.String(), "[]\n")
	}
	if got := collect(t, JSON{}.Read(&b)); got != nil {
		t.Errorf("reading it back gave %v", got)
	}
}

func TestJSONIndent(t *testing.T) {
	var b bytes.Buffer
	if err := (JSON{Indent: "  "}).Write(context.Background(), &b, Triples(graph())); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(b.Bytes()) {
		t.Fatalf("indented json does not parse:\n%s", b.String())
	}
	if !strings.Contains(b.String(), "\n    \"subject\": \"Alice\",\n") {
		t.Errorf("the assertions are not indented:\n%s", b.String())
	}
	if got := collect(t, JSON{}.Read(&b)); !reflect.DeepEqual(got, graph()) {
		t.Errorf("indented json read back as %v", got)
	}
}

// json and ndjson carry every field, so what is read is what was written,
// confidence and source span included.
func TestJSONRoundTrip(t *testing.T) {
	for _, format := range []string{"json", "ndjson"} {
		var b bytes.Buffer
		if err := Default.Write(context.Background(), &b, Triples(graph()), format); err != nil {
			t.Fatal(err)
		}
		if got := collect(t, Default.Read(&b, format)); !reflect.DeepEqual(got, graph()) {
			t.Errorf("%s round trip gave\n%v\nwant\n%v", format, got, graph())
		}
	}
}

func TestNDJSON(t *testing.T) {
	want := `{"subject":"Alice","predicate":"knows","object":"Bob","score":0.9,"doc":"d1","chunk":2,"text":"Alice knows Bob."}` + "\n" +
		`{"subject":"Bob","predicate":"works_at","object":"Acme Inc.","doc":"d1","chunk":3,"text":"Bob works at Acme Inc."}` + "\n" +
		`{"subject":"Alice","predicate":"http://schema.org/knows","object":"https://example.org/carol"}` + "\n"
	if got := write(t, "ndjson"); got != want {
		t.Errorf("ndjson wrote\n%s\nwant\n%s", got, want)
	}
	// A blank line is not a record.
	got := collect(t, NDJSON{}.Read(strings.NewReader("\n"+want+"\n  \n")))
	if !reflect.DeepEqual(got, graph()) {
		t.Errorf("blank lines changed the graph: %v", got)
	}
	// A line that is not JSON says which line.
	err := NDJSON{}.Read(strings.NewReader("{}\nnot json\n"))(context.Background(), func(semantic.Triple) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Errorf("a broken line gave %v, want it to name line 2", err)
	}
}

// These bytes are data, not a page, so the characters Go escapes for HTML by
// default are written as themselves and read back unchanged.
func TestJSONKeepsMarkup(t *testing.T) {
	raw := []semantic.Triple{{Subject: `a<b>&"c"`, Predicate: "p", Object: "o"}}
	var b bytes.Buffer
	if err := (NDJSON{}).Write(context.Background(), &b, Triples(raw)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `"a<b>&\"c\""`) {
		t.Errorf("markup was escaped away: %s", b.String())
	}
	if got := collect(t, NDJSON{}.Read(&b)); !reflect.DeepEqual(got, raw) {
		t.Errorf("markup round trip gave %v", got)
	}
}

func TestJSONLD(t *testing.T) {
	var doc struct {
		Context map[string]string `json:"@context"`
		Graph   []map[string]any  `json:"@graph"`
	}
	out := write(t, "jsonld")
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("jsonld does not parse: %v\n%s", err, out)
	}
	if doc.Context["sem"] != Base {
		t.Errorf("@context sem = %q, want %q", doc.Context["sem"], Base)
	}
	if len(doc.Graph) != 3 {
		t.Fatalf("@graph holds %d nodes, want 3", len(doc.Graph))
	}
	for i, want := range []struct{ id, pred, obj string }{
		{"sem:Alice", "sem:knows", "sem:Bob"},
		{"sem:Bob", "sem:works_at", Base + "Acme%20Inc."},
		{"sem:Alice", "http://schema.org/knows", "https://example.org/carol"},
	} {
		node := doc.Graph[i]
		if node["@id"] != want.id {
			t.Errorf("node %d @id = %v, want %s", i, node["@id"], want.id)
		}
		obj, ok := node[want.pred].(map[string]any)
		if !ok {
			t.Fatalf("node %d has no %s: %v", i, want.pred, node)
		}
		if obj["@id"] != want.obj {
			t.Errorf("node %d object = %v, want %s", i, obj["@id"], want.obj)
		}
	}
}

func TestJSONLDEmpty(t *testing.T) {
	var b bytes.Buffer
	if err := (JSONLD{}).Write(context.Background(), &b, Triples(nil)); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(b.Bytes()) {
		t.Errorf("an empty jsonld document does not parse:\n%s", b.String())
	}
}

// A JSON array that is not one, or holds something that is not an assertion,
// is reported rather than read as an empty graph.
func TestJSONReadRejects(t *testing.T) {
	for _, in := range []string{`{"subject":"a"}`, `[1,2]`, `[{"subject":`} {
		err := JSON{}.Read(strings.NewReader(in))(context.Background(), func(semantic.Triple) error { return nil })
		if err == nil {
			t.Errorf("%q was accepted", in)
		}
	}
}
