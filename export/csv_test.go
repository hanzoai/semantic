package export

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/hanzoai/semantic"
)

func TestCSV(t *testing.T) {
	want := "subject,predicate,object,score,doc,chunk,text\n" +
		"Alice,knows,Bob,0.9,d1,2,Alice knows Bob.\n" +
		"Bob,works_at,Acme Inc.,0,d1,3,Bob works at Acme Inc.\n" +
		"Alice,http://schema.org/knows,https://example.org/carol,0,,0,\n"
	if got := write(t, "csv"); got != want {
		t.Errorf("csv wrote\n%s\nwant\n%s", got, want)
	}
	if got := write(t, "tsv"); got != strings.ReplaceAll(want, ",", "\t") {
		t.Errorf("tsv wrote\n%s", got)
	}
}

// A table carries every field, so what is read is what was written.
func TestCSVRoundTrip(t *testing.T) {
	for _, format := range []string{"csv", "tsv"} {
		var b bytes.Buffer
		if err := Default.Write(context.Background(), &b, Triples(graph()), format); err != nil {
			t.Fatal(err)
		}
		if got := collect(t, Default.Read(&b, format)); !reflect.DeepEqual(got, graph()) {
			t.Errorf("%s round trip gave\n%v\nwant\n%v", format, got, graph())
		}
	}
}

// The header says which column is which, so a table whose columns were moved
// or dropped still reads as the assertions it holds.
func TestCSVHeaderNamesTheColumns(t *testing.T) {
	in := "object,subject,predicate\nBob,Alice,knows\n"
	want := []semantic.Triple{{Subject: "Alice", Predicate: "knows", Object: "Bob"}}
	if got := collect(t, CSV{}.Read(strings.NewReader(in))); !reflect.DeepEqual(got, want) {
		t.Errorf("a reordered table read as %v", got)
	}
	// A column that is not one of ours is ignored rather than misread.
	in = "subject,colour,predicate,object\nAlice,red,knows,Bob\n"
	if got := collect(t, CSV{}.Read(strings.NewReader(in))); !reflect.DeepEqual(got, want) {
		t.Errorf("a table with a stray column read as %v", got)
	}
	// A table with no subject column is not this table.
	err := CSV{}.Read(strings.NewReader("a,b\n1,2\n"))(context.Background(), func(semantic.Triple) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "subject") {
		t.Errorf("a table with no subject column gave %v", err)
	}
}

// Rows without a header keep the written column order, which is what makes
// them appendable to a table that already has one.
func TestCSVBare(t *testing.T) {
	var b bytes.Buffer
	if err := (CSV{Bare: true}).Write(context.Background(), &b, Triples(graph())); err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(b.String(), "subject") {
		t.Errorf("a bare table wrote a header:\n%s", b.String())
	}
	if got := collect(t, CSV{Bare: true}.Read(&b)); !reflect.DeepEqual(got, graph()) {
		t.Errorf("bare round trip gave %v", got)
	}
}

// A term holding the separator, a quote or a newline is one term, and comes
// back as one.
func TestCSVQuoting(t *testing.T) {
	raw := []semantic.Triple{{
		Subject:   `Acme, Inc.`,
		Predicate: `said "hello"`,
		Object:    "two\nlines",
		Score:     0.125,
		From:      semantic.Chunk{DocID: "d,1", Index: 7, Text: "a\tb"},
	}}
	var b bytes.Buffer
	if err := (CSV{}).Write(context.Background(), &b, Triples(raw)); err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(b.String(), "\n"); lines != 3 {
		t.Errorf("the row was not quoted as one record:\n%s", b.String())
	}
	if got := collect(t, CSV{}.Read(&b)); !reflect.DeepEqual(got, raw) {
		t.Errorf("quoting round trip gave\n%v\nwant\n%v", got, raw)
	}
}

// A cell that should hold a number and does not is reported with its line
// rather than read as zero.
func TestCSVBadNumber(t *testing.T) {
	in := "subject,predicate,object,score\na,b,c,high\n"
	err := CSV{}.Read(strings.NewReader(in))(context.Background(), func(semantic.Triple) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Errorf("a confidence of \"high\" gave %v, want it to name line 2", err)
	}
}

func TestCSVEmpty(t *testing.T) {
	var b bytes.Buffer
	if err := (CSV{}).Write(context.Background(), &b, Triples(nil)); err != nil {
		t.Fatal(err)
	}
	if b.String() != "subject,predicate,object,score,doc,chunk,text\n" {
		t.Errorf("an empty graph wrote %q", b.String())
	}
	if got := collect(t, CSV{}.Read(&b)); got != nil {
		t.Errorf("reading it back gave %v", got)
	}
	if got := collect(t, CSV{}.Read(strings.NewReader(""))); got != nil {
		t.Errorf("reading nothing gave %v", got)
	}
}
