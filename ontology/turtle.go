package ontology

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Statement is one RDF triple in lexical form. Subject and Predicate are IRIs
// or blank node labels; Object is one of those unless Literal is set, in
// which case it is the lexical value and Datatype or Lang describes it.
type Statement struct {
	Subject   string
	Predicate string
	Object    string
	Literal   bool
	Datatype  string
	Lang      string
}

// Vocabulary terms this package reads and writes.
const (
	rdfType   = "http://www.w3.org/1999/02/22-rdf-syntax-ns#type"
	rdfFirst  = "http://www.w3.org/1999/02/22-rdf-syntax-ns#first"
	rdfRest   = "http://www.w3.org/1999/02/22-rdf-syntax-ns#rest"
	rdfNil    = "http://www.w3.org/1999/02/22-rdf-syntax-ns#nil"
	rdfProp   = "http://www.w3.org/1999/02/22-rdf-syntax-ns#Property"
	rdfsLabel = "http://www.w3.org/2000/01/rdf-schema#label"
	rdfsCmt   = "http://www.w3.org/2000/01/rdf-schema#comment"
	rdfsSub   = "http://www.w3.org/2000/01/rdf-schema#subClassOf"
	rdfsDom   = "http://www.w3.org/2000/01/rdf-schema#domain"
	rdfsRange = "http://www.w3.org/2000/01/rdf-schema#range"
	rdfsClass = "http://www.w3.org/2000/01/rdf-schema#Class"
	owlOnt    = "http://www.w3.org/2002/07/owl#Ontology"
	owlClass  = "http://www.w3.org/2002/07/owl#Class"
	owlObject = "http://www.w3.org/2002/07/owl#ObjectProperty"
	owlData   = "http://www.w3.org/2002/07/owl#DatatypeProperty"
	owlAnnot  = "http://www.w3.org/2002/07/owl#AnnotationProperty"
	owlVer    = "http://www.w3.org/2002/07/owl#versionInfo"
	xsdNS     = "http://www.w3.org/2001/XMLSchema#"
)

// Statements parses Turtle into triples. N-Triples is a subset of the same
// grammar, so it parses too and there is no second entry point for it. The
// parser is deliberately tolerant: it reads the constructs a serializer emits
// — prefixes, predicate and object lists, blank node property lists,
// collections, typed and tagged literals — and reports where it gives up.
func Statements(src []byte) ([]Statement, error) {
	p, err := read(src)
	if err != nil {
		return nil, err
	}
	return p.out, nil
}

// read runs the parser over a document once, so a caller that wants the
// prefix bindings as well as the triples gets both from the same pass.
func read(src []byte) (*turtle, error) {
	p := &turtle{src: src, prefix: map[string]string{}}
	if err := p.document(); err != nil {
		return nil, err
	}
	return p, nil
}

type turtle struct {
	src    []byte
	pos    int
	line   int
	base   string
	prefix map[string]string
	blanks int
	out    []Statement
}

type node struct {
	text string
	lit  bool
	dt   string
	lang string
}

func (p *turtle) errf(format string, a ...any) error {
	return fmt.Errorf("turtle: line %d: %s", p.line+1, fmt.Sprintf(format, a...))
}

func (p *turtle) document() error {
	for {
		p.space()
		if p.pos >= len(p.src) {
			return nil
		}
		switch {
		case p.peek('@'), p.word("PREFIX"), p.word("BASE"):
			if err := p.directive(); err != nil {
				return err
			}
		default:
			if err := p.triples(); err != nil {
				return err
			}
		}
	}
}

func (p *turtle) directive() error {
	dot := true
	var kind string
	switch {
	case p.take("@prefix"):
		kind = "prefix"
	case p.take("@base"):
		kind = "base"
	case p.take("PREFIX"):
		kind, dot = "prefix", false
	case p.take("BASE"):
		kind, dot = "base", false
	default:
		return p.errf("unknown directive")
	}
	p.space()
	if kind == "prefix" {
		name := p.readUntil(':')
		if !p.take(":") {
			return p.errf("prefix name must end in a colon")
		}
		p.space()
		iri, err := p.iri()
		if err != nil {
			return err
		}
		p.prefix[strings.TrimSpace(name)] = iri
	} else {
		iri, err := p.iri()
		if err != nil {
			return err
		}
		p.base = iri
	}
	p.space()
	if dot && !p.take(".") {
		return p.errf("directive must end in a full stop")
	}
	return nil
}

