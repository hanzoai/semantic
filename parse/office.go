package parse

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"
)

// The three Office Open XML formats (ECMA-376) are zip archives of XML parts
// tied together by relationship files. Everything they need is in archive/zip
// and encoding/xml; what is here is the package structure they share. The
// legacy binary formats — doc, xls, ppt — are a different container
// altogether, and stay placeholders that report ErrFormat.

// most is what reading one package may spend, in bytes: the stated size of
// each part each time it is read, a node for each element decoded, and the
// text each table writes. Each is charged before it happens — a zip states a
// part's size and archive/zip holds the part to it — so a small file that
// would inflate to gigabytes, name one part or one string without end, or
// decode to a tree many times its size is refused rather than read.
const most = 1 << 28

// node is what an element costs to decode: about what a tree holds for one,
// the element with its name and its place among its parent's children.
const node = 128

// errSpent reports a package that costs more than most to read.
var errSpent = fmt.Errorf("package costs more than %d bytes to read", most)

// pkg is an OOXML package being read, and what reading it may still spend.
type pkg struct {
	ctx  context.Context
	z    *zip.Reader
	left int
}

// unzip opens a document's bytes as the package they are. Ingest keeps a
// binary source's bytes as they were, so Doc.Text is the file.
func unzip(ctx context.Context, d string) (*pkg, error) {
	z, err := zip.NewReader(strings.NewReader(d), int64(len(d)))
	if err != nil {
		return nil, err
	}
	return &pkg{ctx: ctx, z: z, left: most}, nil
}

// spend charges n bytes to the read. It fails once the caller's context is
// done or the budget is gone, and the read stops there.
func (p *pkg) spend(n int) error {
	if err := p.ctx.Err(); err != nil {
		return err
	}
	if n < 0 || n > p.left {
		return errSpent
	}
	p.left -= n
	return nil
}

// part decodes the named part of an OOXML package, as XML, into a T. A part
// the package does not have reports fs.ErrNotExist, and so does the empty
// name a missing relationship resolves to; that is how the readers tell an
// optional part — styles, shared strings, notes — from a broken one.
func part[T any](p *pkg, name string) (T, error) {
	var v T
	if name == "" {
		return v, fs.ErrNotExist
	}
	f, err := p.z.Open(name)
	if err != nil {
		return v, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return v, err
	}
	if err := p.spend(int(fi.Size())); err != nil {
		return v, fmt.Errorf("%s: %w", name, err)
	}
	if err := xml.NewTokenDecoder(meter{xml.NewDecoder(f), p}).Decode(&v); err != nil {
		return v, fmt.Errorf("%s: %w", name, err)
	}
	return v, nil
}

// meter passes a part's tokens on, charging a node for each element as it
// starts. The raw tokens are passed, and the decoder reading them checks
// their nesting and resolves their namespaces.
type meter struct {
	d *xml.Decoder
	p *pkg
}

func (m meter) Token() (xml.Token, error) {
	t, err := m.d.RawToken()
	if _, ok := t.(xml.StartElement); ok {
		if err := m.p.spend(node); err != nil {
			return nil, err
		}
	}
	return t, err
}

// rels is a part's relationships: what it refers to, by id and by type.
type rels struct {
	Rel []struct {
		ID     string `xml:"Id,attr"`
		Type   string `xml:"Type,attr"`
		Target string `xml:"Target,attr"`
		Mode   string `xml:"TargetMode,attr"`
	} `xml:"Relationship"`
}

// related reads the relationships of the part named src, with each internal
// target resolved to the part name it points at. A part with no relationships
// has none, which is not an error.
func related(p *pkg, src string) (rels, error) {
	dir, file := path.Split(src)
	r, err := part[rels](p, dir+"_rels/"+file+".rels")
	if errors.Is(err, fs.ErrNotExist) {
		return rels{}, nil
	}
	if err != nil {
		return rels{}, err
	}
	for i, l := range r.Rel {
		if strings.EqualFold(l.Mode, "External") {
			continue
		}
		if strings.HasPrefix(l.Target, "/") {
			r.Rel[i].Target = strings.TrimPrefix(l.Target, "/")
		} else {
			r.Rel[i].Target = path.Join(dir, l.Target)
		}
	}
	return r, nil
}

