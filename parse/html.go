package parse

import (
	"context"
	"html"
	"slices"
	"strings"

	"github.com/hanzoai/semantic"
)

// HTML strips the markup and keeps what the markup meant: the text with its
// block structure as line breaks, a section per heading, the link and image
// targets, the title, and the meta elements as tags. Script, style and
// template content is dropped rather than read as prose.
//
// The scanner is tolerant by design — unclosed tags, stray angle brackets and
// unquoted attributes are all common in the wild, and a document that a strict
// parser rejects is exactly the one an ingest pipeline still has to read.
type HTML struct {
	// Base resolves relative link and image targets. When it is empty the
	// document's own <base href> is used, and failing that its Source.
	Base string
}

// Parse extracts the document's text, headings, links and metadata.
func (h HTML) Parse(_ context.Context, d semantic.Doc) (semantic.Doc, error) {
	p := &page{tags: map[string]string{}}
	p.scan(lines(d.Text))

	base := h.Base
	if base == "" {
		base = p.base
	}
	if base == "" {
		base = d.Source
	}
	resolve(base, p.links)
	resolve(base, p.images)

	p.out.trim()
	text := p.out.text()
	for i := range p.secs {
		p.secs[i].Start = min(p.secs[i].Start, len(text))
	}

	kv := map[string]any{"format": "html"}
	if p.title != "" {
		kv["title"] = p.title
	}
	if secs := tile(p.secs, len(text)); len(secs) > 0 {
		kv["sections"] = secs
	}
	if len(p.links) > 0 {
		kv["links"] = p.links
	}
	if len(p.images) > 0 {
		kv["images"] = p.images
	}
	if len(p.tags) > 0 {
		kv["tags"] = p.tags
	}
	return with(d, text, kv), nil
}

// page is one HTML document being read.
type page struct {
	out    buf
	grabs  []*grab
	secs   []Section
	links  []Link
	images []Link
	tags   map[string]string
	title  string
	base   string
}

// grab captures the text inside an element whose content is itself a value:
// the words of a heading, of a link, of the title.
type grab struct {
	out   buf
	tag   string
	level int
	start int
	href  string
}

// blocks are the elements that end a line of text.
var blocks = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true, "br": true,
	"caption": true, "dd": true, "div": true, "dl": true, "dt": true, "figcaption": true,
	"figure": true, "footer": true, "form": true, "h1": true, "h2": true, "h3": true,
	"h4": true, "h5": true, "h6": true, "header": true, "hr": true, "li": true,
	"main": true, "nav": true, "ol": true, "p": true, "pre": true, "section": true,
	"table": true, "tbody": true, "tfoot": true, "thead": true, "tr": true, "ul": true,
}

// hidden are the elements whose content is code or markup, never prose.
var hidden = map[string]bool{"script": true, "style": true, "noscript": true, "template": true}

func (p *page) scan(s string) {
	for i := 0; i < len(s); {
		j := strings.IndexByte(s[i:], '<')
		if j < 0 {
			p.put(s[i:])
			return
		}
		p.put(s[i : i+j])
		i += j

		switch {
		case strings.HasPrefix(s[i:], "<!--"):
			k := strings.Index(s[i+4:], "-->")
			if k < 0 {
				return
			}
			i += 4 + k + 3
			continue
		case strings.HasPrefix(s[i:], "<!"), strings.HasPrefix(s[i:], "<?"):
			k := strings.IndexByte(s[i:], '>')
			if k < 0 {
				return
			}
			i += k + 1
			continue
		}

		t, next, ok := tag(s, i)
		if !ok {
			p.put(s[i : i+1])
			i++
			continue
		}
		i = next
		if hidden[t.name] {
			if !t.shut && !t.self {
				i = shut(s, i, t.name)
			}
			continue
		}
		p.element(t)
	}
}

