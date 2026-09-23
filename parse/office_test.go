package parse

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hanzoai/semantic"
	"github.com/hanzoai/semantic/ingest"
)

// The fixtures below are built the way an Office application writes them — a
// zip of XML parts tied together by relationship files — with archive/zip,
// and read back. Each holds the shapes a reader gets wrong: runs split
// mid-word, deleted revisions, styles inherited from other styles, slides
// stored out of their shown order, dates stored as serial numbers.

const (
	nsW      = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"`
	nsR      = `xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`
	nsP      = `xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"`
	nsA      = `xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"`
	nsX      = `xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"`
	rel      = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/"
	coreType = "http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties"
)

// pack zips name, body pairs into the bytes of a package.
func pack(t *testing.T, parts ...string) string {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for i := 0; i+1 < len(parts); i += 2 {
		w, err := z.Create(parts[i])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(parts[i+1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// links writes a relationships part: id, type, target triples.
func links(rs ...string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for i := 0; i+2 < len(rs); i += 3 {
		typ := rs[i+1]
		if !strings.Contains(typ, "://") {
			typ = rel + typ
		}
		b.WriteString(`<Relationship Id="` + rs[i] + `" Type="` + typ + `" Target="` + rs[i+2] + `"/>`)
	}
	b.WriteString(`</Relationships>`)
	return b.String()
}

const coreXML = `<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" ` +
	`xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/">` +
	`<dc:title>Quarterly memo</dc:title><dc:creator>Ada Lovelace</dc:creator>` +
	`<dcterms:created>2023-03-01T09:00:00Z</dcterms:created></cp:coreProperties>`

// para is a paragraph of plain runs in either vocabulary, with its
// properties, if any, first.
func para(ns string, style string, texts ...string) string {
	var b strings.Builder
	b.WriteString("<" + ns + ":p>")
	if style != "" {
		b.WriteString(style)
	}
	for _, t := range texts {
		b.WriteString("<" + ns + `:r><` + ns + `:t xml:space="preserve">` + t + "</" + ns + ":t></" + ns + ":r>")
	}
	b.WriteString("</" + ns + ":p>")
	return b.String()
}

func docxFixture(t *testing.T) string {
	t.Helper()
	heading := func(id string) string { return `<w:pPr><w:pStyle w:val="` + id + `"/></w:pPr>` }
	cellP := func(texts ...string) string {
		var b strings.Builder
		b.WriteString("<w:tc>")
		for _, s := range texts {
			b.WriteString(para("w", "", s))
		}
		if len(texts) == 0 {
			b.WriteString("<w:p/>")
		}
		b.WriteString("</w:tc>")
		return b.String()
	}
	body := para("w", heading("Heading1"), "Summary") +
		// A word split across runs, a bold run, and a deleted revision.
		`<w:p><w:r><w:t xml:space="preserve">Revenue </w:t></w:r><w:r><w:rPr><w:b/></w:rPr><w:t>rose</w:t></w:r>` +
		`<w:del><w:r><w:delText xml:space="preserve"> sharply</w:delText></w:r></w:del><w:r><w:t>.</w:t></w:r></w:p>` +
		`<w:p/>` +
		para("w", heading("Aside"), "Staff") +
		`<w:tbl><w:tblPr/>` +
		`<w:tr>` + cellP("Name") + cellP("Role") + `</w:tr>` +
		`<w:tr>` + cellP("Ada") + cellP("Analyst,", "engineer") + `</w:tr>` +
		`<w:tr>` + cellP("Charles") + cellP() + `</w:tr>` +
		`</w:tbl>` +
		`<w:sdt><w:sdtContent>` + para("w", `<w:pPr><w:outlineLvl w:val="0"/></w:pPr>`, "Close") + `</w:sdtContent></w:sdt>` +
		`<w:p><w:r><w:t>Signed</w:t><w:tab/><w:t>A. L.</w:t></w:r></w:p>` +
		`<w:sectPr/>`
	return pack(t,
		"_rels/.rels", links("rId1", "officeDocument", "word/document.xml", "rId2", coreType, "docProps/core.xml"),
		"docProps/core.xml", coreXML,
		"word/_rels/document.xml.rels", links("rId1", "styles", "styles.xml"),
		"word/styles.xml", `<w:styles `+nsW+`>`+
			`<w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:pPr><w:outlineLvl w:val="0"/></w:pPr></w:style>`+
			`<w:style w:type="paragraph" w:styleId="Heading2"><w:name w:val="heading 2"/></w:style>`+
			`<w:style w:type="paragraph" w:styleId="Aside"><w:name w:val="Aside"/><w:basedOn w:val="Heading2"/></w:style>`+
			`<w:style w:type="character" w:styleId="Strong"><w:name w:val="Strong"/></w:style>`+
			`</w:styles>`,
		"word/document.xml", `<w:document `+nsW+` `+nsR+`><w:body>`+body+`</w:body></w:document>`,
	)
}

const docxText = "Summary\n\nRevenue rose.\n\nStaff\n\nName: Ada\nRole: Analyst, engineer\n\nName: Charles\n\n" +
	"Close\n\nSigned A. L."

// TestDocx reads headings as sections at their level — by outline level, by
// built-in name, and through a style based on one — and a table as records
// named by its first row.
func TestDocx(t *testing.T) {
	d := read(t, Any{}, doc("memo.docx", docxFixture(t)))
	if d.Meta["format"] != "docx" {
		t.Fatalf("format = %v", d.Meta["format"])
	}
	if d.Text != docxText {
		t.Errorf("Text =\n%q\nwant\n%q", d.Text, docxText)
	}
	var got []Section
	for _, s := range Sections(d) {
		got = append(got, Section{Title: s.Title, Level: s.Level})
	}
	want := []Section{{Title: "Summary", Level: 1}, {Title: "Staff", Level: 2}, {Title: "Close", Level: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sections = %+v, want %+v", got, want)
	}
	if secs := Sections(d); len(secs) == 3 {
		if s := secs[1].Text(d); !strings.HasPrefix(s, "Staff\n") || !strings.Contains(s, "Name: Charles") {
			t.Errorf("section Staff = %q, want the heading and the table under it", s)
		}
	}
	if Title(d) != "Summary" {
		t.Errorf("Title = %q, want the first top-level heading", Title(d))
	}
	if tags := Tags(d); tags["title"] != "Quarterly memo" || tags["creator"] != "Ada Lovelace" ||
		tags["created"] != "2023-03-01T09:00:00Z" {
		t.Errorf("Tags = %v, want the core properties", tags)
	}
}

func pptxFixture(t *testing.T) string {
	t.Helper()
	sp := func(ph string, paras ...string) string {
		var b strings.Builder
		b.WriteString(`<p:sp><p:nvSpPr><p:cNvPr id="2" name="s"/><p:cNvSpPr/><p:nvPr>` + ph + `</p:nvPr></p:nvSpPr><p:spPr/><p:txBody><a:bodyPr/>`)
		for _, s := range paras {
			b.WriteString(para("a", "", s))
		}
		b.WriteString(`</p:txBody></p:sp>`)
		return b.String()
	}
	slide := func(shapes string) string {
		return `<p:sld ` + nsP + ` ` + nsA + ` ` + nsR + `><p:cSld><p:spTree><p:nvGrpSpPr/><p:grpSpPr/>` +
			shapes + `</p:spTree></p:cSld></p:sld>`
	}
	table := `<p:graphicFrame><p:nvGraphicFramePr/><a:graphic><a:graphicData><a:tbl>` +
		`<a:tr><a:tc><a:txBody>` + para("a", "", "Owner") + `</a:txBody></a:tc><a:tc><a:txBody>` + para("a", "", "Due") + `</a:txBody></a:tc></a:tr>` +
		`<a:tr><a:tc><a:txBody>` + para("a", "", "Ada") + `</a:txBody></a:tc><a:tc><a:txBody>` + para("a", "", "May") + `</a:txBody></a:tc></a:tr>` +
		`</a:tbl></a:graphicData></a:graphic></p:graphicFrame>`
	plan := slide(sp(`<p:ph type="title"/>`, "Plan") +
		sp(`<p:ph idx="1"/>`, "Ship the reader", "", "Test it") +
		sp(`<p:ph type="sldNum" idx="12"/>`, "7") +
		`<p:grpSp><p:nvGrpSpPr/><p:grpSpPr/>` + sp(``, "Draft") + `</p:grpSp>` +
		table)
	welcome := slide(sp(`<p:ph type="ctrTitle"/>`, "Welcome") + sp(`<p:ph type="subTitle" idx="1"/>`, "Babbage Engines"))
	notes := `<p:notes ` + nsP + ` ` + nsA + `><p:cSld><p:spTree><p:nvGrpSpPr/><p:grpSpPr/>` +
		sp(`<p:ph type="sldImg"/>`) + sp(`<p:ph type="body" idx="1"/>`, "Say hello", "Then the plan") +
		`</p:spTree></p:cSld></p:notes>`
	return pack(t,
		"_rels/.rels", links("rId1", "officeDocument", "ppt/presentation.xml", "rId2", coreType, "docProps/core.xml"),
		"docProps/core.xml", coreXML,
		// Shown order is slide 2 then slide 1: the list decides, not the names.
		"ppt/presentation.xml", `<p:presentation `+nsP+` `+nsR+`><p:sldIdLst>`+
			`<p:sldId id="257" r:id="rId3"/><p:sldId id="256" r:id="rId2"/></p:sldIdLst></p:presentation>`,
		"ppt/_rels/presentation.xml.rels", links("rId2", "slide", "slides/slide1.xml", "rId3", "slide", "slides/slide2.xml"),
		"ppt/slides/slide1.xml", welcome,
		"ppt/slides/_rels/slide1.xml.rels", links("rId1", "notesSlide", "../notesSlides/notesSlide1.xml"),
		"ppt/notesSlides/notesSlide1.xml", notes,
		"ppt/slides/slide2.xml", plan,
	)
}

const pptxText = "Plan\n\nShip the reader\nTest it\n\nDraft\n\nOwner: Ada\nDue: May\n\n" +
	"Welcome\n\nBabbage Engines\n\nNotes:\nSay hello\nThen the plan"

// TestPptx reads slides in the order the deck shows them, one section each,
// with the slide number left out and the speaker notes kept.
func TestPptx(t *testing.T) {
	d := read(t, Any{}, doc("deck.pptx", pptxFixture(t)))
	if d.Meta["format"] != "pptx" {
		t.Fatalf("format = %v", d.Meta["format"])
	}
	if d.Text != pptxText {
		t.Errorf("Text =\n%q\nwant\n%q", d.Text, pptxText)
	}
	secs := Sections(d)
	if len(secs) != 2 || secs[0].Title != "Plan" || secs[1].Title != "Welcome" {
		t.Fatalf("sections = %+v, want Plan then Welcome", secs)
	}
	if s := secs[1].Text(d); !strings.Contains(s, "Notes:\nSay hello") {
		t.Errorf("slide Welcome = %q, want its notes in it", s)
	}
	if Title(d) != "Plan" {
		t.Errorf("Title = %q, want the first slide's", Title(d))
	}
	if Tags(d)["creator"] != "Ada Lovelace" {
		t.Errorf("Tags = %v", Tags(d))
	}
}

func xlsxFixture(t *testing.T, from1904 bool) string {
	t.Helper()
	pr := ""
	if from1904 {
		pr = `<workbookPr date1904="1"/>`
	}
	return pack(t,
		"_rels/.rels", links("rId1", "officeDocument", "xl/workbook.xml"),
		"xl/workbook.xml", `<workbook `+nsX+` `+nsR+`>`+pr+`<sheets>`+
			`<sheet name="People" sheetId="1" r:id="rId1"/><sheet name="Empty" sheetId="2" r:id="rId2"/>`+
			`<sheet name="Log" sheetId="3" r:id="rId3"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels", links(
			"rId1", "worksheet", "worksheets/sheet1.xml",
			"rId2", "worksheet", "worksheets/sheet2.xml",
			"rId3", "worksheet", "/xl/worksheets/sheet3.xml", // an absolute target
			"rId4", "sharedStrings", "sharedStrings.xml",
			"rId5", "styles", "styles.xml"),
		"xl/sharedStrings.xml", `<sst `+nsX+`>`+
			`<si><t>Name</t></si><si><t>Joined</t></si><si><t>Score</t></si><si><t>Ada</t></si><si><t>Active</t></si>`+
			`<si><r><t>Char</t></r><r><rPr><b/></rPr><t>les</t></r><rPh><t>X</t></rPh></si></sst>`,
		"xl/styles.xml", `<styleSheet `+nsX+`><numFmts>`+
			`<numFmt numFmtId="164" formatCode="yyyy-mm-dd hh:mm"/><numFmt numFmtId="165" formatCode="[h]:mm"/>`+
			`</numFmts><cellXfs><xf numFmtId="0"/><xf numFmtId="14"/><xf numFmtId="164"/><xf numFmtId="165"/><xf numFmtId="20"/></cellXfs></styleSheet>`,
		"xl/worksheets/sheet1.xml", `<worksheet `+nsX+`><sheetData>`+
			`<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c><c r="C1" t="s"><v>2</v></c><c r="D1" t="s"><v>4</v></c></row>`+
			`<row r="2"><c r="A2" t="s"><v>3</v></c><c r="B2" s="1"><v>45000</v></c><c r="C2"><v>3.5</v></c><c r="D2" t="b"><v>1</v></c></row>`+
			`<row r="3"><c r="A3" t="s"><v>5</v></c><c r="B3" s="2"><v>45000.5</v></c><c r="C3" s="3"><v>1.5</v></c>`+
			`<c r="F3" t="inlineStr"><is><t>late</t></is></c></row>`+
			`<row r="4"><c r="A4" s="1"/></row>`+
			`</sheetData></worksheet>`,
		"xl/worksheets/sheet2.xml", `<worksheet `+nsX+`><sheetData/></worksheet>`,
		"xl/worksheets/sheet3.xml", `<worksheet `+nsX+`><sheetData>`+
			`<row r="1"><c r="A1" t="inlineStr"><is><t>When</t></is></c></row>`+
			`<row r="2"><c r="A2" s="4"><v>0.4375</v></c></row>`+
			`</sheetData></worksheet>`,
	)
}

// TestXlsx reads every sheet with a value in it, resolves shared and rich
// strings, and writes a date as the date it stands for.
func TestXlsx(t *testing.T) {
	d := read(t, Any{}, doc("book.xlsx", xlsxFixture(t, false)))
	if d.Meta["format"] != "xlsx" {
		t.Fatalf("format = %v", d.Meta["format"])
	}
	want := "People\n\nName: Ada\nJoined: 2023-03-15\nScore: 3.5\nActive: TRUE\n\n" +
		"Name: Charles\nJoined: 2023-03-15T12:00:00\nScore: 1.5\n6: late\n\n" +
		"Log\n\nWhen: 10:30:00"
	if d.Text != want {
		t.Errorf("Text =\n%q\nwant\n%q", d.Text, want)
	}
	secs := Sections(d)
	if len(secs) != 2 || secs[0].Title != "People" || secs[1].Title != "Log" {
		t.Fatalf("sections = %+v, want People and Log, the empty sheet skipped", secs)
	}

	// The 1904 date system counts from a different day.
	d = read(t, Any{}, doc("book.xlsx", xlsxFixture(t, true)))
	if !strings.Contains(d.Text, "Joined: 2027-03-16\n") {
		t.Errorf("1904 system: %q, want 45000 days after 1904-01-01", d.Text)
	}
}

// TestSerial pins the date arithmetic, including the day the 1900 system
// counts and the calendar does not.
func TestSerial(t *testing.T) {
	day, clock, both := shows{date: true}, shows{time: true}, shows{date: true, time: true}
	for _, c := range []struct {
		v    string
		w    shows
		m    bool
		want string
		ok   bool
	}{
		{"1", day, false, "1900-01-01", true},
		{"59", day, false, "1900-02-28", true},
		{"60", day, false, "", false}, // 29 February 1900
		{"61", day, false, "1900-03-01", true},
		{"44927", day, false, "2023-01-01", true},
		{"44927.25", day, false, "2023-01-01T06:00:00", true}, // the value, not the display
		{"0.5", clock, false, "12:00:00", true},
		{"0", clock, false, "00:00:00", true},
		{"0", day, false, "", false}, // 0 January 1900
		{"44927.000011574", both, false, "2023-01-01T00:00:01", true},
		{"0.0000005787", clock, false, "00:00:00.05", true},
		{"0", day, true, "1904-01-01", true},
		{"-1", day, false, "", false},
		{"2958466", day, false, "", false}, // after 9999-12-31
		{"x", day, false, "", false},
	} {
		got, ok := serial(c.v, c.w, c.m)
		if got != c.want || ok != c.ok {
			t.Errorf("serial(%s, %+v, 1904=%v) = %q, %v; want %q, %v", c.v, c.w, c.m, got, ok, c.want, c.ok)
		}
	}
}

func TestCustom(t *testing.T) {
	for code, want := range map[string]shows{
		"yyyy-mm-dd":              {date: true},
		"mmm yy":                  {date: true},
		"mmmm":                    {date: true},
		"hh:mm":                   {time: true},
		"mm:ss":                   {time: true},
		"yyyy-mm-dd hh:mm:ss":     {date: true, time: true},
		"[h]:mm:ss":               {},
		"[$-409]d-mmm-yyyy":       {date: true},
		`0.00 "days"`:             {},
		`#,##0;[Red]-#,##0`:       {},
		"General":                 {},
		"0.00E+00":                {},
		`\d0`:                     {},
		`_(* #,##0_);_(* (#,##0)`: {},
	} {
		if got := custom(code); got != want {
			t.Errorf("custom(%q) = %+v, want %+v", code, got, want)
		}
	}
}

func TestColumn(t *testing.T) {
	for ref, want := range map[string]int{"A1": 0, "Z9": 25, "AA1": 26, "AB7": 27, "XFD1048576": 16383} {
		if got, ok := column(ref); !ok || got != want {
			t.Errorf("column(%q) = %d, %v; want %d", ref, got, ok, want)
		}
	}
	for _, ref := range []string{"", "12", "XFE1", "AAAAAAAA1"} {
		if _, ok := column(ref); ok {
			t.Errorf("column(%q) read a column", ref)
		}
	}
}

// TestOfficeFromDisk runs a document file through ingest and parse, which is
// the path a real one takes. Ingest has to leave the bytes alone for this to
// work at all.
func TestOfficeFromDisk(t *testing.T) {
	p := filepath.Join(t.TempDir(), "memo.docx")
	if err := os.WriteFile(p, []byte(docxFixture(t)), 0o600); err != nil {
		t.Fatal(err)
	}
	docs, err := ingest.File{}.Ingest(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	d := read(t, Any{}, docs[0])
	if d.Text != docxText {
		t.Errorf("Text = %q, want %q", d.Text, docxText)
	}
	if d.Meta["type"] != "docx" {
		t.Errorf("type = %v, want ingest's origin kept", d.Meta["type"])
	}
}

// TestOfficeSniff names a package with no extension by where its main part
// lives.
func TestOfficeSniff(t *testing.T) {
	for want, text := range map[string]string{
		"docx": docxFixture(t),
		"pptx": pptxFixture(t),
		"xlsx": xlsxFixture(t, false),
		"zip":  pack(t, "readme.txt", "hello"),
		"pdf":  "%PDF-1.7\n%\xe2\xe3\xcf\xd3\n",
	} {
		if got := Detect(semantic.Doc{Source: "https://x.example/download?id=3", Text: text}); got != want {
			t.Errorf("Detect = %q, want %q", got, want)
		}
	}
	if _, err := (Any{}).Parse(context.Background(), doc("", pack(t, "readme.txt", "hello"))); !errors.Is(err, ErrFormat) {
		t.Errorf("a zip that is no document: %v, want ErrFormat", err)
	}
}

// claim zips parts as pack does, and stores the named part as inflating to
// size bytes, whatever it holds. A zip states each part's size, and the
// statement is what a read is charged: a part whose XML ends before the
// claimed size is read to its end and no further.
func claim(t *testing.T, name string, size uint64, body string, parts ...string) string {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	w, err := z.CreateRaw(&zip.FileHeader{
		Name: name, Method: zip.Store,
		CompressedSize64: uint64(len(body)), UncompressedSize64: size,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(parts); i += 2 {
		w, err := z.Create(parts[i])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(parts[i+1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// TestOfficeBroken holds the readers to failing on what they cannot read: a
// file that is not a zip and a zip with no main part.
func TestOfficeBroken(t *testing.T) {
	for name, text := range map[string]string{
		"not a zip":    "just text",
		"no main part": pack(t, "word/document.xml", "<w:document/>"),
	} {
		for _, f := range []Format{Docx{}, Pptx{}, Xlsx{}} {
			if _, err := f.Parse(context.Background(), doc("x", text)); err == nil || errors.Is(err, ErrFormat) {
				t.Errorf("%s through %T: %v, want a read error", name, f, err)
			}
		}
	}

	// The two formats that walk a body as a tree refuse one nested past the
	// bound rather than recursing off the stack. A spreadsheet is decoded into
	// fields, which skips what it does not name without recursing at all.
	deep := strings.Repeat("<a>", deepest+2) + strings.Repeat("</a>", deepest+2)
	nested := pack(t, "_rels/.rels", links("rId1", "officeDocument", "main.xml"),
		"main.xml", `<root><sldIdLst><sldId xmlns:r="urn:r" r:id="rId1"/></sldIdLst><body>`+deep+`</body></root>`,
		"_rels/main.xml.rels", links("rId1", "slide", "main.xml"))
	for _, f := range []Format{Docx{}, Pptx{}} {
		if _, err := f.Parse(context.Background(), doc("x", nested)); err == nil || !strings.Contains(err.Error(), "deeper") {
			t.Errorf("nesting past %d through %T: %v, want it refused", deepest, f, err)
		}
	}
}

// TestOfficeSpend holds a read to its budget. Each part read is charged its
// inflated size, each element decoded from it a node, and each table the text
// it writes, and a package that would spend more than most is refused however
// small its file: one part claimed past the budget, one part named four
// times, one string written into many cells, many elements in a part that
// left room for few.
func TestOfficeSpend(t *testing.T) {
	root := func(main string) string { return links("rId1", "officeDocument", main) }
	deck := func(n int) string {
		var b strings.Builder
		b.WriteString(`<p:presentation ` + nsP + ` ` + nsR + `><p:sldIdLst>`)
		for i := range n {
			fmt.Fprintf(&b, `<p:sldId id="%d" r:id="rId2"/>`, 256+i)
		}
		b.WriteString(`</p:sldIdLst></p:presentation>`)
		return b.String()
	}
	book := func(n int) string {
		var b strings.Builder
		b.WriteString(`<workbook ` + nsX + ` ` + nsR + `><sheets>`)
		for i := range n {
			fmt.Fprintf(&b, `<sheet name="S%d" sheetId="%d" r:id="rId2"/>`, i, i+1)
		}
		b.WriteString(`</sheets></workbook>`)
		return b.String()
	}
	// shared is a workbook whose one shared string, 64 KiB of it, is the
	// header and every value of a column n rows long, in a package that
	// leaves a MiB to spend once the string is read.
	shared := func(n int) string {
		var b strings.Builder
		b.WriteString(`<worksheet ` + nsX + `><sheetData>`)
		for range n + 1 {
			b.WriteString(`<row><c t="s"><v>0</v></c></row>`)
		}
		b.WriteString(`</sheetData></worksheet>`)
		return claim(t, "xl/sharedStrings.xml", most-1<<20,
			`<sst `+nsX+`><si><t>`+strings.Repeat("x", 64<<10)+`</t></si></sst>`,
			"_rels/.rels", root("xl/workbook.xml"),
			"xl/workbook.xml", book(1),
			"xl/_rels/workbook.xml.rels", links("rId2", "worksheet", "sheet.xml", "rId3", "sharedStrings", "sharedStrings.xml"),
			"xl/sheet.xml", b.String())
	}
	crowd := strings.Repeat("<w:p/>", 1024) // 128 KiB of nodes

	for _, c := range []struct {
		name string
		f    Format
		text string
	}{
		{"a part claimed past the budget", Docx{}, claim(t, "word/document.xml", most+1, `<w:document `+nsW+`/>`,
			"_rels/.rels", root("word/document.xml"))},
		{"a part claimed past what a size holds", Docx{}, claim(t, "word/document.xml", 1<<63, `<w:document `+nsW+`/>`,
			"_rels/.rels", root("word/document.xml"))},
		{"one slide shown four times", Pptx{}, claim(t, "ppt/slide.xml", most/4+1, `<p:sld `+nsP+`/>`,
			"_rels/.rels", root("ppt/presentation.xml"),
			"ppt/presentation.xml", deck(4),
			"ppt/_rels/presentation.xml.rels", links("rId2", "slide", "slide.xml"))},
		{"one sheet listed four times", Xlsx{}, claim(t, "xl/sheet.xml", most/4+1, `<worksheet `+nsX+`/>`,
			"_rels/.rels", root("xl/workbook.xml"),
			"xl/workbook.xml", book(4),
			"xl/_rels/workbook.xml.rels", links("rId2", "worksheet", "sheet.xml"))},
		{"one string in many cells", Xlsx{}, shared(16)},
		{"many elements in a body", Docx{}, claim(t, "word/document.xml", most-64<<10,
			`<w:document `+nsW+`><w:body>`+crowd+`</w:body></w:document>`,
			"_rels/.rels", root("word/document.xml"))},
		{"many cells in a sheet", Xlsx{}, claim(t, "xl/sheet.xml", most-64<<10,
			`<worksheet `+nsX+`><sheetData><row>`+strings.Repeat("<c/>", 1024)+`</row></sheetData></worksheet>`,
			"_rels/.rels", root("xl/workbook.xml"),
			"xl/workbook.xml", book(1),
			"xl/_rels/workbook.xml.rels", links("rId2", "worksheet", "sheet.xml"))},
	} {
		if _, err := c.f.Parse(context.Background(), doc("x", c.text)); !errors.Is(err, errSpent) {
			t.Errorf("%s: %v, want it refused as over the budget", c.name, err)
		}
	}

	// The same string in a few cells is within it.
	d := read(t, Xlsx{}, doc("x", shared(4)))
	if n := strings.Count(d.Text, strings.Repeat("x", 64<<10)); n != 8 {
		t.Errorf("a string in four records written %d times, want 8: a name and a value each", n)
	}

	// A caller that has given up stops the read.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, c := range []struct {
		f    Format
		text string
	}{{Docx{}, docxFixture(t)}, {Pptx{}, pptxFixture(t)}, {Xlsx{}, xlsxFixture(t, false)}} {
		if _, err := c.f.Parse(ctx, doc("x", c.text)); !errors.Is(err, context.Canceled) {
			t.Errorf("%T with its context cancelled: %v, want context.Canceled", c.f, err)
		}
	}
}

// TestDocxStyleChain resolves styles in time linear in their number: a chain
// of styles, each based on the next, reads in about the time as many styles
// based on nothing do. The style at the end of the chain names the level of
// all of it, and a cycle of styles is body text.
func TestDocxStyleChain(t *testing.T) {
	const n = 1 << 13
	styles := func(chain bool) string {
		var st strings.Builder
		st.WriteString(`<w:styles ` + nsW + `>`)
		for i := range n {
			base := fmt.Sprintf("x%d", i) // a style never defined
			if chain {
				base = fmt.Sprintf("s%d", i+1)
			}
			fmt.Fprintf(&st, `<w:style w:type="paragraph" w:styleId="s%d"><w:name w:val="s%d"/><w:basedOn w:val="%s"/></w:style>`, i, i, base)
		}
		fmt.Fprintf(&st, `<w:style w:type="paragraph" w:styleId="s%d"><w:name w:val="heading 3"/></w:style>`, n)
		st.WriteString(`<w:style w:type="paragraph" w:styleId="a"><w:name w:val="a"/><w:basedOn w:val="b"/></w:style>` +
			`<w:style w:type="paragraph" w:styleId="b"><w:name w:val="b"/><w:basedOn w:val="a"/></w:style></w:styles>`)
		style := func(id string) string { return `<w:pPr><w:pStyle w:val="` + id + `"/></w:pPr>` }
		return pack(t,
			"_rels/.rels", links("rId1", "officeDocument", "word/document.xml"),
			"word/_rels/document.xml.rels", links("rId1", "styles", "styles.xml"),
			"word/styles.xml", st.String(),
			"word/document.xml", `<w:document `+nsW+`><w:body>`+
				para("w", style("s0"), "Deep")+para("w", style("a"), "Loop")+`</w:body></w:document>`)
	}
	loose, chained := styles(false), styles(true)
	timed := func(z string) time.Duration {
		start := time.Now()
		read(t, Docx{}, doc("x.docx", z))
		return time.Since(start)
	}
	flat, chain := time.Duration(math.MaxInt64), time.Duration(math.MaxInt64)
	for range 3 {
		flat, chain = min(flat, timed(loose)), min(chain, timed(chained))
	}
	if chain > 10*flat {
		t.Errorf("%d styles in a chain took %v, and unchained %v", n, chain, flat)
	}
	if secs := Sections(read(t, Docx{}, doc("x.docx", chained))); len(secs) != 1 ||
		secs[0].Title != "Deep" || secs[0].Level != 3 {
		t.Errorf("sections = %+v, want Deep at level 3 and Loop as body text", secs)
	}
}
