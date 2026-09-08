package parse

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/hanzoai/semantic"
)

func doc(source, text string) semantic.Doc {
	return semantic.Doc{ID: "d1", Source: source, Text: text}
}

func read(t *testing.T, f Format, d semantic.Doc) semantic.Doc {
	t.Helper()
	out, err := f.Parse(context.Background(), d)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return out
}

func TestDetect(t *testing.T) {
	cases := []struct {
		name   string
		source string
		text   string
		want   string
	}{
		{"txt", "notes.txt", "hello", "text"},
		{"log", "app.log", "hello", "text"},
		{"md", "readme.md", "hello", "markdown"},
		{"html", "page.html", "hello", "html"},
		{"json", "data.json", "hello", "json"},
		{"jsonl", "data.jsonl", "hello", "jsonl"},
		{"csv", "rows.csv", "hello", "csv"},
		{"tsv", "rows.tsv", "hello", "tsv"},
		{"xml", "doc.xml", "hello", "xml"},
		{"rss", "feed.rss", "hello", "xml"},
		{"eml", "note.eml", "hello", "email"},
		{"pdf", "paper.pdf", "hello", "pdf"},
		{"docx", "memo.docx", "hello", "docx"},
		{"upper", "MEMO.HTML", "hello", "html"},
		{"query", "https://x.example/a.html?q=1#top", "hello", "html"},
		{"sniff json object", "", `{"a": 1}`, "json"},
		{"sniff json array", "", `[1, 2]`, "json"},
		{"sniff html", "", "<html><body>x", "html"},
		{"sniff doctype", "", "<!doctype html><p>x", "html"},
		{"sniff xml", "", `<?xml version="1.0"?><a/>`, "xml"},
		{"sniff xml element", "", "<feed><entry/></feed>", "xml"},
		{"sniff markdown", "", "# Head\n\ntext", "markdown"},
		{"sniff fence", "", "text\n\n```go\nx\n```", "markdown"},
		{"sniff csv", "", "a,b\n1,2\n", "csv"},
		{"sniff semicolons", "", "a;b\n1;2\n", "csv"},
		{"sniff tabs", "", "a\tb\n1\t2\n", "csv"},
		{"sniff email", "", "From: a@b.example\nSubject: hi\n\nbody", "email"},
		{"sniff email crlf", "", "From: a@b.example\r\nSubject: hi\r\n\r\nbody", "email"},
		{"prose is not email", "", "Note: this is prose.\n\nMore prose.", "text"},
		{"prose is not csv", "", "One sentence here.\nAnother one here.\n", "text"},
		{"unknown extension", "data.zzz", "plain words", "text"},
		{"empty", "", "", "text"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Detect(doc(c.source, c.text)); got != c.want {
				t.Fatalf("Detect = %q, want %q", got, c.want)
			}
		})
	}
}

func TestText(t *testing.T) {
	d := read(t, Text{}, doc("a.txt", "one\r\ntwo\rthree"))
	if d.Text != "one\ntwo\nthree" {
		t.Fatalf("Text = %q", d.Text)
	}
	if d.Meta["format"] != "text" {
		t.Fatalf("format = %v", d.Meta["format"])
	}
	if len(Sections(d)) != 0 {
		t.Fatalf("prose has %d sections, want none", len(Sections(d)))
	}
}

// TestCSV covers the cases tests/parse/test_parse_comprehensive.py asserts for
// CSVParser — two rows from a three-line file, addressed by header name — and
// the shapes a real export adds to them.
func TestCSV(t *testing.T) {
	d := read(t, CSV{}, doc("people.csv", "name,age\nAlice,30\nBob,25\n"))

	if got, want := Fields(d), []string{"name", "age"}; !slices.Equal(got, want) {
		t.Errorf("Fields = %v, want %v", got, want)
	}
	rows, ok := Data(d).([]map[string]string)
	if !ok {
		t.Fatalf("Data is %T, want []map[string]string", Data(d))
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 — the header is not one", len(rows))
	}
	if rows[0]["name"] != "Alice" || rows[0]["age"] != "30" {
		t.Errorf("row 0 = %v", rows[0])
	}
	if rows[1]["name"] != "Bob" || rows[1]["age"] != "25" {
		t.Errorf("row 1 = %v", rows[1])
	}
	if want := "name: Alice\nage: 30\nname: Bob\nage: 25"; d.Text != want {
		t.Errorf("Text = %q, want %q", d.Text, want)
	}
	secs := Sections(d)
	if len(secs) != 2 {
		t.Fatalf("got %d sections, want one per row", len(secs))
	}
	if got, want := secs[0].Text(d), "name: Alice\nage: 30\n"; got != want {
		t.Errorf("section 0 = %q, want %q", got, want)
	}
}

