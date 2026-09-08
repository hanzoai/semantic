// Command semantic runs a pipeline over a reference and prints what it
// asserted, one tab-separated triple to a line.
//
//	semantic notes.md
//	semantic https://example.com/page
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/hanzoai/semantic"
	"github.com/hanzoai/semantic/extract"
	"github.com/hanzoai/semantic/ingest"
	"github.com/hanzoai/semantic/normalize"
	"github.com/hanzoai/semantic/parse"
	"github.com/hanzoai/semantic/split"
)

// read parses a document and settles its text, which is two stages of the
// pipeline in the one slot the Pipeline gives them.
type read struct{}

func (read) Parse(ctx context.Context, d semantic.Doc) (semantic.Doc, error) {
	d, err := parse.Any{}.Parse(ctx, d)
	if err != nil {
		return d, err
	}
	d.Text = normalize.Text(d.Text)
	return d, nil
}

func main() {
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: semantic <source>")
		os.Exit(2)
	}
	p := semantic.Pipeline{
		Ingest:  ingest.Default,
		Parse:   read{},
		Split:   split.Sentences{Max: 1},
		Extract: extract.Rules{},
	}
	triples, err := p.Run(context.Background(), flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, t := range triples {
		fmt.Printf("%s\t%s\t%s\n", t.Subject, t.Predicate, t.Object)
	}
}
