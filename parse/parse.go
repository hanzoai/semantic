// Package parse turns a raw document into text plus the structure the rest of
// the pipeline needs: section boundaries a splitter can respect, link targets,
// and — for tabular and tree formats — the decoded value itself.
//
// A Format reads one format and is registered under a short name. Any picks
// that name from Doc.Meta["format"] when the ingester already knew it, and
// otherwise from the document's extension and the shape of its text, so a
// pipeline that does not know what it is reading still needs only one parser.
//
// The Office Open XML formats — docx, xlsx, pptx — are zip archives of XML,
// and are read here with archive/zip and encoding/xml. Formats this package
// names but does not read — pdf, and the legacy binary Office formats doc, xls
// and ppt — are registered as placeholders that fail with ErrFormat.
// Supplying a reader is one Register call under the same name; nothing else
// changes.
//
// Parsing never opens a file. Turning a source into a Doc is ingest's job;
// what parse reads is Doc.Text. Parsing also does not touch the caller's
// Doc: the returned Doc carries a copy of Meta.
//
// Formats record what they found in Doc.Meta under these keys. Read them with
// the accessors rather than asserting the types by hand:
//
//	format    string             the format that read the document
//	title     string             document title, where the format carries one
//	sections  []Section          spans of Doc.Text
//	links     []Link             link targets
//	images    []Link             image targets, alt text as the link text
//	tags      map[string]string  the document's own metadata pairs
//	fields    []string           column names (csv, tsv)
//	data      any                decoded value: JSON any, CSV []map[string]string, XML *Node
//
// A binary format reads its file from Doc.Text, which holds the file's bytes
// as ingest read them.
package parse

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"path"
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/hanzoai/semantic"
)

// Format reads one document format: it returns the document with its text
// normalized and what it found recorded in Meta.
type Format interface {
	Parse(context.Context, semantic.Doc) (semantic.Doc, error)
}

// ErrFormat reports a format this package can name but not read, because no
// reader is registered under that name or only a placeholder is. Register
// supplies one.
var ErrFormat = errors.New("no reader for format")

var (
	mu      sync.RWMutex
	formats = map[string]Format{}
)

func init() {
	for name, f := range map[string]Format{
		"text":     Text{},
		"markdown": Markdown{},
		"html":     HTML{},
		"json":     JSON{},
		"jsonl":    JSON{},
		"csv":      CSV{},
		"tsv":      CSV{Sep: '\t'},
		"xml":      XML{},
		"email":    Email{},
		"docx":     Docx{},
		"pptx":     Pptx{},
		"xlsx":     Xlsx{},
	} {
		Register(name, f)
	}
	// Named so detection and error messages can be specific, but unreadable
	// until someone registers a reader over them.
	for _, name := range []string{"pdf", "doc", "xls", "ppt"} {
		Register(name, need(name))
	}
}

// Register makes f the reader for name, replacing whatever held that name.
func Register(name string, f Format) {
	mu.Lock()
	defer mu.Unlock()
	formats[name] = f
}

// Get returns the Format registered under name.
func Get(name string) (Format, bool) {
	mu.RLock()
	defer mu.RUnlock()
	f, ok := formats[name]
	return f, ok
}

// Names lists the registered format names in order.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	return slices.Sorted(maps.Keys(formats))
}

// need stands in for a format this package names but does not read.
type need string

// Parse reports that no reader is registered for this format, naming it.
func (n need) Parse(_ context.Context, d semantic.Doc) (semantic.Doc, error) {
	return d, fmt.Errorf("%s: %w", string(n), ErrFormat)
}

// Any parses whatever the document turns out to be. It takes the format from
// Doc.Meta["format"] when the ingester set one and otherwise detects it, so a
// pipeline reading a mixed corpus needs only this one parser.
type Any struct{}

// Parse detects the document's format and hands it to the registered reader.
func (Any) Parse(ctx context.Context, d semantic.Doc) (semantic.Doc, error) {
	name, _ := d.Meta["format"].(string)
	if name == "" {
		name = Detect(d)
	}
	f, ok := Get(name)
	if !ok {
		return d, fmt.Errorf("%s: %w", name, ErrFormat)
	}
	return f.Parse(ctx, d)
}