func TestCSVShapes(t *testing.T) {
	cases := []struct {
		name   string
		csv    CSV
		source string
		text   string
		format string
		fields []string
		rows   []map[string]string
		want   string
	}{{
		name: "tab separated", csv: CSV{Sep: '\t'}, source: "x.tsv",
		text: "name\tage\nAlice\t30\n", format: "tsv",
		fields: []string{"name", "age"},
		rows:   []map[string]string{{"name": "Alice", "age": "30"}},
		want:   "name: Alice\nage: 30",
	}, {
		name: "separator taken from the document", csv: CSV{}, source: "x.csv",
		text: "name;age\nAlice;30\n", format: "csv",
		fields: []string{"name", "age"},
		rows:   []map[string]string{{"name": "Alice", "age": "30"}},
		want:   "name: Alice\nage: 30",
	}, {
		name: "tabs found without being told", csv: CSV{}, source: "x.csv",
		text: "name\tage\nAlice\t30\n", format: "tsv",
		fields: []string{"name", "age"},
		rows:   []map[string]string{{"name": "Alice", "age": "30"}},
		want:   "name: Alice\nage: 30",
	}, {
		name: "no header row", csv: CSV{Bare: true}, source: "x.csv",
		text: "Alice,30\nBob,25\n", format: "csv",
		fields: []string{"1", "2"},
		rows: []map[string]string{
			{"1": "Alice", "2": "30"},
			{"1": "Bob", "2": "25"},
		},
		want: "1: Alice\n2: 30\n1: Bob\n2: 25",
	}, {
		name: "row wider than the header", csv: CSV{}, source: "x.csv",
		text: "a,b\n1,2,3\n", format: "csv",
		fields: []string{"a", "b"},
		rows:   []map[string]string{{"a": "1", "b": "2", "3": "3"}},
		want:   "a: 1\nb: 2\n3: 3",
	}, {
		name: "row narrower than the header", csv: CSV{}, source: "x.csv",
		text: "a,b\n1\n", format: "csv",
		fields: []string{"a", "b"},
		rows:   []map[string]string{{"a": "1", "b": ""}},
		want:   "a: 1",
	}, {
		name: "empty cells assert nothing", csv: CSV{}, source: "x.csv",
		text: "a,b\n,\n1,2\n", format: "csv",
		fields: []string{"a", "b"},
		rows: []map[string]string{
			{"a": "", "b": ""},
			{"a": "1", "b": "2"},
		},
		want: "a: 1\nb: 2",
	}, {
		name: "quoted separators and newlines", csv: CSV{}, source: "x.csv",
		text: "a,b\n\"x,y\",\"one\ntwo\"\n", format: "csv",
		fields: []string{"a", "b"},
		rows:   []map[string]string{{"a": "x,y", "b": "one\ntwo"}},
		want:   "a: x,y\nb: one two",
	}, {
		name: "blank header cells are numbered", csv: CSV{}, source: "x.csv",
		text: "a,,c\n1,2,3\n", format: "csv",
		fields: []string{"a", "2", "c"},
		rows:   []map[string]string{{"a": "1", "2": "2", "c": "3"}},
		want:   "a: 1\n2: 2\nc: 3",
	}, {
		name: "byte order mark", csv: CSV{}, source: "x.csv",
		text: "\ufeffname,age\nAlice,30\n", format: "csv",
		fields: []string{"name", "age"},
		rows:   []map[string]string{{"name": "Alice", "age": "30"}},
		want:   "name: Alice\nage: 30",
	}, {
		name: "header only", csv: CSV{}, source: "x.csv",
		text: "a,b\n", format: "csv",
		fields: []string{"a", "b"},
		rows:   []map[string]string{},
		want:   "",
	}, {
		name: "carriage returns", csv: CSV{}, source: "x.csv",
		text: "a,b\r\n1,2\r\n", format: "csv",
		fields: []string{"a", "b"},
		rows:   []map[string]string{{"a": "1", "b": "2"}},
		want:   "a: 1\nb: 2",
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := read(t, c.csv, doc(c.source, c.text))
			if d.Meta["format"] != c.format {
				t.Errorf("format = %v, want %v", d.Meta["format"], c.format)
			}
			if got := Fields(d); !slices.Equal(got, c.fields) {
				t.Errorf("Fields = %v, want %v", got, c.fields)
			}
			rows, ok := Data(d).([]map[string]string)
			if !ok {
				t.Fatalf("Data is %T, want []map[string]string", Data(d))
			}
			if len(rows) != len(c.rows) {
				t.Fatalf("got %d rows, want %d: %v", len(rows), len(c.rows), rows)
			}
			for i := range rows {
				if !maps.Equal(rows[i], c.rows[i]) {
					t.Errorf("row %d = %v, want %v", i, rows[i], c.rows[i])
				}
			}
			if d.Text != c.want {
				t.Errorf("Text = %q, want %q", d.Text, c.want)
			}
		})
	}
}

