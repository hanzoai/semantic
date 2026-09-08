package export

import (
	"context"
	"io"
	"strconv"
	"strings"

	"github.com/hanzoai/semantic"
)

// GraphML writes the XML that Cytoscape, yEd and Gephi read. A node is
// declared the first time an assertion names it, immediately before the edge
// that needs it, so the document is written in one pass and every edge follows
// the nodes it joins.
type GraphML struct{}

// Write serializes src to w as GraphML.
func (GraphML) Write(ctx context.Context, w io.Writer, src Source) error {
	p := ink(w)
	p.put("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n",
		"<graphml xmlns=\"http://graphml.graphdrawing.org/xmlns\"\n",
		"         xmlns:xsi=\"http://www.w3.org/2001/XMLSchema-instance\"\n",
		"         xsi:schemaLocation=\"http://graphml.graphdrawing.org/xmlns\n",
		"         http://graphml.graphdrawing.org/xmlns/1.0/graphml.xsd\">\n",
		"  <key id=\"label\" for=\"all\" attr.name=\"label\" attr.type=\"string\"/>\n",
		"  <key id=\"score\" for=\"edge\" attr.name=\"score\" attr.type=\"double\"/>\n",
		"  <graph id=\"G\" edgedefault=\"directed\">\n")
	nodes := seen{}
	i := 0
	err := src(ctx, func(t semantic.Triple) error {
		if p.err != nil {
			return p.err
		}
		if err := whole(t, i); err != nil {
			return err
		}
		i++
		for _, term := range [2]string{t.Subject, t.Object} {
			if _, fresh := nodes.id(term, nil); fresh {
				p.put("    <node id=\"", tag(term), "\"><data key=\"label\">", tag(term), "</data></node>\n")
			}
		}
		p.put("    <edge source=\"", tag(t.Subject), "\" target=\"", tag(t.Object), "\">",
			"<data key=\"label\">", tag(t.Predicate), "</data>")
		if t.Score != 0 {
			p.put("<data key=\"score\">", num(t.Score), "</data>")
		}
		p.put("</edge>\n")
		return p.err
	})
	if err != nil {
		return err
	}
	p.put("  </graph>\n</graphml>\n")
	return p.done()
}

// GEXF writes the XML Gephi reads. Its grammar puts every node before every
// edge, so this is the one format here that walks the source twice: once for
// the nodes and once for the edges. The alternative is holding the edges, and
// a graph has more of those than nodes.
type GEXF struct{}

// Write serializes src to w as GEXF, walking src twice.
func (GEXF) Write(ctx context.Context, w io.Writer, src Source) error {
	p := ink(w)
	p.put("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n",
		"<gexf xmlns=\"http://www.gexf.net/1.2draft\" version=\"1.2\">\n",
		"  <graph mode=\"static\" defaultedgetype=\"directed\">\n",
		"    <nodes>\n")
	nodes := seen{}
	i := 0
	err := src(ctx, func(t semantic.Triple) error {
		if p.err != nil {
			return p.err
		}
		if err := whole(t, i); err != nil {
			return err
		}
		i++
		for _, term := range [2]string{t.Subject, t.Object} {
			if _, fresh := nodes.id(term, nil); fresh {
				p.put("      <node id=\"", tag(term), "\" label=\"", tag(term), "\"/>\n")
			}
		}
		return p.err
	})
	if err != nil {
		return err
	}
	p.put("    </nodes>\n    <edges>\n")
	i = 0
	err = src(ctx, func(t semantic.Triple) error {
		if p.err != nil {
			return p.err
		}
		if err := whole(t, i); err != nil {
			return err
		}
		p.put("      <edge id=\"", strconv.Itoa(i), "\" source=\"", tag(t.Subject),
			"\" target=\"", tag(t.Object), "\" label=\"", tag(t.Predicate), "\"")
		if t.Score != 0 {
			p.put(" weight=\"", num(t.Score), "\"")
		}
		p.put("/>\n")
		i++
		return p.err
	})
	if err != nil {
		return err
	}
	p.put("    </edges>\n  </graph>\n</gexf>\n")
	return p.done()
}

// DOT writes the Graphviz language. Name is the graph's name, G when unset.
type DOT struct{ Name string }

// Write serializes src to w as DOT.
func (d DOT) Write(ctx context.Context, w io.Writer, src Source) error {
	name := d.Name
	if name == "" {
		name = "G"
	}
	p := ink(w)
	p.put("digraph ", dots(name), " {\n  rankdir=LR;\n")
	nodes := seen{}
	i := 0
	err := src(ctx, func(t semantic.Triple) error {
		if p.err != nil {
			return p.err
		}
		if err := whole(t, i); err != nil {
			return err
		}
		i++
		for _, term := range [2]string{t.Subject, t.Object} {
			if _, fresh := nodes.id(term, nil); fresh {
				p.put("  ", dots(term), " [label=", dots(term), "];\n")
			}
		}
		p.put("  ", dots(t.Subject), " -> ", dots(t.Object), " [label=", dots(t.Predicate))
		if t.Score != 0 {
			// score, not weight: weight is a layout hint in this language,
			// and a confidence is data, not a request to draw the edge short.
			p.put(", score=", num(t.Score))
		}
		p.put("];\n")
		return p.err
	})
	if err != nil {
		return err
	}
	p.put("}\n")
	return p.done()
}

// dot escapes a string for a quoted DOT identifier, and returns it quoted.
// The backslash goes first: escaping the quote first would then escape the
// backslash the quote's escape had just added.
var dot = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", "")

func dots(s string) string { return `"` + dot.Replace(s) + `"` }

// Mermaid writes a flowchart, for a diagram in a document that renders one.
// Dir is the layout direction — LR, TD, RL, BT — LR when unset.
//
// Mermaid names a node with an identifier rather than a string, so each term
// gets one, n0 upward in the order the terms are first seen, with the term
// itself as the node's text.
type Mermaid struct{ Dir string }

// Write serializes src to w as a Mermaid flowchart.
func (m Mermaid) Write(ctx context.Context, w io.Writer, src Source) error {
	dir := m.Dir
	if dir == "" {
		dir = "LR"
	}
	p := ink(w)
	p.put("flowchart ", dir, "\n")
	nodes := seen{}
	i := 0
	err := src(ctx, func(t semantic.Triple) error {
		if p.err != nil {
			return p.err
		}
		if err := whole(t, i); err != nil {
			return err
		}
		i++
		for _, term := range [2]string{t.Subject, t.Object} {
			if id, fresh := nodes.id(term, node); fresh {
				p.put("  ", id, "[\"", mer.Replace(term), "\"]\n")
			}
		}
		from, _ := nodes.id(t.Subject, node)
		to, _ := nodes.id(t.Object, node)
		p.put("  ", from, " -->|\"", mer.Replace(t.Predicate), "\"| ", to, "\n")
		return p.err
	})
	if err != nil {
		return err
	}
	return p.done()
}

// node names the nth distinct term of a diagram.
func node(n int) string { return "n" + strconv.Itoa(n) }

// mer escapes text for a Mermaid label, where a quote would end the label and
// a hash would start an entity. Both replacements are made in one pass, so the
// escape of one is not read as the start of the other.
var mer = strings.NewReplacer("#", "#35;", `"`, "#quot;", "\n", "<br/>", "\r", "")

// num writes a score the way its bytes read back as the same number.
func num(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }
