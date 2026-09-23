package parse

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/hanzoai/semantic"
)

// Xlsx reads a SpreadsheetML workbook (.xlsx): one section per sheet, in the
// workbook's order, titled and headed by the sheet's name. The first row with
// a value names the columns and every row after it is written as a record of
// "column: value" lines, as CSV is, so a sheet is something a splitter and an
// extractor can work on.
//
// A cell is written as the value it holds, not as it is displayed: shared and
// inline strings as their text, a number as stored, a boolean as TRUE or
// FALSE. A number in a date or time format is the date it stands for, written
// in RFC 3339's forms: full-date, partial-time, or the two joined by "T". A
// workbook records no zone for its dates, so none is written rather than one
// invented. A format that counts elapsed time, [h]:mm, is a duration and not
// a date, and its number is left as it is.
type Xlsx struct{}

// Parse reads every sheet of the workbook.
func (Xlsx) Parse(_ context.Context, d semantic.Doc) (semantic.Doc, error) {
	z, err := unzip(d.Text)
	if err != nil {
		return d, fmt.Errorf("parse xlsx: %w", err)
	}
	main, core, err := begin(z)
	if err != nil {
		return d, fmt.Errorf("parse xlsx: %w", err)
	}
	wb, err := part[struct {
		Pr struct {
			Date1904 string `xml:"date1904,attr"`
		} `xml:"workbookPr"`
		Sheets []struct {
			Name string     `xml:"name,attr"`
			Attr []xml.Attr `xml:",any,attr"`
		} `xml:"sheets>sheet"`
	}](z, main)
	if err != nil {
		return d, fmt.Errorf("parse xlsx: %w", err)
	}
	r, err := related(z, main)
	if err != nil {
		return d, fmt.Errorf("parse xlsx: %w", err)
	}
	shared, err := pool(z, r.kind("sharedStrings"))
	if err != nil {
		return d, fmt.Errorf("parse xlsx: %w", err)
	}
	looks, err := styled(z, r.kind("styles"))
	if err != nil {
		return d, fmt.Errorf("parse xlsx: %w", err)
	}
	tags, err := props(z, core)
	if err != nil {
		return d, fmt.Errorf("parse xlsx: %w", err)
	}
	b := book{shared: shared, looks: looks}
	b.from1904 = wb.Pr.Date1904 == "1" || strings.EqualFold(wb.Pr.Date1904, "true")

	var (
		out  buf
		secs []Section
	)
	for _, sh := range wb.Sheets {
		rows, err := b.sheet(z, r.id(rid(sh.Attr)))
		if err != nil {
			return d, fmt.Errorf("parse xlsx: sheet %q: %w", sh.Name, err)
		}
		if len(rows) == 0 {
			continue
		}
		out.gap()
		secs = append(secs, Section{Title: sh.Name, Level: 1, Start: out.len()})
		out.put(sh.Name)
		out.nl()
		grid(&out, rows)
	}
	out.trim()

	kv := map[string]any{"format": "xlsx"}
	if secs = tile(secs, out.len()); len(secs) > 0 {
		kv["sections"] = secs
	}
	if len(tags) > 0 {
		kv["tags"] = tags
		if t := tags["title"]; t != "" {
			kv["title"] = t
		}
	}
	return with(d, out.text(), kv), nil
}

// book is what reading a sheet's cells needs from the rest of the workbook.
type book struct {
	shared   []string
	looks    []shows // by cell style index
	from1904 bool
}

// rich is a string as a workbook stores it: plain, or in runs, each with its
// own formatting and text. Phonetic guides are not the string, and are not
// read.
type rich struct {
	T string `xml:"t"`
	R []struct {
		T string `xml:"t"`
	} `xml:"r"`
}

func (r rich) String() string {
	var b strings.Builder
	b.WriteString(r.T)
	for _, x := range r.R {
		b.WriteString(x.T)
	}
	return b.String()
}

// pool reads the shared string table. A workbook without one has none.
func pool(z *zip.Reader, name string) ([]string, error) {
	t, err := part[struct {
		SI []rich `xml:"si"`
	}](z, name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]string, len(t.SI))
	for i, s := range t.SI {
		out[i] = s.String()
	}
	return out, nil
}

// shows is which parts of a date a number format displays. Neither means the
// format is not a date's.
type shows struct{ date, time bool }