func TestCSVEmpty(t *testing.T) {
	d := read(t, CSV{}, doc("x.csv", ""))
	if d.Text != "" {
		t.Errorf("Text = %q, want empty", d.Text)
	}
	if Data(d) != nil {
		t.Errorf("Data = %v, want nil for a document with no rows", Data(d))
	}
}

// TestXML covers the case tests/parse/test_parse_comprehensive.py asserts for
// XMLParser — a root element holding a person — and the layout that reaches
// the splitter.
func TestXML(t *testing.T) {
	const src = `<?xml version="1.0"?>
<root>
    <person>
        <name>Alice</name>
        <age>30</age>
    </person>
</root>
`
	d := read(t, XML{}, doc("people.xml", src))

	root, ok := Data(d).(*Node)
	if !ok {
		t.Fatalf("Data is %T, want *Node", Data(d))
	}
	if root.Name != "root" {
		t.Fatalf("root is %q, want root", root.Name)
	}
	people := root.Find("person")
	if len(people) != 1 {
		t.Fatalf("Find(person) returned %d, want 1", len(people))
	}
	if got := people[0].Find("name"); len(got) != 1 || got[0].Text != "Alice" {
		t.Errorf("name = %v", got)
	}
	if got := root.Find("age"); len(got) != 1 || got[0].Text != "30" {
		t.Errorf("age = %v", got)
	}
	if want := "person.name: Alice\nperson.age: 30"; d.Text != want {
		t.Errorf("Text = %q, want %q", d.Text, want)
	}
	secs := Sections(d)
	if len(secs) != 1 || secs[0].Title != "person" || secs[0].Level != 1 {
		t.Fatalf("sections = %+v, want one titled person", secs)
	}
	if secs[0].Text(d) != d.Text {
		t.Errorf("the only section does not cover the text")
	}
}

func TestXMLShapes(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		want  string
		title string
		secs  []string
	}{{
		name: "repeated siblings are numbered",
		text: "<list><item>a</item><item>b</item></list>",
		want: "item.0: a\nitem.1: b",
		secs: []string{"item", "item"},
	}, {
		name: "attributes",
		text: `<person role="dev" id="7">Alice</person>`,
		want: "@id: 7\n@role: dev\nAlice",
	}, {
		name: "nested attributes",
		text: `<root><person id="7"><name>Alice</name></person></root>`,
		want: "person@id: 7\nperson.name: Alice",
		secs: []string{"person"},
	}, {
		name: "text interrupted by an element",
		text: "<p>before<b>middle</b>after</p>",
		want: "before after\nb: middle",
		secs: []string{"", "b"},
	}, {
		name:  "title",
		text:  "<doc><title>Notes</title><body>words</body></doc>",
		want:  "title: Notes\nbody: words",
		title: "Notes",
		secs:  []string{"title", "body"},
	}, {
		name: "declared encoding is ignored",
		text: `<?xml version="1.0" encoding="ISO-8859-1"?><a>x</a>`,
		want: "x",
	}, {
		name: "missing end tag",
		text: "<root><a>x</root>",
		want: "a: x",
		secs: []string{"a"},
	}, {
		name: "unknown entity is left alone",
		text: "<a>five &lt; six &nbsp; seven</a>",
		want: "five < six &nbsp; seven",
	}, {
		name: "several top level elements",
		text: "<a>1</a><b>2</b>",
		want: "a: 1\nb: 2",
		secs: []string{"a", "b"},
	}, {
		name: "no elements at all",
		text: "hello world",
		want: "hello world",
	}, {
		name: "comments and instructions are not text",
		text: "<a><!-- note --><?target data?>x</a>",
		want: "x",
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := read(t, XML{}, doc("x.xml", c.text))
			if d.Text != c.want {
				t.Errorf("Text = %q, want %q", d.Text, c.want)
			}
			if got := Title(d); got != c.title {
				t.Errorf("Title = %q, want %q", got, c.title)
			}
			var titles []string
			for _, s := range Sections(d) {
				titles = append(titles, s.Title)
			}
			if !slices.Equal(titles, c.secs) {
				t.Errorf("section titles = %v, want %v", titles, c.secs)
			}
		})
	}
}