func (p *turtle) triples() error {
	p.space()
	var subj node
	if p.peek('[') {
		b, err := p.blankList()
		if err != nil {
			return err
		}
		subj = b
		p.space()
		if p.take(".") {
			return nil
		}
	} else {
		s, err := p.term()
		if err != nil {
			return err
		}
		subj = s
	}
	if err := p.predicates(subj); err != nil {
		return err
	}
	p.space()
	if !p.take(".") {
		return p.errf("statement must end in a full stop")
	}
	return nil
}

func (p *turtle) predicates(subj node) error {
	for {
		p.space()
		if p.pos >= len(p.src) || p.peek('.') || p.peek(']') {
			return nil
		}
		var pred node
		if p.takeWord("a") {
			pred = node{text: rdfType}
		} else {
			t, err := p.term()
			if err != nil {
				return err
			}
			pred = t
		}
		for {
			p.space()
			obj, err := p.object()
			if err != nil {
				return err
			}
			p.emit(subj, pred, obj)
			p.space()
			if !p.take(",") {
				break
			}
		}
		p.space()
		if !p.take(";") {
			return nil
		}
	}
}

func (p *turtle) object() (node, error) {
	p.space()
	switch {
	case p.peek('['):
		return p.blankList()
	case p.peek('('):
		return p.collection()
	}
	return p.term()
}

// blankList reads "[ predicate object ; … ]" and returns the blank node the
// enclosed statements were made about.
func (p *turtle) blankList() (node, error) {
	if !p.take("[") {
		return node{}, p.errf("expected [")
	}
	b := p.blank()
	if err := p.predicates(b); err != nil {
		return node{}, err
	}
	p.space()
	if !p.take("]") {
		return node{}, p.errf("unclosed blank node property list")
	}
	return b, nil
}

// collection reads "( a b c )" into the rdf:first/rdf:rest chain it stands for.
func (p *turtle) collection() (node, error) {
	if !p.take("(") {
		return node{}, p.errf("expected (")
	}
	var items []node
	for {
		p.space()
		if p.take(")") {
			break
		}
		if p.pos >= len(p.src) {
			return node{}, p.errf("unclosed collection")
		}
		it, err := p.object()
		if err != nil {
			return node{}, err
		}
		items = append(items, it)
	}
	if len(items) == 0 {
		return node{text: rdfNil}, nil
	}
	head := p.blank()
	cur := head
	for i, it := range items {
		p.emit(cur, node{text: rdfFirst}, it)
		next := node{text: rdfNil}
		if i+1 < len(items) {
			next = p.blank()
		}
		p.emit(cur, node{text: rdfRest}, next)
		cur = next
	}
	return head, nil
}

func (p *turtle) blank() node {
	p.blanks++
	return node{text: fmt.Sprintf("_:b%d", p.blanks)}
}

func (p *turtle) emit(s, pr, o node) {
	p.out = append(p.out, Statement{
		Subject:   s.text,
		Predicate: pr.text,
		Object:    o.text,
		Literal:   o.lit,
		Datatype:  o.dt,
		Lang:      o.lang,
	})
}

// term reads an IRI, a prefixed name, a blank node label or a literal.
func (p *turtle) term() (node, error) {
	p.space()
	if p.pos >= len(p.src) {
		return node{}, p.errf("unexpected end of input")
	}
	switch c := p.src[p.pos]; {
	case c == '<':
		iri, err := p.iri()
		return node{text: iri}, err
	case c == '"', c == '\'':
		return p.literal()
	case c == '_' && p.pos+1 < len(p.src) && p.src[p.pos+1] == ':':
		p.pos += 2
		return node{text: "_:" + p.readName()}, nil
	case c >= '0' && c <= '9', c == '+', c == '-', c == '.':
		return p.number()
	}
	if p.takeWord("true") {
		return node{text: "true", lit: true, dt: xsdNS + "boolean"}, nil
	}
	if p.takeWord("false") {
		return node{text: "false", lit: true, dt: xsdNS + "boolean"}, nil
	}
	return p.prefixed()
}

func (p *turtle) prefixed() (node, error) {
	start := p.pos
	name := p.readName()
	if !p.take(":") {
		p.pos = start
		return node{}, p.errf("expected an IRI, a prefixed name or a literal")
	}
	ns, ok := p.prefix[name]
	if !ok {
		if ns, ok = Standard[name]; !ok {
			return node{}, p.errf("prefix %q is not bound", name)
		}
	}
	return node{text: ns + p.readName()}, nil
}

