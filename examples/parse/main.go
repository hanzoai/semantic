// Command parse reads seven formats — markdown, HTML, JSON, CSV, XML, email and
// a Word document — and shows what each one recovers beyond the text: the
// sections a splitter can cut on, the links, the document's own metadata, and
// the decoded value for the formats that have one.
//
// Parsing never opens a file. Turning a source into a document is ingest's job;
// parse reads Doc.Text and returns a document with a copy of Meta, so the
// origin the reader recorded survives.
//
//	go run ./examples/parse
package main

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/hanzoai/semantic"
	"github.com/hanzoai/semantic/parse"
)

func main() {
	ctx := context.Background()

	markdown(ctx)
	html(ctx)
	structured(ctx)
	mail(ctx)
	word(ctx)
	detect(ctx)
	missing(ctx)
}

// markdown keeps the source as the author wrote it and reports the structure
// the source declares. A heading inside a fenced code block is code, not
// structure, and the last section here holds that down.
func markdown(ctx context.Context) {
	const src = "---\ntitle: Field notes\nauthor: Ada Lovelace\n---\n\n" +
		"# Field notes\n\nBabbage Engines was founded by [Charles Babbage](https://example.org/cb).\n\n" +
		"## Location\n\nHeadquartered in Montréal.\n\n" +
		"## Code\n\n```\n# not a heading\n```\n"

	d := say(ctx, parse.Markdown{}, semantic.Doc{ID: "notes", Source: "notes.md", Text: src})
	fmt.Println("markdown")
	fmt.Printf("  title    %q\n", parse.Title(d))
	fmt.Printf("  tags     %v\n", parse.Tags(d))
	for _, s := range parse.Sections(d) {
		fmt.Printf("  section  h%d %-10q %d..%d\n", s.Level, s.Title, s.Start, s.End)
	}
	for _, l := range parse.Links(d) {
		fmt.Printf("  link     %q → %s\n", l.Text, l.URL)
	}
}

// html keeps what the markup meant and drops what it was for. Script and style
// content is not prose and is not read as prose.
func html(ctx context.Context) {
	const src = `<html><head><title>Babbage Engines</title>` +
		`<meta name="description" content="An engine works"></head>` +
		`<body><script>window.x=1</script><h1>About</h1>` +
		`<p>Founded by <a href="/people/cb">Charles Babbage</a>.</p>` +
		`<img src="/img/mill.png" alt="the mill"></body></html>`

	d := say(ctx, parse.HTML{Base: "https://example.org"}, semantic.Doc{ID: "about", Source: "about.html", Text: src})
	fmt.Println("\nhtml")
	fmt.Printf("  title    %q\n", parse.Title(d))
	fmt.Printf("  tags     %v\n", parse.Tags(d))
	fmt.Printf("  text     %q\n", oneline(d.Text))
	fmt.Printf("  script kept out of the text: %v\n", !strings.Contains(d.Text, "window.x"))
	for _, l := range parse.Links(d) {
		fmt.Printf("  link     %q → %s\n", l.Text, l.URL)
	}
	for _, l := range parse.Images(d) {
		fmt.Printf("  image    %q → %s\n", l.Text, l.URL)
	}
}

// structured shows the three formats that decode to a value. A grid of bare
// cells says nothing about what the cells mean, so each one is also rendered
// as "name: value" lines — which is what gives a splitter and an extractor
// something to read.
func structured(ctx context.Context) {
	fmt.Println("\njson")
	d := say(ctx, parse.JSON{}, semantic.Doc{ID: "co", Source: "co.json", Text: `
		{"company":{"name":"Babbage Engines","founded":1843,"sites":["London","Montréal"]}}`})
	fmt.Printf("  text     %s\n", oneline(d.Text))
	fmt.Printf("  leaves   %v\n", parse.Flatten(parse.Data(d)))

	fmt.Println("\ncsv")
	d = say(ctx, parse.CSV{}, semantic.Doc{ID: "roster", Source: "roster.csv",
		Text: "name,role,city\nAda Lovelace,analyst,Montréal\nCharles Babbage,founder,London\n"})
	rows, _ := parse.Data(d).([]map[string]string)
	fmt.Printf("  fields   %v\n", parse.Fields(d))
	fmt.Printf("  rows     %d, one section each: %d\n", len(rows), len(parse.Sections(d)))
	fmt.Printf("  row 1    %v\n", rows[0])

	fmt.Println("\nxml")
	d = say(ctx, parse.XML{}, semantic.Doc{ID: "cat", Source: "cat.xml", Text: `
		<catalog><engine id="dm"><name>Difference Engine</name></engine>` +
		`<engine id="am"><name>Analytical Engine</name></engine></catalog>`})
	root, _ := parse.Data(d).(*parse.Node)
	fmt.Printf("  root     %s with %d children\n", root.Name, len(root.Nodes))
	for _, n := range root.Find("name") {
		fmt.Printf("  name     %q\n", n.Text)
	}
	fmt.Printf("  text     %s\n", oneline(d.Text))
}