func TestXMLNamespace(t *testing.T) {
	const atom = `<feed xmlns="http://www.w3.org/2005/Atom" xmlns:x="urn:x">
  <title>Feed</title>
  <entry><link href="/one"/><x:note>n</x:note></entry>
</feed>`
	d := read(t, XML{}, doc("https://x.example/a/feed.atom", atom))

	root := Data(d).(*Node)
	if root.Name != "feed" {
		t.Fatalf("root is %q", root.Name)
	}
	if root.Space != "http://www.w3.org/2005/Atom" {
		t.Errorf("Space = %q", root.Space)
	}
	if len(root.Attr) != 0 {
		t.Errorf("Attr = %v, want no namespace declarations", root.Attr)
	}
	if got := root.Find("note"); len(got) != 1 || got[0].Space != "urn:x" {
		t.Errorf("prefixed element = %v", got)
	}
	if got := Title(d); got != "Feed" {
		t.Errorf("Title = %q, want Feed", got)
	}
	links := Links(d)
	if len(links) != 1 || links[0].URL != "https://x.example/one" {
		t.Fatalf("Links = %+v, want the target resolved against the source", links)
	}
}

func TestXMLFeedLink(t *testing.T) {
	const rss = `<rss><channel><title>Feed</title>` +
		`<link>https://x.example/post</link>` +
		`<description>3.14: not a link</description></channel></rss>`
	d := read(t, XML{}, doc("feed.rss", rss))

	links := Links(d)
	if len(links) != 1 || links[0].URL != "https://x.example/post" {
		t.Fatalf("Links = %+v, want the one element that holds a URL", links)
	}
}

func TestXMLBroken(t *testing.T) {
	cases := map[string]string{
		"ends mid element": "<root><a>x",
		"crossed tags":     "<a><b></a></b>",
		"no element name":  "<<<",
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := (XML{}).Parse(context.Background(), doc("x.xml", text)); err == nil {
				t.Fatal("Parse returned no error")
			}
		})
	}
}

// TestEmail covers the case tests/parse/test_parse_comprehensive.py asserts
// for EmailParser: the subject and sender off the headers, the body as text.
func TestEmail(t *testing.T) {
	const note = "From: sender@example.com\n" +
		"To: recipient@example.com\n" +
		"Subject: Test Email\n" +
		"\n" +
		"This is the body.\n"
	d := read(t, Email{}, doc("note.eml", note))

	tags := Tags(d)
	if tags["subject"] != "Test Email" {
		t.Errorf("subject = %q", tags["subject"])
	}
	if tags["from"] != "sender@example.com" {
		t.Errorf("from = %q", tags["from"])
	}
	if tags["to"] != "recipient@example.com" {
		t.Errorf("to = %q", tags["to"])
	}
	if Title(d) != "Test Email" {
		t.Errorf("Title = %q", Title(d))
	}
	if d.Text != "This is the body." {
		t.Errorf("Text = %q", d.Text)
	}
	secs := Sections(d)
	if len(secs) != 1 || secs[0].Title != "text/plain" {
		t.Fatalf("sections = %+v", secs)
	}
}