var (
	_ semantic.Parser = Any{}
	_ Format          = Text{}
	_ Format          = Markdown{}
	_ Format          = HTML{}
	_ Format          = JSON{}
	_ Format          = CSV{}
	_ Format          = XML{}
	_ Format          = Email{}
	_ Format          = Docx{}
	_ Format          = Pptx{}
	_ Format          = Xlsx{}
	_ Format          = need("")
)

// exts maps a source's extension to a format name.
var exts = map[string]string{
	".txt": "text", ".text": "text", ".log": "text", ".rst": "text",
	".md": "markdown", ".markdown": "markdown", ".mdown": "markdown",
	".html": "html", ".htm": "html", ".xhtml": "html",
	".json": "json", ".jsonl": "jsonl", ".ndjson": "jsonl",
	".csv": "csv", ".tsv": "tsv", ".tab": "tsv",
	".xml": "xml", ".rss": "xml", ".atom": "xml", ".svg": "xml",
	".eml":  "email",
	".pdf":  "pdf",
	".docx": "docx", ".docm": "docx",
	".xlsx": "xlsx", ".xlsm": "xlsx",
	".pptx": "pptx", ".pptm": "pptx",
	// The legacy binary formats are compound files, not zips: an OOXML reader
	// cannot open them, and they are named so that they fail as themselves.
	".doc": "doc", ".xls": "xls", ".ppt": "ppt",
}

// Detect names the format of d, from the extension of its source when that
// says anything and from the shape of its text otherwise. It falls back to
// "text", which reads anything.
func Detect(d semantic.Doc) string {
	src := d.Source
	if i := strings.IndexAny(src, "?#"); i >= 0 {
		src = src[:i]
	}
	if name, ok := exts[strings.ToLower(path.Ext(src))]; ok {
		return name
	}
	return sniff(d.Text)
}

// sniff guesses a format from the first characters of the text, or for a
// binary document from its signature.
func sniff(s string) string {
	switch {
	case strings.HasPrefix(s, "PK\x03\x04"):
		return office(s)
	case strings.HasPrefix(s, "%PDF-"):
		return "pdf"
	}
	t := strings.TrimSpace(s)
	switch {
	case t == "":
		return "text"
	case t[0] == '{' || t[0] == '[':
		return "json"
	case t[0] == '<':
		low := strings.ToLower(t)
		if strings.Contains(low, "<html") || strings.Contains(low, "<!doctype html") || strings.Contains(low, "<body") {
			return "html"
		}
		return "xml"
	case header(lines(t)):
		return "email"
	case strings.HasPrefix(t, "---\n"), strings.HasPrefix(t, "# "), strings.HasPrefix(t, "## "),
		strings.Contains(t, "\n# "), strings.Contains(t, "\n## "), strings.Contains(t, "\n```"):
		return "markdown"
	case sep(lines(t)) != 0:
		return "csv"
	}
	return "text"
}

// Section is a span of Doc.Text that stands on its own: a heading and the body
// under it, a CSV row, a JSON record, a child of an XML root. Start and End
// are byte offsets into Doc.Text, so a splitter can respect boundaries it did
// not compute itself. Sections neither nest nor overlap.
type Section struct {
	Title string
	Level int
	Start int
	End   int
}

// Text returns the span of d.Text that s covers, or "" when s does not fit it.
func (s Section) Text(d semantic.Doc) string {
	if s.Start < 0 || s.End > len(d.Text) || s.Start > s.End {
		return ""
	}
	return d.Text[s.Start:s.End]
}

// Link is one target and the text that pointed at it. For an image the text is
// the alt attribute.
type Link struct {
	Text string
	URL  string
}

// Sections returns the spans a format found in d.
func Sections(d semantic.Doc) []Section {
	v, _ := d.Meta["sections"].([]Section)
	return v
}

// Links returns the link targets a format found in d.
func Links(d semantic.Doc) []Link {
	v, _ := d.Meta["links"].([]Link)
	return v
}