// styled reads what each cell style displays. A workbook without styles
// shows every number as a number.
func styled(z *zip.Reader, name string) ([]shows, error) {
	st, err := part[struct {
		Fmts []struct {
			ID   int    `xml:"numFmtId,attr"`
			Code string `xml:"formatCode,attr"`
		} `xml:"numFmts>numFmt"`
		Xfs []struct {
			Fmt int `xml:"numFmtId,attr"`
		} `xml:"cellXfs>xf"`
	}](z, name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	codes := map[int]string{}
	for _, f := range st.Fmts {
		codes[f.ID] = f.Code
	}
	out := make([]shows, len(st.Xfs))
	for i, x := range st.Xfs {
		if code, ok := codes[x.Fmt]; ok {
			out[i] = custom(code)
		} else {
			out[i] = builtin(x.Fmt)
		}
	}
	return out, nil
}

// builtin is what one of ECMA-376's built-in number formats displays: 14 to 22
// are the dates and times every locale has, and 27 to 36 and 50 to 58 the ones
// East Asian locales add. 45 to 47 count minutes and seconds, which is a
// duration as often as a time, and are left as numbers.
func builtin(id int) shows {
	switch {
	case id >= 14 && id <= 17, id >= 27 && id <= 31, id >= 34 && id <= 36, id >= 50 && id <= 58:
		return shows{date: true}
	case id >= 18 && id <= 21, id == 32, id == 33:
		return shows{time: true}
	case id == 22:
		return shows{date: true, time: true}
	}
	return shows{}
}

// custom is what a custom number format displays, from the tokens left once the
// literals are gone: quoted text, bracketed colours and locales, and escaped
// characters. y and d are dates, h and s are times, and m is minutes beside an
// h or an s and months otherwise. A bracketed [h], [m] or [s] counts elapsed
// time, and makes the format a duration's. Only the first section, the one for
// positive numbers, is read.
func custom(code string) shows {
	low := strings.ToLower(code)
	for _, e := range []string{"[h", "[m", "[s"} {
		if strings.Contains(low, e) {
			return shows{}
		}
	}
	var y, d, h, s, m bool
	for i := 0; i < len(low); i++ {
		switch c := low[i]; c {
		case ';':
			i = len(low)
		case '"':
			if j := strings.IndexByte(low[i+1:], '"'); j >= 0 {
				i += j + 1
			} else {
				i = len(low)
			}
		case '[':
			if j := strings.IndexByte(low[i:], ']'); j >= 0 {
				i += j
			}
		case '\\', '_', '*':
			i++
		case 'y':
			y = true
		case 'd':
			d = true
		case 'h':
			h = true
		case 's':
			s = true
		case 'm':
			m = true
		}
	}
	return shows{date: y || d || (m && !h && !s), time: h || s}
}

// sheet reads a worksheet's rows as sparse cells, in order.
func (b book) sheet(z *zip.Reader, name string) ([][]cell, error) {
	ws, err := part[struct {
		Rows []struct {
			Cells []struct {
				Ref string `xml:"r,attr"`
				T   string `xml:"t,attr"`
				S   int    `xml:"s,attr"`
				V   string `xml:"v"`
				Is  rich   `xml:"is"`
			} `xml:"c"`
		} `xml:"sheetData>row"`
	}](z, name)
	if err != nil {
		return nil, err
	}
	var rows [][]cell
	for _, r := range ws.Rows {
		var row []cell
		col := -1
		for _, c := range r.Cells {
			if n, ok := column(c.Ref); ok {
				col = n
			} else {
				col++
			}
			v, err := b.value(c.T, c.S, c.V, c.Is)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", c.Ref, err)
			}
			if strings.TrimSpace(v) != "" {
				row = append(row, cell{col: col, text: v})
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// value is the text of one cell, by its type.
func (b book) value(typ string, style int, v string, inline rich) (string, error) {
	switch typ {
	case "s":
		i, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || i < 0 || i >= len(b.shared) {
			return "", fmt.Errorf("shared string %q of %d", v, len(b.shared))
		}
		return b.shared[i], nil
	case "inlineStr":
		return inline.String(), nil
	case "b":
		switch strings.TrimSpace(v) {
		case "1":
			return "TRUE", nil
		case "0":
			return "FALSE", nil
		}
		return v, nil
	case "", "n":
		if style >= 0 && style < len(b.looks) && b.looks[style] != (shows{}) {
			if t, ok := serial(v, b.looks[style], b.from1904); ok {
				return t, nil
			}
		}
	}
	return v, nil // str, e and d are text already, and so is a plain number
}

// column is the zero-based column of a cell reference: A1 is 0, AB7 is 27.
func column(ref string) (int, bool) {
	n, i := 0, 0
	for ; i < len(ref); i++ {
		c := ref[i] | 0x20 // lower case
		if c < 'a' || c > 'z' {
			break
		}
		n = n*26 + int(c-'a'+1)
		if n > 1<<14 {
			return 0, false // past XFD, the last column there is
		}
	}
	if i == 0 {
		return 0, false
	}
	return n - 1, true
}

// latest is the serial of 31 December 9999, the last day a workbook can hold.
const latest = 2958465

// serial writes the date a serial number stands for, or reports that it stands
// for none: a negative serial, one past the last day, or 29 February 1900,
// which the 1900 date system counts — Lotus 1-2-3 thought 1900 a leap year,
// and Excel kept the error — and the calendar does not. Days before it are
// counted from 31 December 1899 and days after it from the 30th, which is
// what makes every other serial land on the right day. A serial is rounded to
// the millisecond, the finest a workbook resolves.
func serial(v string, w shows, from1904 bool) (string, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || f < 0 || f >= latest+1 || math.IsNaN(f) {
		return "", false
	}
	ms := int64(math.Round(f * 86400000))
	days, rest := ms/86400000, ms%86400000
	clock := time.UnixMilli(rest).UTC().Format("15:04:05.999")

	var base time.Time
	switch {
	case from1904:
		base = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)
	case days == 0:
		// Day zero of the 1900 system is 0 January 1900: a time of day with
		// no date, and nothing at all without one.
		if !w.time && rest == 0 {
			return "", false
		}
		return clock, true
	case days == 60:
		return "", false
	case days < 60:
		base = time.Date(1899, 12, 31, 0, 0, 0, 0, time.UTC)
	default:
		base = time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	}
	if !w.date && days == 0 {
		return clock, true
	}
	day := base.AddDate(0, 0, int(days)).Format("2006-01-02")
	if rest == 0 {
		return day, true
	}
	return day + "T" + clock, true
}