// id is the part the relationship with this id points at.
func (r rels) id(id string) string {
	for _, l := range r.Rel {
		if l.ID == id && !strings.EqualFold(l.Mode, "External") {
			return l.Target
		}
	}
	return ""
}

// kind is the first part of a relationship type, matched by the type's last
// segment so the transitional and strict namespaces of ECMA-376 both match.
func (r rels) kind(name string) string {
	for _, l := range r.Rel {
		if strings.HasSuffix(l.Type, "/"+name) && !strings.EqualFold(l.Mode, "External") {
			return l.Target
		}
	}
	return ""
}

// begin reads the package's own relationships: the main document part, and
// the core properties part when there is one.
func begin(p *pkg) (doc, core string, err error) {
	r, err := related(p, "")
	if err != nil {
		return "", "", err
	}
	doc = r.kind("officeDocument")
	if doc == "" {
		return "", "", errors.New("no main document part")
	}
	return doc, r.kind("core-properties"), nil
}

// props reads the core properties — the title, author and dates a document
// carries about itself — as tags. A package without them has none.
func props(p *pkg, name string) (map[string]string, error) {
	c, err := part[struct {
		Title       string `xml:"title"`
		Subject     string `xml:"subject"`
		Creator     string `xml:"creator"`
		Keywords    string `xml:"keywords"`
		Description string `xml:"description"`
		Created     string `xml:"created"`
		Modified    string `xml:"modified"`
	}](p, name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	tags := map[string]string{}
	for k, v := range map[string]string{
		"title": c.Title, "subject": c.Subject, "creator": c.Creator,
		"keywords": c.Keywords, "description": c.Description,
		"created": c.Created, "modified": c.Modified,
	} {
		if v = strings.TrimSpace(v); v != "" {
			tags[k] = v
		}
	}
	return tags, nil
}

// elem is an element of a part kept whole: its local name, its attributes,
// the character data directly inside it, and its children in document order.
// Order is the meaning in a document body — a paragraph, then a table, then a
// paragraph — so the body is walked rather than decoded into fields.
type elem struct {
	name string
	attr []xml.Attr
	text string
	kids []*elem
}

// deepest is how far elements may nest. Real documents nest a few dozen deep;
// the bound is there so a hostile one cannot recurse the reader off its stack,
// and matches the one encoding/xml keeps for its own decoding.
const deepest = 10000

// UnmarshalXML reads an element and everything inside it.
func (e *elem) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	return e.fill(d, start, 0)
}

func (e *elem) fill(d *xml.Decoder, start xml.StartElement, depth int) error {
	if depth > deepest {
		return fmt.Errorf("elements nest deeper than %d", deepest)
	}
	e.name, e.attr = start.Name.Local, start.Attr
	var b strings.Builder
	for {
		t, err := d.Token()
		if err != nil {
			return err
		}
		switch t := t.(type) {
		case xml.StartElement:
			k := &elem{}
			if err := k.fill(d, t, depth+1); err != nil {
				return err
			}
			e.kids = append(e.kids, k)
		case xml.CharData:
			b.Write(t)
		case xml.EndElement:
			e.text = b.String()
			return nil
		}
	}
}