// Images returns the image targets a format found in d.
func Images(d semantic.Doc) []Link {
	v, _ := d.Meta["images"].([]Link)
	return v
}

// Tags returns the document's own metadata pairs: HTML meta elements by name
// or property, Markdown front matter by key.
func Tags(d semantic.Doc) map[string]string {
	v, _ := d.Meta["tags"].(map[string]string)
	return v
}

// Fields returns the column names of a delimited document.
func Fields(d semantic.Doc) []string {
	v, _ := d.Meta["fields"].([]string)
	return v
}

// Title returns the document title, empty when the format carries none.
func Title(d semantic.Doc) string {
	v, _ := d.Meta["title"].(string)
	return v
}

// Data returns the decoded value of a structured document: any for JSON,
// []map[string]string for CSV, *Node for XML, nil for prose.
func Data(d semantic.Doc) any { return d.Meta["data"] }

// with returns d with new text and metadata, leaving the caller's map alone.
func with(d semantic.Doc, text string, kv map[string]any) semantic.Doc {
	m := maps.Clone(d.Meta)
	if m == nil {
		m = make(map[string]any, len(kv))
	}
	maps.Copy(m, kv)
	d.Text = text
	d.Meta = m
	return d
}

// lines normalizes CR and CRLF line endings to LF, so byte offsets computed
// here mean the same thing on every platform.
func lines(s string) string {
	if !strings.ContainsRune(s, '\r') {
		return s
	}
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

// starts lists the byte offset of every line in s.
func starts(s string) []int {
	out := []int{0}
	for i := range len(s) {
		if s[i] == '\n' {
			out = append(out, i+1)
		}
	}
	return out
}

// tile fills in each section's End from the next section's Start and prepends
// the run of text before the first one, so the sections cover the whole text.
func tile(secs []Section, n int) []Section {
	if len(secs) == 0 {
		return nil
	}
	slices.SortStableFunc(secs, func(a, b Section) int { return a.Start - b.Start })
	if secs[0].Start > 0 {
		secs = append([]Section{{End: secs[0].Start}}, secs...)
	}
	for i := range secs {
		if i+1 < len(secs) {
			secs[i].End = secs[i+1].Start
		} else {
			secs[i].End = n
		}
	}
	return secs
}

// resolve rewrites relative targets against base, which must be absolute.
func resolve(base string, ls []Link) {
	if base == "" {
		return
	}
	b, err := url.Parse(base)
	if err != nil || !b.IsAbs() {
		return
	}
	for i := range ls {
		u, err := url.Parse(ls[i].URL)
		if err != nil {
			continue
		}
		ls[i].URL = b.ResolveReference(u).String()
	}
}

// buf accumulates extracted text. put collapses runs of whitespace, so the
// result carries the document's words and not its layout; nl ends a line.
type buf struct{ b []byte }

func (x *buf) len() int     { return len(x.b) }
func (x *buf) text() string { return string(x.b) }
func (x *buf) raw(s string) { x.b = append(x.b, s...) }
func (x *buf) last() byte   { return x.b[len(x.b)-1] }
func (x *buf) empty() bool  { return len(x.b) == 0 }
func (x *buf) drop()        { x.b = x.b[:len(x.b)-1] }

func (x *buf) put(s string) {
	for _, r := range s {
		if unicode.IsSpace(r) {
			if x.empty() || x.last() == ' ' || x.last() == '\n' {
				continue
			}
			x.b = append(x.b, ' ')
			continue
		}
		x.b = utf8.AppendRune(x.b, r)
	}
}

// nl closes the current line, dropping the trailing spaces first so no line
// ends in whitespace.
func (x *buf) nl() {
	for !x.empty() && x.last() == ' ' {
		x.drop()
	}
	if !x.empty() && x.last() != '\n' {
		x.b = append(x.b, '\n')
	}
}

// trim removes trailing blank space from the whole buffer.
func (x *buf) trim() {
	for !x.empty() && (x.last() == '\n' || x.last() == ' ') {
		x.drop()
	}
}
