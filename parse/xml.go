package parse

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/hanzoai/semantic"
)

// XML reads XML into a tree and lays that tree out as text: one
// "path: text" line per element that carries character data, and one
// "path@name: value" line per attribute, the path being the element names
// from the root down. Siblings that share a name are numbered, so two
// <item> elements do not land on the same lines.
//
// The tree itself comes back as Data, since the document's shape is the
// point of writing it in XML at all. Each child of the root is a section,
// which for the two feed vocabularies is one section per entry.
//
// Reading is tolerant of what documents in the wild get wrong: a missing end
// tag is supplied, an unquoted attribute value is read, an unknown entity is
// left as it was written, and the encoding named in the prolog is ignored,
// ingest having decoded the bytes to UTF-8 long before parse sees them. A
// document that ends in the middle of an element is an error, since half a
// tree is not a smaller tree.
type XML struct{}

// Node is one element: its name, the namespace it is in, its attributes, the
// character data it holds directly, and the elements inside it.
type Node struct {
	Name  string            // local name, without its namespace prefix
	Space string            // namespace URI, empty when the element is in none
	Attr  map[string]string // attributes by local name
	Text  string            // the element's own character data, spacing collapsed
	Nodes []*Node
}

// Find returns every element named name in document order, n itself included.
func (n *Node) Find(name string) []*Node {
	var out []*Node
	var walk func(*Node)
	walk = func(n *Node) {
		if n.Name == name {
			out = append(out, n)
		}
		for _, c := range n.Nodes {
			walk(c)
		}
	}
	walk(n)
	return out
}

// Parse reads the document's elements and lays them out as text.
func (XML) Parse(_ context.Context, d semantic.Doc) (semantic.Doc, error) {
	root, err := tree(lines(d.Text))
	if err != nil {
		return d, fmt.Errorf("parse xml: %w", err)
	}
	kv := map[string]any{"format": "xml"}
	var (
		b    buf
		secs []Section
	)
	pairs(&b, root, "")
	for _, s := range steps(root.Nodes, "") {
		start := b.len()
		lay(&b, s.n, s.at)
		if b.len() > start {
			secs = append(secs, Section{Title: s.n.Name, Level: 1, Start: start})
		}
	}
	b.trim()

	links := targets(root)
	resolve(d.Source, links)

	kv["data"] = root
	if t := first(root, "title"); t != "" {
		kv["title"] = t
	}
	if secs = tile(secs, b.len()); len(secs) > 0 {
		kv["sections"] = secs
	}
	if len(links) > 0 {
		kv["links"] = links
	}
	return with(d, b.text(), kv), nil
}

// tree reads the token stream into elements. A document that is not one
// element — a fragment holding several, or character data holding none —
// comes back under a node with no name standing for the document itself.
func tree(s string) (*Node, error) {
	dec := xml.NewDecoder(strings.NewReader(s))
	// A missing end tag is a typo, not a reason to read nothing.
	dec.Strict = false
	// The encoding the prolog names is already spent: ingest decoded the
	// bytes to UTF-8 before parse saw them, so the reader passes through.
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }

	type frame struct {
		n *Node
		b buf
	}
	var (
		roots []*Node
		stack []*frame
		loose buf // character data outside any element
	)
	for {
		t, err := dec.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		switch t := t.(type) {
		case xml.StartElement:
			n := &Node{Name: t.Name.Local, Space: t.Name.Space, Attr: attrs(t.Attr)}
			if len(stack) > 0 {
				top := stack[len(stack)-1]
				top.n.Nodes = append(top.n.Nodes, n)
				// The child breaks the parent's text: what runs before it
				// and what runs after are two runs, not one word.
				top.b.put(" ")
			} else {
				roots = append(roots, n)
			}
			stack = append(stack, &frame{n: n})
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].b.put(string(t))
			} else {
				loose.put(string(t))
			}
		case xml.EndElement:
			if len(stack) == 0 {
				continue
			}
			f := stack[len(stack)-1]
			f.b.trim()
			f.n.Text = f.b.text()
			stack = stack[:len(stack)-1]
		}
	}
	if len(roots) == 1 {
		return roots[0], nil
	}
	loose.trim()
	return &Node{Text: loose.text(), Nodes: roots}, nil
}

// attrs keeps an element's attributes by local name. A namespace declaration
// is not one of them: what it declares reaches the tree as Node.Space.
func attrs(as []xml.Attr) map[string]string {
	var out map[string]string
	for _, a := range as {
		if a.Name.Local == "xmlns" || a.Name.Space == "xmlns" {
			continue
		}
		if out == nil {
			out = make(map[string]string, len(as))
		}
		out[a.Name.Local] = a.Value
	}
	return out
}

// step is one child element and the path it stands at.
type step struct {
	n  *Node
	at string
}

// steps pairs each child with its path, numbering the ones whose name repeats
// so that two elements of a list do not flatten onto the same lines.
func steps(ns []*Node, at string) []step {
	count := map[string]int{}
	for _, c := range ns {
		count[c.Name]++
	}
	seen := map[string]int{}
	out := make([]step, 0, len(ns))
	for _, c := range ns {
		seg := c.Name
		if count[c.Name] > 1 {
			seg += "." + strconv.Itoa(seen[c.Name])
			seen[c.Name]++
		}
		if at != "" {
			seg = at + "." + seg
		}
		out = append(out, step{n: c, at: seg})
	}
	return out
}

// lay writes n and everything under it.
func lay(b *buf, n *Node, at string) {
	pairs(b, n, at)
	for _, s := range steps(n.Nodes, at) {
		lay(b, s.n, s.at)
	}
}

// pairs writes an element's own values: its attributes, then its text.
func pairs(b *buf, n *Node, at string) {
	for _, k := range slices.Sorted(maps.Keys(n.Attr)) {
		pair(b, at+"@"+k, n.Attr[k])
	}
	pair(b, at, n.Text)
}

// pair writes one "path: value" line, the value alone where the path is
// empty, and nothing at all where the value is.
func pair(b *buf, at, v string) {
	if v == "" {
		return
	}
	if at != "" {
		b.put(at)
		b.raw(": ")
	}
	b.put(v)
	b.nl()
}

// first is the text of the first element named name, in document order.
func first(n *Node, name string) string {
	if n.Name == name && n.Text != "" {
		return n.Text
	}
	for _, c := range n.Nodes {
		if t := first(c, name); t != "" {
			return t
		}
	}
	return ""
}

// targets collects the link targets: an href or src attribute anywhere in the
// tree, and the text of a link element when that text is a URL, which is how
// the two feed vocabularies each write the same thing.
func targets(n *Node) []Link {
	var out []Link
	var walk func(*Node)
	walk = func(n *Node) {
		for _, k := range []string{"href", "src"} {
			if v := n.Attr[k]; v != "" {
				out = append(out, Link{Text: n.Text, URL: v})
			}
		}
		if n.Name == "link" && n.Attr["href"] == "" && abs(n.Text) {
			out = append(out, Link{URL: n.Text})
		}
		for _, c := range n.Nodes {
			walk(c)
		}
	}
	walk(n)
	return out
}

// abs reports whether s is a URL and not a sentence that happens to hold a
// colon.
func abs(s string) bool {
	if s == "" || strings.ContainsAny(s, " \t\n") {
		return false
	}
	u, err := url.Parse(s)
	return err == nil && u.IsAbs()
}
