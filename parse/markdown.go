package parse

import (
	"context"
	"strings"

	"github.com/hanzoai/semantic"
)

// Markdown reads Markdown. The text it returns is the source with any front
// matter removed — nothing is rewritten, so a reader still sees the emphasis
// and the code the author wrote — and the structure the source declares comes
// back in Meta: a section per heading, the links and images, and the front
// matter as tags.
//
// Headings and links inside a fenced code block are code, not structure, and
// are ignored.
type Markdown struct{}

// Parse splits the document at its headings and collects its links.
func (Markdown) Parse(_ context.Context, d semantic.Doc) (semantic.Doc, error) {
	src := lines(d.Text)
	tags, src := matter(src)

	var (
		secs  []Section
		links []ref
		imgs  []ref
		defs  = map[string]string{}
		fence string
		prev  string
		at    int
	)
	for _, start := range starts(src) {
		line := src[start:]
		if n := strings.IndexByte(line, '\n'); n >= 0 {
			line = line[:n]
		}
		text := strings.TrimLeft(line, " \t")
		indent := len(line) - len(text)

		switch {
		case fence != "":
			if strings.HasPrefix(text, fence) {
				fence = ""
			}
			prev, at = "", start
			continue
		case open(text) != "":
			fence = open(text)
			prev, at = "", start
			continue
		case indent < 4 && strings.HasPrefix(text, "#"):
			if level, title := atx(text); level > 0 {
				secs = append(secs, Section{Title: title, Level: level, Start: start})
				prev, at = "", start
				continue
			}
		case indent < 4 && prev != "" && under(text) != 0:
			secs = append(secs, Section{Title: strings.TrimSpace(prev), Level: under(text), Start: at})
			prev, at = "", start
			continue
		case indent < 4 && define(text, defs):
			prev, at = "", start
			continue
		}

		scan(line, &links, &imgs)
		prev, at = text, start
	}

	secs = tile(secs, len(src))
	kv := map[string]any{"format": "markdown"}
	if len(tags) > 0 {
		kv["tags"] = tags
	}
	if len(secs) > 0 {
		kv["sections"] = secs
	}
	if ls := deref(links, defs); len(ls) > 0 {
		kv["links"] = ls
	}
	if ls := deref(imgs, defs); len(ls) > 0 {
		kv["images"] = ls
	}
	if t := title(secs, tags); t != "" {
		kv["title"] = t
	}
	return with(d, src, kv), nil
}

// title is the first level-one heading, or the front matter's own title.
func title(secs []Section, tags map[string]string) string {
	for _, s := range secs {
		if s.Level == 1 && s.Title != "" {
			return s.Title
		}
	}
	return tags["title"]
}

// matter takes the YAML front matter off the top of a document. Only scalar
// "key: value" lines are read; anything else in the block is dropped with it.
func matter(s string) (map[string]string, string) {
	if !strings.HasPrefix(s, "---\n") {
		return nil, s
	}
	rest := s[4:]
	end, closed := len(rest), false
	for _, start := range starts(rest) {
		line := rest[start:]
		if n := strings.IndexByte(line, '\n'); n >= 0 {
			line = line[:n]
		}
		if t := strings.TrimRight(line, " \t"); t == "---" || t == "..." {
			end, closed = start, true
			break
		}
	}
	if !closed {
		// An opening delimiter with no closing one is a thematic break, not
		// front matter, and the document keeps every line.
		return nil, s
	}
	tags := map[string]string{}
	for _, line := range strings.Split(rest[:end], "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(k) == "" {
			continue
		}
		tags[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	body := rest[end:]
	if n := strings.IndexByte(body, '\n'); n >= 0 {
		body = body[n+1:]
	} else {
		body = ""
	}
	return tags, body
}

// open returns the fence a line opens, or "" if it opens none.
func open(line string) string {
	for _, c := range []byte{'`', '~'} {
		n := 0
		for n < len(line) && line[n] == c {
			n++
		}
		if n >= 3 {
			return line[:n]
		}
	}
	return ""
}

// atx reads a "## Heading" line: its level and its text.
func atx(line string) (int, string) {
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	if n < 1 || n > 6 {
		return 0, ""
	}
	if n == len(line) {
		return n, ""
	}
	if line[n] != ' ' && line[n] != '\t' {
		return 0, ""
	}
	return n, strings.TrimRight(strings.TrimSpace(line[n:]), "# ")
}

// under reads a setext underline: === is level one, --- is level two.
func under(line string) int {
	t := strings.TrimRight(line, " \t")
	if t == "" {
		return 0
	}
	if strings.Trim(t, "=") == "" {
		return 1
	}
	if strings.Trim(t, "-") == "" && len(t) > 1 {
		return 2
	}
	return 0
}

// define reads a "[label]: url" line into defs.
func define(line string, defs map[string]string) bool {
	if !strings.HasPrefix(line, "[") {
		return false
	}
	k := strings.Index(line, "]:")
	if k < 1 {
		return false
	}
	dest := strings.TrimSpace(line[k+2:])
	if i := strings.IndexAny(dest, " \t"); i >= 0 {
		dest = dest[:i]
	}
	defs[strings.ToLower(line[1:k])] = strings.Trim(dest, "<>")
	return true
}

// ref is a link whose target may still be a reference to a definition.
type ref struct {
	Link
	label string
}

// deref resolves reference links against the definitions, dropping the ones
// that name a label the document never defined.
func deref(rs []ref, defs map[string]string) []Link {
	var out []Link
	for _, r := range rs {
		if r.label != "" {
			u, ok := defs[strings.ToLower(r.label)]
			if !ok {
				continue
			}
			r.URL = u
		}
		out = append(out, r.Link)
	}
	return out
}

// scan collects the links and images on one line, inline, reference and
// autolink alike.
func scan(line string, links, imgs *[]ref) {
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '<':
			j := strings.IndexByte(line[i:], '>')
			if j < 2 {
				continue
			}
			u := line[i+1 : i+j]
			if strings.Contains(u, "://") || strings.HasPrefix(u, "mailto:") {
				*links = append(*links, ref{Link: Link{Text: u, URL: u}})
				i += j
			}
		case '[':
			img := i > 0 && line[i-1] == '!'
			depth, j := 1, i+1
			for ; j < len(line) && depth > 0; j++ {
				switch line[j] {
				case '[':
					depth++
				case ']':
					depth--
				}
			}
			if depth != 0 {
				continue
			}
			text, r := line[i+1:j-1], ref{}
			switch {
			case j < len(line) && line[j] == '(':
				k := strings.IndexByte(line[j:], ')')
				if k < 0 {
					continue
				}
				dest := strings.TrimSpace(line[j+1 : j+k])
				if sp := strings.IndexAny(dest, " \t"); sp >= 0 {
					dest = dest[:sp]
				}
				r = ref{Link: Link{Text: text, URL: strings.Trim(dest, "<>")}}
				j += k
			case j < len(line) && line[j] == '[':
				k := strings.IndexByte(line[j:], ']')
				if k < 0 {
					continue
				}
				label := line[j+1 : j+k]
				if label == "" {
					label = text
				}
				r = ref{Link: Link{Text: text}, label: label}
				j += k
			default:
				continue
			}
			if img {
				*imgs = append(*imgs, r)
			} else {
				*links = append(*links, r)
			}
			i = j
		}
	}
}
