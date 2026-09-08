package parse

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"

	"github.com/hanzoai/semantic"
)

// Email reads a mail message. Its headers become the tags, decoded, so a
// subject written in another script arrives as words rather than as an
// encoded word; the subject becomes the title; the body becomes the text,
// one section per part, titled with the part's media type.
//
// A multipart/alternative holds one message written more than once, so only
// its plainest part is read. An HTML part is read as HTML, which keeps its
// markup out of the text and puts its link targets in Links. Parts that are
// not text — the attachments — are not read: their bytes are a different
// document, and ingest is what turns a source into one.
type Email struct{}

// Parse reads the message's headers and the text of its body.
func (Email) Parse(ctx context.Context, d semantic.Doc) (semantic.Doc, error) {
	src, err := mail.ReadMessage(strings.NewReader(lines(d.Text)))
	if err != nil {
		return d, fmt.Errorf("parse email: %w", err)
	}

	var w mime.WordDecoder
	tags := make(map[string]string, len(src.Header))
	for k, vs := range src.Header {
		v := strings.Join(vs, ", ")
		if s, err := w.DecodeHeader(v); err == nil {
			v = s
		}
		tags[strings.ToLower(k)] = v
	}

	var m msg
	if err := m.part(ctx, tags["content-type"], tags["content-transfer-encoding"], src.Body); err != nil {
		return d, fmt.Errorf("parse email: %w", err)
	}
	m.out.trim()

	kv := map[string]any{"format": "email", "tags": tags}
	if s := tags["subject"]; s != "" {
		kv["title"] = s
	}
	if secs := tile(m.secs, m.out.len()); len(secs) > 0 {
		kv["sections"] = secs
	}
	if len(m.links) > 0 {
		kv["links"] = m.links
	}
	return with(d, m.out.text(), kv), nil
}

// msg is one message being read: the text taken from its parts, the sections
// they became, and the targets their markup pointed at.
type msg struct {
	out   buf
	secs  []Section
	links []Link
}

// part reads one entity: a multipart entity part by part, a text entity into
// the message's text, anything else not at all.
func (m *msg) part(ctx context.Context, typ, enc string, r io.Reader) error {
	kind, par, err := mime.ParseMediaType(typ)
	if err != nil {
		kind = "text/plain" // what a message without a content type is
	}
	switch {
	case strings.HasPrefix(kind, "multipart/"):
		return m.split(ctx, kind, par["boundary"], r)
	case !strings.HasPrefix(kind, "text/"):
		return nil
	}

	b, err := decode(enc, r)
	if err != nil {
		return err
	}
	s := string(b)
	if kind == "text/html" {
		h, err := HTML{}.Parse(ctx, semantic.Doc{Text: s})
		if err != nil {
			return err
		}
		s = h.Text
		m.links = append(m.links, Links(h)...)
	}
	s = strings.TrimSpace(lines(s))
	if s == "" {
		return nil
	}
	start := m.out.len()
	m.out.raw(s)
	m.out.nl()
	m.secs = append(m.secs, Section{Title: kind, Start: start, End: m.out.len()})
	return nil
}

// split reads a multipart entity. An alternative carries one message written
// several ways, so the plainest of its parts stands for all of them; every
// other multipart carries different content in each part, so each is read.
func (m *msg) split(ctx context.Context, kind, edge string, r io.Reader) error {
	if edge == "" {
		return fmt.Errorf("%s without a boundary", kind)
	}
	type piece struct {
		typ  string
		enc  string
		body []byte
	}
	var ps []piece
	mr := multipart.NewReader(r, edge)
	for {
		p, err := mr.NextPart()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
		b, err := io.ReadAll(p)
		p.Close()
		if err != nil {
			return err
		}
		ps = append(ps, piece{
			typ:  p.Header.Get("Content-Type"),
			enc:  p.Header.Get("Content-Transfer-Encoding"),
			body: b,
		})
	}

	read := func(p piece) error {
		return m.part(ctx, p.typ, p.enc, bytes.NewReader(p.body))
	}
	if kind != "multipart/alternative" {
		for _, p := range ps {
			if err := read(p); err != nil {
				return err
			}
		}
		return nil
	}
	for _, want := range []string{"text/plain", "text/", "multipart/"} {
		for _, p := range ps {
			if kind, _, err := mime.ParseMediaType(p.typ); err == nil && strings.HasPrefix(kind, want) {
				return read(p)
			}
		}
	}
	return nil
}

// decode undoes the transfer encoding a part was sent in. Quoted-printable
// parts arrive already decoded, mime/multipart having done it while reading.
func decode(enc string, r io.Reader) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(enc)) {
	case "base64":
		return io.ReadAll(base64.NewDecoder(base64.StdEncoding, r))
	case "quoted-printable":
		return io.ReadAll(quotedprintable.NewReader(r))
	}
	return io.ReadAll(r)
}

// header reports whether the text opens the way a message does: header lines,
// one of them a header only a message carries, and then a blank line.
func header(s string) bool {
	head, _, ok := strings.Cut(s, "\n\n")
	if !ok {
		return false
	}
	seen := false
	for _, line := range strings.Split(head, "\n") {
		if line == "" {
			return false
		}
		if line[0] == ' ' || line[0] == '\t' {
			continue // a folded continuation of the line above
		}
		k, _, ok := strings.Cut(line, ":")
		if !ok || k == "" || strings.ContainsAny(k, " \t") {
			return false
		}
		switch strings.ToLower(k) {
		case "from", "to", "subject", "date", "message-id", "received":
			seen = true
		}
	}
	return seen
}
