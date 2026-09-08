// Package ingest turns a reference — a path, a pattern, a URL, stdin — into
// documents, and records where every document came from.
//
// The readers here are the ones the standard library can serve honestly:
// files, directory trees, HTTP(S), and readers already in memory. Everything
// else (object stores, SaaS APIs, databases, message queues) is a driver:
// implement semantic.Ingester and put it in a Registry under its scheme. No
// source is faked.
//
// A document without its origin is not evidence, so every reader fills
// Doc.Meta with an Origin: the reference, byte offset, size, modification
// time, content hash and type. Read it back with OriginOf.
package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hanzoai/semantic"
)

// Errors this package reports. Test for them with errors.Is.
var (
	// ErrScheme means no reader is registered for the reference's scheme.
	ErrScheme = errors.New("ingest: no reader for scheme")
	// ErrEmpty means the reference was blank.
	ErrEmpty = errors.New("ingest: empty reference")
	// ErrSize means the source is larger than the reader was allowed to read.
	ErrSize = errors.New("ingest: source over size limit")
	// ErrBlocked means a fetch was aimed at an address it may not reach:
	// loopback, private, link-local or otherwise internal.
	ErrBlocked = errors.New("ingest: address blocked")
)

// Keys under which an Origin is stored in Doc.Meta. Unexported so there is
// one way to read them back: OriginOf.
const (
	keySize   = "size"
	keyMod    = "mod"
	keyHash   = "hash"
	keyType   = "type"
	keyMime   = "mime"
	keyAt     = "at"
	keyOffset = "offset"
	keyStatus = "status"
	keyRecord = "record"
	keyTitle  = "title"
)

// Origin is where a document's text came from. Readers write it into
// Doc.Meta so provenance survives any later stage that only copies the map.
type Origin struct {
	// Ref is the path or URL the text was read from.
	Ref string
	// Offset is the byte offset of this document within Ref. It is 0 for a
	// whole-source document and the start of the record for one row or line.
	Offset int64
	// Size is the number of bytes read from Ref.
	Size int64
	// Mod is the source's modification time, zero when the source has none.
	Mod time.Time
	// Hash is the SHA-256 of the bytes read, hex encoded.
	Hash string
	// Type is the source's kind: an extension such as "json" or "csv", or
	// what the leading bytes say it is.
	Type string
	// Mime is the media type when one is known.
	Mime string
	// Status is the HTTP status of a fetch, 0 for sources that have none.
	Status int
	// At is when the read happened.
	At time.Time
}

// Meta renders the origin as the map a Doc carries.
func (o Origin) Meta() map[string]any {
	m := map[string]any{keySize: o.Size}
	if o.Offset != 0 {
		m[keyOffset] = o.Offset
	}
	if !o.Mod.IsZero() {
		m[keyMod] = o.Mod
	}
	if o.Hash != "" {
		m[keyHash] = o.Hash
	}
	if o.Type != "" {
		m[keyType] = o.Type
	}
	if o.Mime != "" {
		m[keyMime] = o.Mime
	}
	if o.Status != 0 {
		m[keyStatus] = o.Status
	}
	if !o.At.IsZero() {
		m[keyAt] = o.At
	}
	return m
}

// OriginOf reads back the origin a reader recorded on d. Values that have
// been through JSON — where an int becomes a float and a time becomes a
// string — are understood, so a document reloaded from disk still reports
// where it came from.
func OriginOf(d semantic.Doc) Origin {
	o := Origin{Ref: d.Source}
	o.Offset = num(d.Meta[keyOffset])
	o.Size = num(d.Meta[keySize])
	o.Mod = when(d.Meta[keyMod])
	o.At = when(d.Meta[keyAt])
	o.Hash, _ = d.Meta[keyHash].(string)
	o.Type, _ = d.Meta[keyType].(string)
	o.Mime, _ = d.Meta[keyMime].(string)
	o.Status = int(num(d.Meta[keyStatus]))
	return o
}

// Record returns the structured record a document was built from — one JSON
// object, one JSONL line, one CSV row — or nil when the document is plain
// text.
func Record(d semantic.Doc) map[string]any {
	r, _ := d.Meta[keyRecord].(map[string]any)
	return r
}

func num(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	case string:
		i, _ := strconv.ParseInt(n, 10, 64)
		return i
	}
	return 0
}

func when(v any) time.Time {
	switch t := v.(type) {
	case time.Time:
		return t
	case string:
		p, err := time.Parse(time.RFC3339Nano, t)
		if err != nil {
			return time.Time{}
		}
		return p
	}
	return time.Time{}
}