func (p *turtle) iri() (string, error) {
	if !p.take("<") {
		return "", p.errf("expected <")
	}
	var b strings.Builder
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == '>' {
			p.pos++
			s := b.String()
			if p.base != "" && !absolute(s) && !strings.HasPrefix(s, "#") {
				s = p.base + s
			}
			return s, nil
		}
		if c == '\\' {
			r, err := p.escape()
			if err != nil {
				return "", err
			}
			b.WriteRune(r)
			continue
		}
		if c == '\n' {
			p.line++
		}
		b.WriteByte(c)
		p.pos++
	}
	return "", p.errf("unclosed IRI")
}

func (p *turtle) literal() (node, error) {
	quote := p.src[p.pos]
	long := p.pos+2 < len(p.src) && p.src[p.pos+1] == quote && p.src[p.pos+2] == quote
	end := string(quote)
	if long {
		end = strings.Repeat(string(quote), 3)
		p.pos += 3
	} else {
		p.pos++
	}
	var b strings.Builder
	closed := false
	for p.pos < len(p.src) {
		if strings.HasPrefix(string(p.src[p.pos:]), end) {
			p.pos += len(end)
			closed = true
			break
		}
		if p.src[p.pos] == '\\' {
			r, err := p.escape()
			if err != nil {
				return node{}, err
			}
			b.WriteRune(r)
			continue
		}
		if p.src[p.pos] == '\n' {
			p.line++
		}
		b.WriteByte(p.src[p.pos])
		p.pos++
	}
	if !closed {
		return node{}, p.errf("unclosed literal")
	}
	n := node{text: b.String(), lit: true}
	switch {
	case p.take("^^"):
		t, err := p.term()
		if err != nil {
			return node{}, err
		}
		n.dt = t.text
	case p.take("@"):
		n.lang = p.readName()
	}
	return n, nil
}

func (p *turtle) number() (node, error) {
	start := p.pos
	for p.pos < len(p.src) && strings.ContainsRune("+-0123456789.eE", rune(p.src[p.pos])) {
		p.pos++
	}
	text := strings.TrimRight(string(p.src[start:p.pos]), ".")
	p.pos = start + len(text)
	if text == "" {
		return node{}, p.errf("expected a number")
	}
	dt := xsdNS + "integer"
	if _, err := strconv.Atoi(text); err != nil {
		if _, err := strconv.ParseFloat(text, 64); err != nil {
			return node{}, p.errf("bad number %q", text)
		}
		dt = xsdNS + "decimal"
		if strings.ContainsAny(text, "eE") {
			dt = xsdNS + "double"
		}
	}
	return node{text: text, lit: true, dt: dt}, nil
}

func (p *turtle) escape() (rune, error) {
	p.pos++ // the backslash
	if p.pos >= len(p.src) {
		return 0, p.errf("trailing backslash")
	}
	c := p.src[p.pos]
	p.pos++
	switch c {
	case 't':
		return '\t', nil
	case 'n':
		return '\n', nil
	case 'r':
		return '\r', nil
	case 'b':
		return '\b', nil
	case 'f':
		return '\f', nil
	case 'u', 'U':
		n := 4
		if c == 'U' {
			n = 8
		}
		if p.pos+n > len(p.src) {
			return 0, p.errf("short unicode escape")
		}
		v, err := strconv.ParseUint(string(p.src[p.pos:p.pos+n]), 16, 32)
		if err != nil {
			return 0, p.errf("bad unicode escape")
		}
		p.pos += n
		return rune(v), nil
	}
	return rune(c), nil
}

func (p *turtle) space() {
	for p.pos < len(p.src) {
		switch c := p.src[p.pos]; c {
		case ' ', '\t', '\r':
			p.pos++
		case '\n':
			p.line++
			p.pos++
		case '#':
			for p.pos < len(p.src) && p.src[p.pos] != '\n' {
				p.pos++
			}
		default:
			return
		}
	}
}

func (p *turtle) peek(c byte) bool { return p.pos < len(p.src) && p.src[p.pos] == c }

func (p *turtle) take(s string) bool {
	if strings.HasPrefix(string(p.src[p.pos:]), s) {
		p.pos += len(s)
		return true
	}
	return false
}

