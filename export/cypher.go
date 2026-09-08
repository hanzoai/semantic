package export

import (
	"context"
	"io"
	"strings"

	"github.com/hanzoai/semantic"
)

// Cypher writes CREATE statements for Neo4j, Memgraph and the graph databases
// that speak the same language. A node is created the first time an assertion
// names it, and each edge is a MATCH on the two ids it joins followed by a
// CREATE of the relation between them.
//
// Label is the label every node is created with, Node when unset; the graph
// carries no type of its own to use in its place. A relation keeps the
// predicate it was asserted with, back-quoted, rather than being upper-cased
// into the house style: case is part of a name, and losing it cannot be undone.
type Cypher struct{ Label string }

// Write serializes src to w as Cypher statements.
func (c Cypher) Write(ctx context.Context, w io.Writer, src Source) error {
	label := c.Label
	if label == "" {
		label = "Node"
	}
	p := ink(w)
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
				p.put("CREATE (", id, ":", quoted(label), " {id: ", text(term), "});\n")
			}
		}
		p.put("MATCH (a {id: ", text(t.Subject), "}), (b {id: ", text(t.Object), "}) ",
			"CREATE (a)-[r:", quoted(t.Predicate))
		if t.Score != 0 {
			p.put(" {score: ", num(t.Score), "}")
		}
		p.put("]->(b);\n")
		return p.err
	})
	if err != nil {
		return err
	}
	return p.done()
}

// quoted writes a label or a relation name in back quotes, so a name holding
// punctuation stays a name rather than becoming syntax.
func quoted(s string) string { return "`" + strings.ReplaceAll(s, "`", "``") + "`" }

// text writes a string value. The backslash is escaped first: escaping the
// quote first would then escape the backslash its own escape had just added,
// which is how the Python this is ported from turns \' into \\'.
var quotes = strings.NewReplacer(`\`, `\\`, `'`, `\'`)

func text(s string) string { return "'" + quotes.Replace(s) + "'" }