func TestEmailBodies(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		want  string
		title string
		links []Link
		secs  []string
	}{{
		name: "encoded subject",
		text: "Subject: =?utf-8?B?SGVsbG8gd29ybGQ=?=\n\nbody\n",
		want: "body", title: "Hello world", secs: []string{"text/plain"},
	}, {
		name: "base64 body",
		text: "Subject: s\nContent-Type: text/plain\n" +
			"Content-Transfer-Encoding: base64\n\nSGVsbG8gd29ybGQ=\n",
		want: "Hello world", title: "s", secs: []string{"text/plain"},
	}, {
		name: "quoted printable body",
		text: "Subject: s\nContent-Type: text/plain\n" +
			"Content-Transfer-Encoding: quoted-printable\n\nfive =3D 5\n",
		want: "five = 5", title: "s", secs: []string{"text/plain"},
	}, {
		name: "html body",
		text: "Subject: s\nContent-Type: text/html\n\n" +
			`<p>Hello <a href="http://x.example/">there</a></p>` + "\n",
		want: "Hello there", title: "s",
		links: []Link{{Text: "there", URL: "http://x.example/"}},
		secs:  []string{"text/html"},
	}, {
		name: "alternative takes the plain part",
		text: "Subject: s\nContent-Type: multipart/alternative; boundary=X\n\n" +
			"--X\nContent-Type: text/plain\n\nplain body\n" +
			"--X\nContent-Type: text/html\n\n<p>rich <b>body</b></p>\n" +
			"--X--\n",
		want: "plain body", title: "s", secs: []string{"text/plain"},
	}, {
		name: "alternative falls back to the html part",
		text: "Subject: s\nContent-Type: multipart/alternative; boundary=X\n\n" +
			"--X\nContent-Type: text/html\n\n<p>rich body</p>\n" +
			"--X--\n",
		want: "rich body", title: "s", secs: []string{"text/html"},
	}, {
		name: "mixed reads every part and skips the attachment",
		text: "Subject: s\nContent-Type: multipart/mixed; boundary=Y\n\n" +
			"--Y\nContent-Type: text/plain\n\nsee attached\n" +
			"--Y\nContent-Type: application/pdf; name=\"a.pdf\"\n" +
			"Content-Transfer-Encoding: base64\n\nAAECAw==\n" +
			"--Y--\n",
		want: "see attached", title: "s", secs: []string{"text/plain"},
	}, {
		name: "nested multipart",
		text: "Subject: s\nContent-Type: multipart/mixed; boundary=Y\n\n" +
			"--Y\nContent-Type: multipart/alternative; boundary=X\n\n" +
			"--X\nContent-Type: text/plain\n\ninner plain\n" +
			"--X\nContent-Type: text/html\n\n<p>inner rich</p>\n" +
			"--X--\n" +
			"--Y\nContent-Type: text/plain\n\ntrailing note\n" +
			"--Y--\n",
		want: "inner plain\ntrailing note", title: "s",
		secs: []string{"text/plain", "text/plain"},
	}, {
		name: "no body",
		text: "Subject: s\n\n",
		want: "", title: "s",
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := read(t, Email{}, doc("note.eml", c.text))
			if d.Text != c.want {
				t.Errorf("Text = %q, want %q", d.Text, c.want)
			}
			if got := Title(d); got != c.title {
				t.Errorf("Title = %q, want %q", got, c.title)
			}
			if got := Links(d); !slices.Equal(got, c.links) {
				t.Errorf("Links = %+v, want %+v", got, c.links)
			}
			var titles []string
			for _, s := range Sections(d) {
				titles = append(titles, s.Title)
			}
			if !slices.Equal(titles, c.secs) {
				t.Errorf("section titles = %v, want %v", titles, c.secs)
			}
		})
	}
}

func TestEmailBroken(t *testing.T) {
	if _, err := (Email{}).Parse(context.Background(), doc("x.eml", "not a message")); err == nil {
		t.Fatal("Parse returned no error for text with no headers")
	}
}

func TestJSON(t *testing.T) {
	d := read(t, JSON{}, doc("data.json", `{"key": "value", "list": [1, 2, 3]}`))
	if want := "key: value\nlist.0: 1\nlist.1: 2\nlist.2: 3"; d.Text != want {
		t.Errorf("Text = %q, want %q", d.Text, want)
	}
	v, ok := Data(d).(map[string]any)
	if !ok {
		t.Fatalf("Data is %T, want map[string]any", Data(d))
	}
	if v["key"] != "value" {
		t.Errorf("key = %v", v["key"])
	}

	d = read(t, JSON{}, doc("data.jsonl", "{\"a\":1}\n{\"a\":2}\n"))
	if d.Meta["format"] != "jsonl" {
		t.Errorf("format = %v, want jsonl", d.Meta["format"])
	}
	if len(Sections(d)) != 2 {
		t.Errorf("got %d sections, want one per record", len(Sections(d)))
	}
}

func TestMarkdown(t *testing.T) {
	const src = "---\ntitle: Notes\n---\n\n# Head\n\n" +
		"Body [link](http://x.example/) and ![pic](p.png).\n"
	d := read(t, Markdown{}, doc("notes.md", src))

	if got := Tags(d)["title"]; got != "Notes" {
		t.Errorf("front matter title = %q", got)
	}
	if got := Title(d); got != "Head" {
		t.Errorf("Title = %q, want the first heading", got)
	}
	if strings.Contains(d.Text, "title: Notes") {
		t.Errorf("front matter is still in the text: %q", d.Text)
	}
	if got, want := Links(d), []Link{{Text: "link", URL: "http://x.example/"}}; !slices.Equal(got, want) {
		t.Errorf("Links = %+v, want %+v", got, want)
	}
	if got, want := Images(d), []Link{{Text: "pic", URL: "p.png"}}; !slices.Equal(got, want) {
		t.Errorf("Images = %+v, want %+v", got, want)
	}
}

func TestMarkdownShapes(t *testing.T) {
	const fence = "```"
	cases := []struct {
		name   string
		text   string
		title  string
		secs   []string
		links  []Link
		images []Link
		keeps  string
	}{{
		name:  "underlined heading",
		text:  "Head\n====\n\nbody\n",
		title: "Head", secs: []string{"Head"},
	}, {
		name:  "underlined subheading",
		text:  "Head\n----\n\nbody\n",
		secs:  []string{"Head"},
		title: "",
	}, {
		name:  "reference links",
		text:  "[one][a] and [two][missing]\n\n[a]: http://x.example/1\n",
		links: []Link{{Text: "one", URL: "http://x.example/1"}},
	}, {
		name:   "reference image",
		text:   "![pic][p]\n\n[p]: i.png\n",
		images: []Link{{Text: "pic", URL: "i.png"}},
	}, {
		name:  "headings inside a fence are code",
		text:  "# Real\n\n" + fence + "\n# not a heading\n" + fence + "\n\nAfter.\n",
		title: "Real", secs: []string{"Real"},
	}, {
		name:  "autolink",
		text:  "See <http://x.example/> now.\n",
		links: []Link{{Text: "http://x.example/", URL: "http://x.example/"}},
	}, {
		name:  "an unclosed delimiter is a thematic break, not front matter",
		text:  "---\nnot front matter\n",
		keeps: "---",
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := read(t, Markdown{}, doc("x.md", c.text))
			if got := Title(d); got != c.title {
				t.Errorf("Title = %q, want %q", got, c.title)
			}
			var titles []string
			for _, s := range Sections(d) {
				titles = append(titles, s.Title)
			}
			if !slices.Equal(titles, c.secs) {
				t.Errorf("section titles = %v, want %v", titles, c.secs)
			}
			if got := Links(d); !slices.Equal(got, c.links) {
				t.Errorf("Links = %+v, want %+v", got, c.links)
			}
			if got := Images(d); !slices.Equal(got, c.images) {
				t.Errorf("Images = %+v, want %+v", got, c.images)
			}
			if c.keeps != "" && !strings.Contains(d.Text, c.keeps) {
				t.Errorf("Text = %q, want it to keep %q", d.Text, c.keeps)
			}
		})
	}
}

func TestHTMLShapes(t *testing.T) {
	t.Run("tables, entities and unclosed tags", func(t *testing.T) {
		d := read(t, HTML{}, doc("x.html",
			"<table><tr><td>a<td>b</tr></table><p>x &amp; y</p><div>unclosed"))
		for _, want := range []string{"a b", "x & y", "unclosed"} {
			if !strings.Contains(d.Text, want) {
				t.Errorf("Text = %q, want it to hold %q", d.Text, want)
			}
		}
	})

	t.Run("stray angle brackets are text", func(t *testing.T) {
		d := read(t, HTML{}, doc("x.html", "a < b and c > d"))
		if d.Text != "a < b and c > d" {
			t.Errorf("Text = %q", d.Text)
		}
	})

	t.Run("the document names its own base", func(t *testing.T) {
		d := read(t, HTML{}, doc("https://x.example/",
			`<head><base href="https://y.example/z/"></head><body><a href="q">Q</a>`))
		if got := Links(d); len(got) != 1 || got[0].URL != "https://y.example/z/q" {
			t.Errorf("Links = %+v", got)
		}
	})

	t.Run("the caller names the base", func(t *testing.T) {
		d := read(t, HTML{Base: "https://z.example/"}, doc("https://x.example/", `<a href="q">Q</a>`))
		if got := Links(d); len(got) != 1 || got[0].URL != "https://z.example/q" {
			t.Errorf("Links = %+v", got)
		}
	})

	t.Run("properties and canonical", func(t *testing.T) {
		d := read(t, HTML{}, doc("x.html",
			`<meta property="og:title" content="OG">`+
				`<link rel="canonical" href="https://x.example/c">`))
		if got := Tags(d)["og:title"]; got != "OG" {
			t.Errorf("og:title = %q", got)
		}
		if got := Tags(d)["canonical"]; got != "https://x.example/c" {
			t.Errorf("canonical = %q", got)
		}
	})
}

