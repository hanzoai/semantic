package parse

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"

	"github.com/hanzoai/semantic"
)

// Pptx reads a PresentationML deck (.pptx): one section per slide, in the
// order the deck shows them, titled by the slide's title. A slide's text is
// its shapes in the order they are drawn — each shape's paragraphs as lines,
// a table as records — and then its speaker notes, under a "Notes:" line,
// since the notes are often where a deck says what its slides only list.
// Slide numbers, dates and footers repeat on every slide and say nothing
// about any of them, and are left out.
//
// The core properties come back as tags, and the deck's title is the first
// slide's title, falling back to the title in its properties.
type Pptx struct{}

// Parse reads every slide of the deck, and its notes.
func (Pptx) Parse(_ context.Context, d semantic.Doc) (semantic.Doc, error) {
	z, err := unzip(d.Text)
	if err != nil {
		return d, fmt.Errorf("parse pptx: %w", err)
	}
	main, core, err := begin(z)
	if err != nil {
		return d, fmt.Errorf("parse pptx: %w", err)
	}
	deck, err := part[struct {
		Slides []struct {
			Attr []xml.Attr `xml:",any,attr"`
		} `xml:"sldIdLst>sldId"`
	}](z, main)
	if err != nil {
		return d, fmt.Errorf("parse pptx: %w", err)
	}
	r, err := related(z, main)
	if err != nil {
		return d, fmt.Errorf("parse pptx: %w", err)
	}
	tags, err := props(z, core)
	if err != nil {
		return d, fmt.Errorf("parse pptx: %w", err)
	}

	var s show
	for i, sl := range deck.Slides {
		name := r.id(rid(sl.Attr))
		if name == "" {
			return d, fmt.Errorf("parse pptx: slide %d has no part", i+1)
		}
		if err := s.slide(z, name); err != nil {
			return d, fmt.Errorf("parse pptx: slide %d: %w", i+1, err)
		}
	}
	s.b.trim()

	kv := map[string]any{"format": "pptx"}
	secs := tile(s.secs, s.b.len())
	if len(secs) > 0 {
		kv["sections"] = secs
	}
	if len(tags) > 0 {
		kv["tags"] = tags
	}
	t := tags["title"]
	for _, sec := range s.secs {
		if sec.Title != "" {
			t = sec.Title
			break
		}
	}
	if t != "" {
		kv["title"] = t
	}
	return with(d, s.b.text(), kv), nil
}

// rid is the relationship id among an element's attributes: the one named
// "id" in a namespace, as opposed to the element's own unqualified id.
func rid(attr []xml.Attr) string {
	for _, a := range attr {
		if a.Name.Local == "id" && a.Name.Space != "" {
			return a.Value
		}
	}
	return ""
}

// show is a deck being laid out as text.
type show struct {
	b     buf
	secs  []Section
	title string // the title of the slide being read
}

// slide writes one slide and its notes as a section.
func (s *show) slide(z *zip.Reader, name string) error {
	sl, err := part[elem](z, name)
	if err != nil {
		return err
	}
	r, err := related(z, name)
	if err != nil {
		return err
	}
	notes, err := part[elem](z, r.kind("notesSlide"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	s.b.gap()
	start := s.b.len()
	s.title = ""
	if tree := sl.find("spTree"); tree != nil {
		s.shapes(tree)
	}
	if tree := notes.find("spTree"); tree != nil {
		var said []string
		for _, sp := range tree.kids {
			if sp.name == "sp" && place(sp) == "body" {
				said = append(said, paras(sp)...)
			}
		}
		if len(said) > 0 {
			s.b.gap()
			s.b.raw("Notes:\n")
			for _, l := range said {
				s.b.put(l)
				s.b.nl()
			}
		}
	}
	if s.b.len() > start {
		s.secs = append(s.secs, Section{Title: s.title, Level: 1, Start: start})
	}
	return nil
}

// shapes writes the shapes of a tree in the order they are drawn, reading
// through groups and taking the first of alternative renderings.
func (s *show) shapes(tree *elem) {
	for _, k := range tree.kids {
		switch k.name {
		case "sp":
			switch place(k) {
			case "sldNum", "dt", "ftr", "hdr":
				continue
			case "title", "ctrTitle":
				if s.title == "" {
					s.title = k.said()
				}
			}
			if ls := paras(k); len(ls) > 0 {
				s.b.gap()
				for _, l := range ls {
					s.b.put(l)
					s.b.nl()
				}
			}
		case "grpSp":
			s.shapes(k)
		case "graphicFrame":
			if t := k.find("tbl"); t != nil {
				grid(&s.b, cells(t))
			}
		case "AlternateContent":
			if len(k.kids) > 0 {
				s.shapes(k.kids[0])
			}
		}
	}
}

// place is the kind of placeholder a shape fills — title, body, slide number
// — or "" for a shape drawn on the slide itself.
func place(sp *elem) string {
	nv := sp.kid("nvSpPr")
	if nv == nil {
		return ""
	}
	if ph := nv.find("ph"); ph != nil {
		if t := ph.at("type"); t != "" {
			return t
		}
		return "obj" // the type ECMA-376 gives a placeholder that states none
	}
	return ""
}

// paras is the text of a shape, a paragraph to a line, empty ones dropped.
func paras(sp *elem) []string {
	body := sp.kid("txBody")
	if body == nil {
		return nil
	}
	var out []string
	for _, p := range body.kids {
		if p.name != "p" {
			continue
		}
		if t := p.said(); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// cells reads a slide table's rows. Every column has a cell element, merged
// or not, so a cell's column is its position.
func cells(t *elem) [][]cell {
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
			col++
		}
		rows = append(rows, row)
	}
	return rows
}
