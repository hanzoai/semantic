// Command semantic runs a pipeline over a reference and prints what it asserted.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/hanzoai/semantic"
)

func main() {
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: semantic <source>")
		os.Exit(2)
	}
	var p semantic.Pipeline
	triples, err := p.Run(context.Background(), flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, t := range triples {
		fmt.Printf("%s\t%s\t%s\n", t.Subject, t.Predicate, t.Object)
	}
}