func TestHTML(t *testing.T) {
	const page = `<html><head><title>Test HTML</title>` +
		`<meta name="author" content="Ada"></head>` +
		`<body><h1>Head</h1><p>Hello World</p>` +
		`<a href="/x">X</a><img src="i.png" alt="Pic">` +
		`<script>var hidden = 1;</script></body></html>`
	d := read(t, HTML{}, doc("https://x.example/a/b.html", page))

	if got := Title(d); got != "Test HTML" {
		t.Errorf("Title = %q", got)
	}
	if got := Tags(d)["author"]; got != "Ada" {
		t.Errorf("author = %q", got)
	}
	if !strings.Contains(d.Text, "Hello World") {
		t.Errorf("Text = %q, want the paragraph", d.Text)
	}
	if strings.Contains(d.Text, "hidden") {
		t.Errorf("script content reached the text: %q", d.Text)
	}
	if got, want := Links(d), []Link{{Text: "X", URL: "https://x.example/x"}}; !slices.Equal(got, want) {
		t.Errorf("Links = %+v, want %+v", got, want)
	}
	if got, want := Images(d), []Link{{Text: "Pic", URL: "https://x.example/a/i.png"}}; !slices.Equal(got, want) {
		t.Errorf("Images = %+v, want %+v", got, want)
	}
}

// TestAny checks the one parser a pipeline reading a mixed corpus needs: it
// takes the format the ingester recorded, and detects one when it did not.
func TestAny(t *testing.T) {
	cases := []struct {
		name   string
		doc    semantic.Doc
		format string
	}{
		{"by extension", doc("x.csv", "a,b\n1,2\n"), "csv"},
		{"by content", doc("", `{"a": 1}`), "json"},
		{"by extension over content", doc("x.txt", `{"a": 1}`), "text"},
		{"markdown", doc("x.md", "# Head\n"), "markdown"},
		{"xml", doc("x.xml", "<a>1</a>"), "xml"},
		{"email", doc("x.eml", "Subject: s\n\nbody\n"), "email"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := read(t, Any{}, c.doc)
			if d.Meta["format"] != c.format {
				t.Fatalf("format = %v, want %v", d.Meta["format"], c.format)
			}
		})
	}

	t.Run("format the ingester recorded", func(t *testing.T) {
		in := doc("mystery", "a,b\n1,2\n")
		in.Meta = map[string]any{"format": "text"}
		d := read(t, Any{}, in)
		if d.Meta["format"] != "text" {
			t.Fatalf("format = %v, want the recorded one", d.Meta["format"])
		}
	})
}

// TestNeed pins the formats this package can name but not read. They must
// fail by name so a caller can tell "no reader" from "unreadable document".
func TestNeed(t *testing.T) {
	for _, name := range []string{"pdf", "docx", "xlsx", "pptx"} {
		t.Run(name, func(t *testing.T) {
			_, err := Any{}.Parse(context.Background(), doc("f."+name, "..."))
			if !errors.Is(err, ErrFormat) {
				t.Fatalf("err = %v, want ErrFormat", err)
			}
			if !strings.Contains(err.Error(), name) {
				t.Errorf("err = %v, want it to name the format", err)
			}
		})
	}
	t.Run("unregistered", func(t *testing.T) {
		f, ok := Get("mystery")
		if ok {
			t.Fatalf("Get returned %v for an unregistered name", f)
		}
	})
}

func TestRegister(t *testing.T) {
	for _, name := range []string{"text", "markdown", "html", "json", "jsonl", "csv", "tsv", "xml", "email", "pdf"} {
		if !slices.Contains(Names(), name) {
			t.Errorf("Names is missing %q", name)
		}
	}
	if !slices.IsSorted(Names()) {
		t.Errorf("Names = %v, want them sorted", Names())
	}

	old, _ := Get("csv")
	t.Cleanup(func() { Register("csv", old) })
	Register("csv", Text{})
	d := read(t, Any{}, doc("x.csv", "a,b\n1,2\n"))
	if d.Meta["format"] != "text" {
		t.Fatalf("format = %v, want the replacement reader to have run", d.Meta["format"])
	}
}

