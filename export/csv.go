package export

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"slices"
	"strconv"

	"github.com/hanzoai/semantic"
)

// cols are the columns of the tabular formats, in order. They are the fields
// of an assertion, so a spreadsheet holds everything the graph knew.
var cols = []string{"subject", "predicate", "object", "score", "doc", "chunk", "text", "start", "end"}

// CSV writes the graph as a table, a row to an assertion, carrying every
// field. Sep is the column separator, a comma when unset; the registry has it
// under csv with a comma and under tsv with a tab.
//
// Bare writes the rows without the header line, for appending to a table that
// already has one. Reading rows written that way takes the columns in the
// order above, since there is no header to say otherwise.
type CSV struct {
	Sep  rune
	Bare bool
}

func (c CSV) sep() rune {
	if c.Sep == 0 {
		return ','
	}
	return c.Sep
}

// Write serializes src to w as delimited text.
func (c CSV) Write(ctx context.Context, w io.Writer, src Source) error {
	out := csv.NewWriter(w)
	out.Comma = c.sep()
	if !c.Bare {
		if err := out.Write(cols); err != nil {
			return err
		}
	}
	err := src(ctx, func(t semantic.Triple) error {
		return out.Write([]string{
			t.Subject,
			t.Predicate,
			t.Object,
			strconv.FormatFloat(t.Score, 'g', -1, 64),
			t.From.DocID,
			strconv.Itoa(t.From.Index),
			t.From.Text,
			strconv.Itoa(t.From.Start),
			strconv.Itoa(t.From.End),
		})
	})
	if err != nil {
		return err
	}
	out.Flush()
	return out.Error()
}

// Read parses delimited text back into assertions. The header names the
// columns, so a table whose columns were reordered or trimmed still reads;
// with Bare set the columns are taken in their written order.
func (c CSV) Read(r io.Reader) Source {
	return once(func(ctx context.Context, yield func(semantic.Triple) error) error {
		in := csv.NewReader(r)
		in.Comma = c.sep()
		at := map[string]int{}
		for i, name := range cols {
			at[name] = i
		}
		if !c.Bare {
			head, err := in.Read()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return fmt.Errorf("csv header: %w", err)
			}
			clear(at)
			for i, name := range head {
				if slices.Contains(cols, name) {
					at[name] = i
				}
			}
			if _, ok := at["subject"]; !ok {
				return fmt.Errorf("csv header %v: no subject column", head)
			}
		}
		for line := 2; ; line++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			row, err := in.Read()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return fmt.Errorf("csv: %w", err)
			}
			cell := func(name string) string {
				i, ok := at[name]
				if !ok || i >= len(row) {
					return ""
				}
				return row[i]
			}
			score, err := number(cell("score"))
			if err != nil {
				return fmt.Errorf("csv line %d score: %w", line, err)
			}
			var place [3]int // chunk, start, end
			for i, name := range []string{"chunk", "start", "end"} {
				n, err := number(cell(name))
				if err != nil {
					return fmt.Errorf("csv line %d %s: %w", line, name, err)
				}
				place[i] = int(n)
			}
			t := semantic.Triple{
				Subject:   cell("subject"),
				Predicate: cell("predicate"),
				Object:    cell("object"),
				Score:     score,
				From: semantic.Chunk{
					DocID: cell("doc"),
					Index: place[0],
					Text:  cell("text"),
					Start: place[1],
					End:   place[2],
				},
			}
			if err := yield(t); err != nil {
				return err
			}
		}
	})
}

// number reads a cell that holds a number, taking an empty cell as zero.
func number(s string) (float64, error) {
	if s == "" {
		return 0, nil
	}
	return strconv.ParseFloat(s, 64)
}