// at is the value of the attribute with this local name, "" when absent.
func (e *elem) at(local string) string {
	for _, a := range e.attr {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

// kid is the first child with this local name, nil when there is none.
func (e *elem) kid(name string) *elem {
	for _, k := range e.kids {
		if k.name == name {
			return k
		}
	}
	return nil
}

// find is the first element with this local name at or below e, in document
// order.
func (e *elem) find(name string) *elem {
	if e.name == name {
		return e
	}
	for _, k := range e.kids {
		if f := k.find(name); f != nil {
			return f
		}
	}
	return nil
}

// said is the text of e, in either vocabulary: the text elements in order, a
// tab, a break or the end of a paragraph as a space. Deleted and moved-away
// revisions and field codes are not text anyone reads, and of alternate
// content only the first choice is taken, since each alternative holds the
// same words.
func (e *elem) said() string {
	var b strings.Builder
	var walk func(*elem)
	walk = func(e *elem) {
		switch e.name {
		case "t":
			b.WriteString(e.text)
			return
		case "tab", "br", "cr":
			b.WriteByte(' ')
			return
		case "del", "delText", "moveFrom", "instrText", "rPr", "pPr":
			return
		case "AlternateContent":
			if len(e.kids) > 0 {
				walk(e.kids[0])
			}
			return
		}
		for _, k := range e.kids {
			walk(k)
		}
		if e.name == "p" {
			b.WriteByte(' ')
		}
	}
	walk(e)
	return strings.Join(strings.Fields(b.String()), " ")
}

// cell is one value of a table and the column it stands in, counted from
// zero. Tables are kept sparse: a spreadsheet can place one value in its last
// column, and a row is not sixteen thousand empty strings.
type cell struct {
	col  int
	text string
}

// grid writes a table as records. The first row names the columns and each
// row after it is a block of "column: value" lines — the layout CSV has, and
// for the same reason: a grid of bare cells says nothing about what they
// mean. A column the first row leaves unnamed is named by its number from
// one. A table of a single row has nothing but names, and they are written as
// lines of text.
//
// A table writes what it holds once as many times as it is named — a
// column's name on every row, a shared string in every cell that points at
// it — so what it writes is charged to p before any of it is.
func grid(p *pkg, b *buf, rows [][]cell) error {
	var full [][]cell
	for _, r := range rows {
		if len(r) > 0 {
			full = append(full, r)
		}
	}
	if len(full) == 0 {
		return nil
	}
	names := map[int]string{}
	for _, c := range full[0] {
		names[c.col] = strings.TrimSpace(strings.TrimPrefix(c.text, "\ufeff"))
	}
	size := 0
	for i, r := range full {
		for _, c := range r {
			size += len(c.text)
			if i > 0 {
				size += len(names[c.col])
			}
		}
	}
	if err := p.spend(size); err != nil {
		return err
	}
	if len(full) == 1 {
		b.gap()
		for _, c := range full[0] {
			b.put(c.text)
			b.nl()
		}
		return nil
	}
	for _, r := range full[1:] {
		b.gap()
		for _, c := range r {
			n := names[c.col]
			if n == "" {
				n = strconv.Itoa(c.col + 1)
			}
			b.put(n)
			b.raw(": ")
			b.put(c.text)
			b.nl()
		}
	}
	return nil
}

// gap starts a block: it ends the current line and leaves one blank line
// after it, so a splitter cutting on paragraphs cuts between blocks. At the
// top of the text there is nothing to separate and it does nothing.
func (x *buf) gap() {
	x.nl()
	if !x.empty() && !bytes.HasSuffix(x.b, []byte("\n\n")) {
		x.b = append(x.b, '\n')
	}
}

// office names the OOXML format of a zip by where its main part lives, which
// is how every producer lays the package out: word/, xl/ or ppt/. Any other
// zip is "zip", which no format reads.
func office(s string) string {
	p, err := unzip(context.Background(), s)
	if err != nil {
		return "zip"
	}
	doc, _, err := begin(p)
	if err != nil {
		return "zip"
	}
	switch top, _, _ := strings.Cut(doc, "/"); top {
	case "word":
		return "docx"
	case "xl":
		return "xlsx"
	case "ppt":
		return "pptx"
	}
	return "zip"
}