// Func adapts a plain function to semantic.Ingester, so a driver can be one
// function rather than a type.
type Func func(context.Context, string) ([]semantic.Doc, error)

// Ingest calls f.
func (f Func) Ingest(ctx context.Context, ref string) ([]semantic.Doc, error) {
	return f(ctx, ref)
}

// Reader reads everything from R as one source. A nil R reads standard
// input, which makes the zero value the stdin reader.
type Reader struct {
	// R is the stream to read. Nil means os.Stdin.
	R io.Reader
	// Source names the origin. Empty falls back to the reference passed to
	// Ingest, then to "stdin".
	Source string
	// Type says how to read the bytes ("json", "csv", …) when Decode is nil.
	Type string
	// Decode turns the bytes into documents. Nil picks a decoder from Type.
	Decode Decoder
}

// Ingest reads R to the end and decodes it. The reference is used only to
// name the origin when Source is empty.
func (r Reader) Ingest(ctx context.Context, ref string) ([]semantic.Doc, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	src := r.R
	name := r.Source
	if src == nil {
		src = os.Stdin
		if name == "" {
			name = "stdin"
		}
	}
	if name == "" {
		name = ref
	}
	if name == "" {
		name = "reader"
	}
	b, err := io.ReadAll(src)
	if err != nil {
		return nil, fmt.Errorf("ingest: read %s: %w", name, err)
	}
	o := Origin{
		Ref:  name,
		Size: int64(len(b)),
		Hash: sum(b),
		Type: kind(name, b),
		Mime: media(name),
		At:   time.Now(),
	}
	if r.Type != "" {
		o.Type = r.Type
	}
	return decode(r.Decode, o).Decode(b, o)
}

// Text treats the reference itself as the document body, for text already in
// hand rather than at a location.
type Text struct {
	// Source names the origin, "text" when empty.
	Source string
	// Type says how to read the text ("json", "csv", …) when Decode is nil.
	Type string
	// Decode turns the text into documents. Nil picks a decoder from Type.
	Decode Decoder
}

// Ingest decodes s as the whole source.
func (t Text) Ingest(ctx context.Context, s string) ([]semantic.Doc, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name := t.Source
	if name == "" {
		name = "text"
	}
	b := []byte(s)
	o := Origin{
		Ref:  name,
		Size: int64(len(b)),
		Hash: sum(b),
		Type: t.Type,
		At:   time.Now(),
	}
	if o.Type == "" {
		o.Type = kind(name, b)
	}
	return decode(t.Decode, o).Decode(b, o)
}

// doc builds one document. n is the record's position in the source, or -1
// when the document is the whole source.
func doc(o Origin, n int, body string) semantic.Doc {
	id := o.Ref
	if n >= 0 {
		id = fmt.Sprintf("%s#%d", o.Ref, n)
	}
	return semantic.Doc{ID: id, Source: o.Ref, Text: body, Meta: o.Meta()}
}

// text decodes b as UTF-8, falling back to Latin-1 when it is not valid
// UTF-8, so a byte sequence never silently becomes replacement characters.
func text(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c)
	}
	return string(r)
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// media is the registered media type for a name's extension, if any.
func media(name string) string {
	t := mime.TypeByExtension(filepath.Ext(name))
	if i := strings.IndexByte(t, ';'); i >= 0 {
		t = strings.TrimSpace(t[:i])
	}
	return t
}

// kind names the source's type: its extension, or what the leading bytes
// say when there is no extension.
func kind(name string, b []byte) string {
	if ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), ".")); ext != "" {
		return ext
	}
	if t := magic(b); t != "" {
		return t
	}
	return "unknown"
}

// magic identifies a format from its leading bytes.
func magic(b []byte) string {
	if len(b) < 4 {
		return ""
	}
	for _, m := range []struct {
		head []byte
		typ  string
	}{
		{[]byte("%PDF"), "pdf"},
		{[]byte("PK\x03\x04"), "zip"},
		{[]byte("\x89PNG"), "png"},
		{[]byte("\xff\xd8\xff"), "jpg"},
		{[]byte("GIF8"), "gif"},
		{[]byte("PAR1"), "parquet"},
		{[]byte("ARROW1\x00\x00"), "arrow"},
	} {
		if len(b) >= len(m.head) && string(b[:len(m.head)]) == string(m.head) {
			return m.typ
		}
	}
	return ""
}