// mail reads a message: the headers become tags with their encoded words
// decoded, and a multipart/alternative is read once, in its plainest part.
func mail(ctx context.Context) {
	const src = "From: ada@example.org\r\n" +
		"To: charles@example.org\r\n" +
		"Subject: =?utf-8?q?Montr=C3=A9al_office?=\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\n" +
		"Babbage Engines is headquartered in Montréal.\r\n"

	d := say(ctx, parse.Email{}, semantic.Doc{ID: "msg", Source: "msg.eml", Text: src})
	fmt.Println("\nemail")
	fmt.Printf("  subject  %q\n", parse.Title(d))
	fmt.Printf("  from     %s → %s\n", parse.Tags(d)["from"], parse.Tags(d)["to"])
	fmt.Printf("  body     %q\n", oneline(d.Text))
}

// word reads a .docx, which is a zip of XML parts: archive/zip and
// encoding/xml are all it takes. Headings become sections at their level and a
// table becomes records named by its first row. The document is built here,
// in memory, the way Word lays one out; ingest hands parse a real one's bytes
// unchanged, so the call is the same.
func word(ctx context.Context) {
	const w = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"`
	p := func(style, text string) string {
		if style != "" {
			style = `<w:pPr><w:pStyle w:val="` + style + `"/></w:pPr>`
		}
		return `<w:p>` + style + `<w:r><w:t>` + text + `</w:t></w:r></w:p>`
	}
	cell := func(text string) string { return `<w:tc>` + p("", text) + `</w:tc>` }
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for _, part := range [][2]string{
		{"_rels/.rels", `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" ` +
			`Target="word/document.xml"/></Relationships>`},
		{"word/document.xml", `<w:document ` + w + `><w:body>` +
			p("Heading1", "Staff") + p("", "Babbage Engines employs two people.") +
			`<w:tbl><w:tr>` + cell("Name") + cell("Role") + `</w:tr>` +
			`<w:tr>` + cell("Ada Lovelace") + cell("analyst") + `</w:tr>` +
			`<w:tr>` + cell("Charles Babbage") + cell("founder") + `</w:tr></w:tbl>` +
			p("Heading2", "Sites") + p("", "London and Montréal.") +
			`</w:body></w:document>`},
	} {
		f, err := z.Create(part[0])
		if err != nil {
			log.Fatal(err)
		}
		if _, err := f.Write([]byte(part[1])); err != nil {
			log.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		log.Fatal(err)
	}

	d := say(ctx, parse.Any{}, semantic.Doc{ID: "staff", Source: "staff.docx", Text: buf.String()})
	fmt.Println("\ndocx")
	fmt.Printf("  format   %v, from %d zipped bytes\n", d.Meta["format"], buf.Len())
	for _, s := range parse.Sections(d) {
		fmt.Printf("  section  h%d %-10q %q\n", s.Level, s.Title, oneline(s.Text(d)))
	}
}

// detect is the one parser a pipeline over a mixed corpus needs: it takes the
// format from what the ingester recorded, and otherwise from the source's
// extension and the shape of the text.
func detect(ctx context.Context) {
	fmt.Println("\ndetect")
	for _, d := range []semantic.Doc{
		{Source: "notes.md", Text: "# Notes\n"},
		{Source: "page.html", Text: "<p>hello</p>"},
		{Source: "unnamed", Text: `{"a":1}`},
		{Source: "unnamed", Text: "a,b\n1,2\n"},
		{Source: "unnamed", Text: "just prose"},
	} {
		got := say(ctx, parse.Any{}, d)
		fmt.Printf("  %-14q → %-8s (Detect says %s)\n", oneline(d.Text), got.Meta["format"], parse.Detect(d))
	}
	fmt.Printf("  registered: %v\n", parse.Names())
}

// missing is the seam for a format this package names but does not read. pdf
// and the legacy binary Office formats — doc, xls, ppt — are registered as
// placeholders that report ErrFormat, so a caller can tell "this build cannot"
// from "nobody has heard of it" — and supplying a reader is one Register call.
func missing(ctx context.Context) {
	fmt.Println("\nformats without a decoder")
	d := semantic.Doc{ID: "report", Source: "report.pdf", Text: "%PDF-1.7"}
	if _, err := (parse.Any{}).Parse(ctx, d); errors.Is(err, parse.ErrFormat) {
		fmt.Printf("  report.pdf → %v\n", err)
	}

	parse.Register("pdf", pages{})
	got := say(ctx, parse.Any{}, d)
	fmt.Printf("  after Register(\"pdf\", …) → %q\n", got.Text)
}

// pages stands in for a PDF reader. A real one decodes the file; what matters
// here is that it is registered under the same name and reached by the same
// call as everything above.
type pages struct{}

func (pages) Parse(_ context.Context, d semantic.Doc) (semantic.Doc, error) {
	d.Meta = map[string]any{"format": "pdf", "pages": 1}
	d.Text = "whatever a pdf reader would return"
	return d, nil
}

// say parses a document and stops on an error, since every document here is
// written a few lines above the call.
func say(ctx context.Context, f parse.Format, d semantic.Doc) semantic.Doc {
	got, err := f.Parse(ctx, d)
	if err != nil {
		log.Fatal(err)
	}
	return got
}

// oneline renders text on a single line so a listing stays a listing.
func oneline(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 96 {
		return s[:96] + "…"
	}
	return s
}