// element applies one tag to the document being built.
func (p *page) element(t htag) {
	if t.shut {
		switch {
		case t.name == "title":
			if g := p.pop("title"); g != nil {
				p.title = strings.TrimSpace(g.out.text())
			}
		case t.name == "a":
			if g := p.pop("a"); g != nil {
				p.links = append(p.links, Link{Text: strings.TrimSpace(g.out.text()), URL: g.href})
			}
		case heading(t.name) > 0:
			if g := p.pop(t.name); g != nil {
				p.secs = append(p.secs, Section{
					Title: strings.TrimSpace(g.out.text()),
					Level: g.level,
					Start: g.start,
				})
			}
		}
		if blocks[t.name] {
			p.nl()
		}
		return
	}

	switch {
	case t.name == "title":
		p.push(&grab{tag: "title"})
		return
	case t.name == "a":
		if href, ok := t.attr["href"]; ok {
			p.push(&grab{tag: "a", href: href})
		}
	case heading(t.name) > 0:
		p.nl()
		p.push(&grab{tag: t.name, level: heading(t.name), start: p.out.len()})
	case t.name == "img":
		if src, ok := t.attr["src"]; ok {
			p.images = append(p.images, Link{Text: t.attr["alt"], URL: src})
		}
	case t.name == "meta":
		name := t.attr["name"]
		if name == "" {
			name = t.attr["property"]
		}
		if name != "" {
			p.tags[strings.ToLower(name)] = t.attr["content"]
		}
	case t.name == "link":
		if strings.EqualFold(t.attr["rel"], "canonical") {
			p.tags["canonical"] = t.attr["href"]
		}
	case t.name == "base":
		if p.base == "" {
			p.base = t.attr["href"]
		}
	case t.name == "td" || t.name == "th":
		p.put(" ")
	}
	if blocks[t.name] {
		p.nl()
	}
}

// put writes text to the document and to every value being captured.
func (p *page) put(s string) {
	if s == "" {
		return
	}
	s = html.UnescapeString(s)
	for _, g := range p.grabs {
		g.out.put(s)
	}
	if !p.quiet() {
		p.out.put(s)
	}
}

func (p *page) nl() {
	if !p.quiet() {
		p.out.nl()
	}
}

// quiet reports whether the text belongs to a value rather than to the body:
// the title is metadata, not the first paragraph.
func (p *page) quiet() bool {
	for _, g := range p.grabs {
		if g.tag == "title" {
			return true
		}
	}
	return false
}

func (p *page) push(g *grab) { p.grabs = append(p.grabs, g) }

// pop closes the innermost capture for name. Unclosed captures above it are
// dropped, which is what a missing end tag means.
func (p *page) pop(name string) *grab {
	for i, g := range slices.Backward(p.grabs) {
		if g.tag == name {

			p.grabs = p.grabs[:i]
			return g
		}
	}
	return nil
}

// heading returns the level of h1 through h6, and zero for anything else.
func heading(name string) int {
	if len(name) == 2 && name[0] == 'h' && name[1] >= '1' && name[1] <= '6' {
		return int(name[1] - '0')
	}
	return 0
}

// htag is one tag as written in the source.
type htag struct {
	name string
	attr map[string]string
	shut bool // </name>
	self bool // <name/>
}

// tag reads the tag beginning at s[i] and returns the offset just past it.
func tag(s string, i int) (htag, int, bool) {
	var t htag
	j := i + 1
	if j < len(s) && s[j] == '/' {
		t.shut = true
		j++
	}
	start := j
	for j < len(s) && name(s[j]) {
		j++
	}
	if j == start {
		return t, i, false
	}
	t.name = strings.ToLower(s[start:j])

	for j < len(s) {
		for j < len(s) && space(s[j]) {
			j++
		}
		if j >= len(s) {
			break
		}
		switch s[j] {
		case '>':
			return t, j + 1, true
		case '/':
			t.self = true
			j++
			continue
		}
		key := j
		for j < len(s) && !space(s[j]) && s[j] != '=' && s[j] != '>' && s[j] != '/' {
			j++
		}
		k := strings.ToLower(s[key:j])
		v := ""
		mark := j
		for j < len(s) && space(s[j]) {
			j++
		}
		if j < len(s) && s[j] == '=' {
			j++
			for j < len(s) && space(s[j]) {
				j++
			}
			if j < len(s) && (s[j] == '"' || s[j] == '\'') {
				q := s[j]
				j++
				val := j
				for j < len(s) && s[j] != q {
					j++
				}
				v = s[val:j]
				if j < len(s) {
					j++
				}
			} else {
				val := j
				for j < len(s) && !space(s[j]) && s[j] != '>' {
					j++
				}
				v = s[val:j]
			}
		} else {
			j = mark // a bare attribute; the whitespace belongs to the next one
		}
		if k != "" {
			if t.attr == nil {
				t.attr = map[string]string{}
			}
			t.attr[k] = html.UnescapeString(v)
		}
	}
	return t, len(s), true
}

// shut finds the end of the element named name, so its content can be skipped.
func shut(s string, from int, name string) int {
	for i := from; i < len(s); {
		j := strings.IndexByte(s[i:], '<')
		if j < 0 {
			return len(s)
		}
		i += j
		if i+2+len(name) <= len(s) && s[i+1] == '/' && strings.EqualFold(s[i+2:i+2+len(name)], name) {
			if k := strings.IndexByte(s[i:], '>'); k >= 0 {
				return i + k + 1
			}
			return len(s)
		}
		i++
	}
	return len(s)
}

func space(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' }

func name(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == ':'
}