// TestTile holds every format to the promise Section makes: the sections of a
// document tile its text, in order, with no gap and no overlap.
func TestTile(t *testing.T) {
	cases := map[string]semantic.Doc{
		"markdown": doc("x.md", "intro\n\n# One\n\ntext\n\n## Two\n\nmore\n"),
		"html":     doc("x.html", "<p>lead</p><h1>One</h1><p>a</p><h2>Two</h2><p>b</p>"),
		"jsonl":    doc("x.jsonl", "{\"a\":1}\n{\"a\":2}\n{\"a\":3}\n"),
		"csv":      doc("x.csv", "a,b\n1,2\n,\n3,4\n"),
		"xml":      doc("x.xml", "<r>lead<a>1</a><a>2</a><b>3</b></r>"),
		"email": doc("x.eml", "Subject: s\nContent-Type: multipart/mixed; boundary=Y\n\n"+
			"--Y\nContent-Type: text/plain\n\none\n"+
			"--Y\nContent-Type: text/plain\n\ntwo\n--Y--\n"),
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			d := read(t, Any{}, in)
			secs := Sections(d)
			if len(secs) == 0 {
				t.Fatal("no sections")
			}
			if secs[0].Start != 0 {
				t.Errorf("first section starts at %d, want 0", secs[0].Start)
			}
			for i, s := range secs {
				if s.Start > s.End {
					t.Fatalf("section %d spans [%d,%d)", i, s.Start, s.End)
				}
				if i+1 < len(secs) && s.End != secs[i+1].Start {
					t.Errorf("section %d ends at %d, section %d starts at %d",
						i, s.End, i+1, secs[i+1].Start)
				}
			}
			if last := secs[len(secs)-1]; last.End != len(d.Text) {
				t.Errorf("last section ends at %d, text is %d long", last.End, len(d.Text))
			}
			var b strings.Builder
			for _, s := range secs {
				b.WriteString(s.Text(d))
			}
			if b.String() != d.Text {
				t.Errorf("sections do not rejoin\n got %q\nwant %q", b.String(), d.Text)
			}
		})
	}
}

// TestMeta pins that parsing reads the caller's document and does not write
// to it: a pipeline that parses one document twice must get the same answer.
func TestMeta(t *testing.T) {
	cases := []struct {
		name string
		f    Format
		text string
	}{
		{"text", Text{}, "hello"},
		{"markdown", Markdown{}, "# Head\n\ntext\n"},
		{"html", HTML{}, "<p>hello</p>"},
		{"json", JSON{}, `{"a": 1}`},
		{"csv", CSV{}, "a,b\n1,2\n"},
		{"xml", XML{}, "<a>1</a>"},
		{"email", Email{}, "Subject: s\n\nbody\n"},
	}
	for _, c := range cases {
		name, f := c.name, c.f
		t.Run(name, func(t *testing.T) {
			in := doc("x", c.text)
			in.Meta = map[string]any{"keep": 1}
			before := maps.Clone(in.Meta)

			d := read(t, f, in)
			if !maps.Equal(in.Meta, before) {
				t.Errorf("the caller's Meta changed: %v", in.Meta)
			}
			if d.Meta["keep"] != 1 {
				t.Errorf("the caller's own keys were dropped: %v", d.Meta)
			}
			if d.Meta["format"] != name {
				t.Errorf("format = %v, want %v", d.Meta["format"], name)
			}
		})
	}
}

func TestSectionText(t *testing.T) {
	d := doc("x", "0123456789")
	cases := []struct {
		name string
		sec  Section
		want string
	}{
		{"inside", Section{Start: 2, End: 5}, "234"},
		{"whole", Section{Start: 0, End: 10}, "0123456789"},
		{"empty", Section{Start: 3, End: 3}, ""},
		{"past the end", Section{Start: 8, End: 20}, ""},
		{"reversed", Section{Start: 5, End: 2}, ""},
		{"negative", Section{Start: -1, End: 4}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.sec.Text(d); got != c.want {
				t.Errorf("Text = %q, want %q", got, c.want)
			}
		})
	}
}
