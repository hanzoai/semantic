package parse

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"

	"github.com/hanzoai/semantic"
)

// Docx reads a WordprocessingML document (.docx). The body is read in order:
// a paragraph is a line, a heading is a line that starts a section at its
// level, and a table is written as records, its first row naming the columns.
// Blocks are separated by a blank line. The core properties — title, creator,
// dates — come back as tags, and the document's title is its first top-level
// heading, as in Markdown, falling back to the title in its properties.
//
// A heading is what Word itself outlines as one: a paragraph with an outline
// level, set on the paragraph or inherited from its style, or a paragraph in
// one of the built-in "heading N" styles. Word stores those style names in
// English whatever language the document is written in, so the match holds
// for every document.
type Docx struct{}

// Parse reads the document's body, headings and properties.
func (Docx) Parse(ctx context.Context, d semantic.Doc) (semantic.Doc, error) {
	p, err := unzip(ctx, d.Text)
	if err != nil {
		return d, fmt.Errorf("parse docx: %w", err)
	}
	main, core, err := begin(p)
	if err != nil {
		return d, fmt.Errorf("parse docx: %w", err)
	}
	doc, err := part[elem](p, main)
	if err != nil {
		return d, fmt.Errorf("parse docx: %w", err)
	}
	r, err := related(p, main)
	if err != nil {
		return d, fmt.Errorf("parse docx: %w", err)
	}
	lv, err := levels(p, r.kind("styles"))
	if err != nil {
		return d, fmt.Errorf("parse docx: %w", err)
	}
	tags, err := props(p, core)
	if err != nil {
		return d, fmt.Errorf("parse docx: %w", err)
	}

	w := word{p: p, lv: lv}
	if body := doc.kid("body"); body != nil {
		if err := w.blocks(body); err != nil {
			return d, fmt.Errorf("parse docx: %w", err)
		}
	}
	w.b.trim()

	kv := map[string]any{"format": "docx"}
	secs := tile(w.secs, w.b.len())
	if len(secs) > 0 {
		kv["sections"] = secs
	}
	if len(tags) > 0 {
		kv["tags"] = tags
	}
	if t := title(secs, tags); t != "" {
		kv["title"] = t
	}
	return with(d, w.b.text(), kv), nil
}

// word is a document body being laid out as text.
type word struct {
	p    *pkg
	b    buf
	secs []Section
	lv   map[string]int // outline level by style id
}

// blocks writes the block-level content of e in order. Content controls and
// custom XML wrap blocks without being blocks themselves, and are read
// through.
func (w *word) blocks(e *elem) error {
	for _, k := range e.kids {
		var err error
		switch k.name {
		case "p":
			w.para(k)
		case "tbl":
			err = w.table(k)
		case "sdt":
			if c := k.kid("sdtContent"); c != nil {
				err = w.blocks(c)
			}
		case "customXml":
			err = w.blocks(k)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// para writes one paragraph as a line, and opens a section when it is a
// heading. A paragraph with no text writes nothing.
func (w *word) para(p *elem) {
	text := p.said()
	if text == "" {
		return
	}
	w.b.gap()
	if n := w.level(p); n > 0 {
		w.secs = append(w.secs, Section{Title: text, Level: n, Start: w.b.len()})
	}
	w.b.put(text)
	w.b.nl()
}

// level is a paragraph's heading level, 0 for body text. An outline level on
// the paragraph itself outranks its style's.
func (w *word) level(p *elem) int {
	pr := p.kid("pPr")
	if pr == nil {
		return 0
	}
	if o := pr.kid("outlineLvl"); o != nil {
		return outline(o.at("val"))
	}
	if s := pr.kid("pStyle"); s != nil {
		id := s.at("val")
		if n, ok := w.lv[id]; ok {
			return n
		}
		return rank(id) // a style the document never defined, named by its id
	}
	return 0
}

// table writes a table's rows as records. A cell merged across columns
// advances the column count by its span, so the cells after it stay under
// the names above them.
func (w *word) table(t *elem) error {
	var rows [][]cell
	for _, tr := range t.kids {
		if tr.name != "tr" {
			continue
		}
		var row []cell
		col := 0
		for _, tc := range tr.kids {
			if tc.name != "tc" {
				continue
			}
			if s := tc.said(); s != "" {
				row = append(row, cell{col: col, text: s})
			}
			col += span(tc.kid("tcPr"), "gridSpan")
		}
		rows = append(rows, row)
	}
	return grid(w.p, &w.b, rows)
}

// levels reads the heading level of every paragraph style: its own outline
// level, else the level its built-in name implies, else its base style's. A
// document without a styles part has none.
func levels(p *pkg, name string) (map[string]int, error) {
	st, err := part[struct {
		Style []struct {
			Type string `xml:"type,attr"`
			ID   string `xml:"styleId,attr"`
			Name struct {
				Val string `xml:"val,attr"`
			} `xml:"name"`
			Based struct {
				Val string `xml:"val,attr"`
			} `xml:"basedOn"`
			Outline *struct {
				Val string `xml:"val,attr"`
			} `xml:"pPr>outlineLvl"`
		} `xml:"style"`
	}](p, name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	type style struct {
		own   int // -1 when the style says nothing and inherits
		based string
	}
	by := map[string]style{}
	for _, s := range st.Style {
		if s.Type != "" && s.Type != "paragraph" {
			continue
		}
		own := rank(s.Name.Val)
		if s.Outline != nil {
			own = outline(s.Outline.Val)
		} else if own == 0 {
			own = -1
		}
		by[s.ID] = style{own: own, based: s.Based.Val}
	}
	// Each chain is walked until it reaches a style that says, or one already
	// resolved, and every style on the walk takes that level, so each style
	// is walked once. A style on the walk is marked 0 as it is passed, so a
	// chain that comes back to itself — styles based on each other — ends
	// there, as body text, and so does one based on a style never defined.
	out := make(map[string]int, len(by))
	for id := range by {
		var walk []string
		n := 0
		for at := id; ; {
			if v, ok := out[at]; ok {
				n = v
				break
			}
			s, ok := by[at]
			if !ok {
				break
			}
			out[at] = 0
			walk = append(walk, at)
			if s.own >= 0 || s.based == "" {
				n = max(s.own, 0)
				break
			}
			at = s.based
		}
		for _, at := range walk {
			out[at] = n
		}
	}
	return out, nil
}

// span is how many columns a cell covers: the value of the named child of its
// properties, or 1.
func span(pr *elem, name string) int {
	if pr == nil {
		return 1
	}
	if g := pr.kid(name); g != nil {
		if n, err := strconv.Atoi(g.at("val")); err == nil && n > 1 {
			return n
		}
	}
	return 1
}

// outline turns an outline level as stored, 0 to 8, into a heading level, 1
// to 9. Level 9 is body text, and so is anything unreadable.
func outline(v string) int {
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || n > 8 {
		return 0
	}
	return n + 1
}

// rank is the level a built-in heading style's name or id implies: "heading
// 2" and "Heading2" are both 2. Anything else is 0.
func rank(name string) int {
	rest, ok := strings.CutPrefix(strings.ToLower(name), "heading")
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(rest))
	if err != nil || n < 1 || n > 9 {
		return 0
	}
	return n
}