// takeWord takes a keyword only when it is not the start of a longer name.
func (p *turtle) takeWord(s string) bool {
	if !strings.HasPrefix(string(p.src[p.pos:]), s) {
		return false
	}
	if n := p.pos + len(s); n < len(p.src) && (isWordRune(rune(p.src[n])) || p.src[n] == ':' || p.src[n] == '_') {
		return false
	}
	p.pos += len(s)
	return true
}

func (p *turtle) word(s string) bool { return strings.HasPrefix(string(p.src[p.pos:]), s) }

// readName reads a prefix or local name, including the characters Turtle
// allows inside one.
func (p *turtle) readName() string {
	start := p.pos
	for p.pos < len(p.src) {
		r, size := utf8.DecodeRune(p.src[p.pos:])
		if isWordRune(r) || r == '_' || r == '-' || r == '.' || r == '%' || r >= 0x80 {
			p.pos += size
			continue
		}
		break
	}
	return strings.TrimRight(string(p.src[start:p.pos]), ".")
}

func (p *turtle) readUntil(c byte) string {
	start := p.pos
	for p.pos < len(p.src) && p.src[p.pos] != c {
		p.pos++
	}
	return string(p.src[start:p.pos])
}

// ParseTurtle reads an OWL ontology out of a Turtle or N-Triples document:
// the classes, the properties, their labels, comments, domains, ranges and
// the subclass hierarchy. Terms are named by the local part of their IRI,
// which is the identity the rest of the package indexes by.
func ParseTurtle(src []byte) (Schema, error) {
	p, err := read(src)
	if err != nil {
		return Schema{}, err
	}
	s := FromStatements(p.out)
	s.Prefixes = bindings(p.prefix)
	return s, nil
}

// bindings are the prefixes a document declared that a reader would not have
// assumed: the standard ones are dropped, and so is the empty prefix, which
// names the document's own namespace rather than a vocabulary. No bindings is
// a nil map, so that every reader in this package says "none" the same way.
func bindings(declared map[string]string) map[string]string {
	var out map[string]string
	for name, iri := range declared {
		if name == "" || Standard[name] == iri {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[name] = iri
	}
	return out
}

// FromStatements builds a schema from triples already parsed.
func FromStatements(st []Statement) Schema {
	var s Schema
	value := func(subj, pred string) (string, bool) {
		for _, t := range st {
			if t.Subject == subj && t.Predicate == pred {
				return t.Object, true
			}
		}
		return "", false
	}
	all := func(subj, pred string) []string {
		var out []string
		for _, t := range st {
			if t.Subject == subj && t.Predicate == pred {
				out = append(out, t.Object)
			}
		}
		return out
	}

	var classOrder, propOrder []string
	classes := map[string]Class{}
	props := map[string]Property{}

	for _, t := range st {
		if t.Predicate != rdfType {
			continue
		}
		if strings.HasPrefix(t.Subject, "_:") {
			continue
		}
		switch t.Object {
		case owlOnt:
			if s.IRI == "" {
				s.IRI = t.Subject
				s.Name = local(t.Subject)
				if v, ok := value(t.Subject, rdfsLabel); ok {
					s.Name = v
				}
				if v, ok := value(t.Subject, rdfsCmt); ok {
					s.Comment = v
				}
				if v, ok := value(t.Subject, owlVer); ok {
					s.Version = v
				}
			}
		case owlClass, rdfsClass:
			if _, seen := classes[t.Subject]; seen {
				continue
			}
			classOrder = append(classOrder, t.Subject)
			c := Class{Name: local(t.Subject), IRI: t.Subject}
			if v, ok := value(t.Subject, rdfsLabel); ok {
				c.Label = v
			}
			if v, ok := value(t.Subject, rdfsCmt); ok {
				c.Comment = v
			}
			for _, parent := range all(t.Subject, rdfsSub) {
				if !strings.HasPrefix(parent, "_:") {
					c.Parent = local(parent)
					break
				}
			}
			classes[t.Subject] = c
		case owlObject, owlData, owlAnnot, rdfProp:
			kind := map[string]Kind{owlObject: Object, owlData: Data, owlAnnot: Annotation, rdfProp: Annotation}[t.Object]
			if cur, seen := props[t.Subject]; seen {
				// A more specific declaration wins over a bare rdf:Property.
				if cur.Kind == Annotation && kind != Annotation {
					cur.Kind = kind
					props[t.Subject] = cur
				}
				continue
			}
			propOrder = append(propOrder, t.Subject)
			p := Property{Name: local(t.Subject), IRI: t.Subject, Kind: kind}
			if v, ok := value(t.Subject, rdfsLabel); ok {
				p.Label = v
			}
			if v, ok := value(t.Subject, rdfsCmt); ok {
				p.Comment = v
			}
			for _, d := range all(t.Subject, rdfsDom) {
				p.Domain = add(p.Domain, shorten(d))
			}
			for _, r := range all(t.Subject, rdfsRange) {
				p.Range = add(p.Range, shorten(r))
			}
			props[t.Subject] = p
		}
	}

	for _, iri := range classOrder {
		s.Classes = append(s.Classes, classes[iri])
	}
	for _, iri := range propOrder {
		s.Properties = append(s.Properties, props[iri])
	}
	if s.Base == "" {
		s.Base = baseOf(classOrder, propOrder, s.IRI)
	}
	if s.Version == "" {
		s.Version = "1.0"
	}
	return s
}

// baseOf takes the namespace the terms were minted in from the terms
// themselves, which is more reliable than any declaration about them.
func baseOf(classOrder, propOrder []string, fallback string) string {
	for _, list := range [][]string{classOrder, propOrder} {
		for _, iri := range list {
			if i := strings.LastIndexAny(iri, "#/"); i >= 0 {
				return iri[:i+1]
			}
		}
	}
	return fallback
}

// shorten names a term the way the schema names it: a datatype keeps its
// prefix so it stays recognisable, anything else keeps its local name.
func shorten(iri string) string {
	switch {
	case strings.HasPrefix(iri, xsdNS):
		return "xsd:" + strings.TrimPrefix(iri, xsdNS)
	case strings.HasPrefix(iri, Standard["owl"]):
		return "owl:" + strings.TrimPrefix(iri, Standard["owl"])
	case strings.HasPrefix(iri, "xsd:"), strings.HasPrefix(iri, "owl:"):
		return iri
	}
	return local(iri)
}

// space is the namespace a schema mints in.
func (s Schema) space() Namespace {
	base := s.Base
	if base == "" {
		base = s.IRI
	}
	ns := Namespace{Base: base, Version: s.Version}
	for p, iri := range s.Prefixes {
		ns.Bind(p, iri)
	}
	return ns
}

// TermIRI is the IRI a name stands for in this schema: the one the term
// carries, or the one the namespace would mint for it.
func (s Schema) TermIRI(name string) string {
	if absolute(name) {
		return name
	}
	if i := strings.Index(name, ":"); i > 0 {
		if iri, ok := s.space().Resolve(name[:i]); ok {
			return iri + name[i+1:]
		}
	}
	for _, c := range s.Classes {
		if c.Name == name {
			if c.IRI != "" {
				return c.IRI
			}
			return s.space().Class(name)
		}
	}
	for _, p := range s.Properties {
		if p.Name == name {
			if p.IRI != "" {
				return p.IRI
			}
			// A property mints in camelCase. Minting it as a class would
			// rewrite worksFor to Worksfor and rename the term on the way
			// out of the document.
			return s.space().Property(name)
		}
	}
	return s.space().Class(name)
}

// Turtle writes the schema as an OWL ontology in Turtle.
func (s Schema) Turtle() string {
	ns := s.space()
	var b strings.Builder
	prefixes := map[string]string{
		"owl":  Standard["owl"],
		"rdf":  Standard["rdf"],
		"rdfs": Standard["rdfs"],
		"xsd":  Standard["xsd"],
	}
	for p, iri := range s.Prefixes {
		prefixes[p] = iri
	}
	fmt.Fprintf(&b, "@prefix : <%s> .\n", ns.Root())
	for _, p := range sorted(prefixes) {
		fmt.Fprintf(&b, "@prefix %s: <%s> .\n", p, prefixes[p])
	}
	b.WriteString("\n")

	if iri := s.IRI; iri != "" {
		lines := []string{fmt.Sprintf("<%s> a owl:Ontology", iri)}
		if s.Name != "" {
			lines = append(lines, fmt.Sprintf("    rdfs:label %s", quote(s.Name)))
		}
		if s.Comment != "" {
			lines = append(lines, fmt.Sprintf("    rdfs:comment %s", quote(s.Comment)))
		}
		if s.Version != "" {
			lines = append(lines, fmt.Sprintf("    owl:versionInfo %s", quote(s.Version)))
		}
		b.WriteString(strings.Join(lines, " ;\n") + " .\n\n")
	}

	for _, c := range s.Classes {
		lines := []string{fmt.Sprintf("<%s> a owl:Class", s.TermIRI(c.Name))}
		if c.Label != "" {
			lines = append(lines, fmt.Sprintf("    rdfs:label %s", quote(c.Label)))
		}
		if c.Comment != "" {
			lines = append(lines, fmt.Sprintf("    rdfs:comment %s", quote(c.Comment)))
		}
		if c.Parent != "" {
			lines = append(lines, fmt.Sprintf("    rdfs:subClassOf <%s>", s.TermIRI(c.Parent)))
		}
		b.WriteString(strings.Join(lines, " ;\n") + " .\n\n")
	}

	for _, p := range s.Properties {
		lines := []string{fmt.Sprintf("<%s> a %s", s.TermIRI(p.Name), owlKind(p.Kind))}
		if p.Label != "" {
			lines = append(lines, fmt.Sprintf("    rdfs:label %s", quote(p.Label)))
		}
		if p.Comment != "" {
			lines = append(lines, fmt.Sprintf("    rdfs:comment %s", quote(p.Comment)))
		}
		for _, d := range p.Domain {
			lines = append(lines, fmt.Sprintf("    rdfs:domain %s", s.turtleTerm(d)))
		}
		for _, r := range p.Range {
			lines = append(lines, fmt.Sprintf("    rdfs:range %s", s.turtleTerm(r)))
		}
		b.WriteString(strings.Join(lines, " ;\n") + " .\n\n")
	}
	return b.String()
}

// NTriples writes the schema one fully expanded triple to a line.
func (s Schema) NTriples() string {
	var b strings.Builder
	line := func(subj, pred, obj string) {
		fmt.Fprintf(&b, "<%s> <%s> %s .\n", subj, pred, obj)
	}
	if s.IRI != "" {
		line(s.IRI, rdfType, "<"+owlOnt+">")
		if s.Name != "" {
			line(s.IRI, rdfsLabel, quote(s.Name))
		}
		if s.Comment != "" {
			line(s.IRI, rdfsCmt, quote(s.Comment))
		}
		if s.Version != "" {
			line(s.IRI, owlVer, quote(s.Version))
		}
	}
	for _, c := range s.Classes {
		iri := s.TermIRI(c.Name)
		line(iri, rdfType, "<"+owlClass+">")
		if c.Label != "" {
			line(iri, rdfsLabel, quote(c.Label))
		}
		if c.Comment != "" {
			line(iri, rdfsCmt, quote(c.Comment))
		}
		if c.Parent != "" {
			line(iri, rdfsSub, "<"+s.TermIRI(c.Parent)+">")
		}
	}
	for _, p := range s.Properties {
		iri := s.TermIRI(p.Name)
		line(iri, rdfType, "<"+expand(owlKind(p.Kind))+">")
		if p.Label != "" {
			line(iri, rdfsLabel, quote(p.Label))
		}
		if p.Comment != "" {
			line(iri, rdfsCmt, quote(p.Comment))
		}
		for _, d := range p.Domain {
			line(iri, rdfsDom, "<"+s.expandTerm(d)+">")
		}
		for _, r := range p.Range {
			line(iri, rdfsRange, "<"+s.expandTerm(r)+">")
		}
	}
	return b.String()
}

// turtleTerm writes a domain or range the way Turtle wants it: a prefixed
// name as it stands, anything else as an IRI.
func (s Schema) turtleTerm(name string) string {
	if i := strings.Index(name, ":"); i > 0 && !absolute(name) {
		return name
	}
	return "<" + s.TermIRI(name) + ">"
}

func (s Schema) expandTerm(name string) string {
	if i := strings.Index(name, ":"); i > 0 && !absolute(name) {
		if iri, ok := s.space().Resolve(name[:i]); ok {
			return iri + name[i+1:]
		}
	}
	return s.TermIRI(name)
}

func owlKind(k Kind) string {
	switch k {
	case Object:
		return "owl:ObjectProperty"
	case Data:
		return "owl:DatatypeProperty"
	}
	return "owl:AnnotationProperty"
}

func expand(prefixed string) string {
	i := strings.Index(prefixed, ":")
	if i <= 0 {
		return prefixed
	}
	if iri, ok := Standard[prefixed[:i]]; ok {
		return iri + prefixed[i+1:]
	}
	return prefixed
}

// quote writes a string as a Turtle literal, escaping what would otherwise
// end it early.
func quote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`)
	return `"` + r.Replace(s) + `"`
}
