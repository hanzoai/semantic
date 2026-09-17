package ontology

import (
	"encoding/json"
	"fmt"
	"maps"
	"strings"
)

// JSONLD writes the schema as a JSON-LD document: a context binding the
// prefixes and a graph of one node per term.
func (s Schema) JSONLD() ([]byte, error) {
	ns := s.space()
	ctx := map[string]any{
		"owl":    Standard["owl"],
		"rdf":    Standard["rdf"],
		"rdfs":   Standard["rdfs"],
		"xsd":    Standard["xsd"],
		"@vocab": ns.Root(),
	}
	for p, iri := range s.Prefixes {
		ctx[p] = iri
	}

	var graph []map[string]any
	if s.IRI != "" {
		ont := map[string]any{"@id": s.IRI, "@type": "owl:Ontology"}
		put(ont, "rdfs:label", s.Name)
		put(ont, "rdfs:comment", s.Comment)
		put(ont, "owl:versionInfo", s.Version)
		graph = append(graph, ont)
	}
	for _, c := range s.Classes {
		n := map[string]any{"@id": s.TermIRI(c.Name), "@type": "owl:Class"}
		put(n, "rdfs:label", c.Label)
		put(n, "rdfs:comment", c.Comment)
		if c.Parent != "" {
			n["rdfs:subClassOf"] = ref(s.TermIRI(c.Parent))
		}
		graph = append(graph, n)
	}
	for _, p := range s.Properties {
		n := map[string]any{"@id": s.TermIRI(p.Name), "@type": owlKind(p.Kind)}
		put(n, "rdfs:label", p.Label)
		put(n, "rdfs:comment", p.Comment)
		if refs := refList(s, p.Domain); len(refs) > 0 {
			n["rdfs:domain"] = refs
		}
		if refs := refList(s, p.Range); len(refs) > 0 {
			n["rdfs:range"] = refs
		}
		graph = append(graph, n)
	}
	return json.MarshalIndent(map[string]any{"@context": ctx, "@graph": graph}, "", "  ")
}

func put(m map[string]any, key, value string) {
	if value != "" {
		m[key] = value
	}
}

func ref(iri string) map[string]any { return map[string]any{"@id": iri} }

func refList(s Schema, names []string) []any {
	var out []any
	for _, n := range names {
		out = append(out, ref(s.expandTerm(n)))
	}
	return out
}

// ParseJSONLD reads a schema from a JSON-LD document. The document may hold a
// graph or a single node, and terms may be written with any prefix the
// context binds.
func ParseJSONLD(src []byte) (Schema, error) {
	var doc map[string]any
	if err := json.Unmarshal(src, &doc); err != nil {
		return Schema{}, fmt.Errorf("json-ld: %w", err)
	}
	ctx := map[string]string{}
	maps.Copy(ctx, Standard)
	vocab := ""
	if c, ok := doc["@context"].(map[string]any); ok {
		for k, v := range c {
			iri, ok := v.(string)
			if !ok {
				continue
			}
			if k == "@vocab" {
				vocab = iri
				continue
			}
			ctx[k] = iri
		}
	}

	nodes := []any{}
	switch g := doc["@graph"].(type) {
	case []any:
		nodes = g
	case map[string]any:
		nodes = []any{g}
	default:
		if _, ok := doc["@id"]; ok {
			nodes = []any{any(doc)}
		}
	}

	s := Schema{Base: vocab, Prefixes: bindings(ctx)}

	for _, raw := range nodes {
		n, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id, _ := n["@id"].(string)
		types := map[string]bool{}
		for _, t := range values(n["@type"]) {
			types[expandWith(ctx, vocab, t)] = true
		}
		label := first(text(n, ctx, vocab, rdfsLabel))
		comment := first(text(n, ctx, vocab, rdfsCmt))

		switch {
		case types[owlOnt]:
			s.IRI = id
			s.Name = label
			if s.Name == "" {
				s.Name = local(id)
			}
			s.Comment = comment
			s.Version = first(text(n, ctx, vocab, owlVer))
		case types[owlClass] || types[rdfsClass]:
			c := Class{Name: local(id), IRI: id, Label: label, Comment: comment}
			for _, p := range text(n, ctx, vocab, rdfsSub) {
				c.Parent = local(p)
				break
			}
			s.Classes = append(s.Classes, c)
		case types[owlObject], types[owlData], types[owlAnnot], types[rdfProp]:
			kind := Annotation
			switch {
			case types[owlObject]:
				kind = Object
			case types[owlData]:
				kind = Data
			}
			p := Property{Name: local(id), IRI: id, Kind: kind, Label: label, Comment: comment}
			for _, d := range text(n, ctx, vocab, rdfsDom) {
				p.Domain = add(p.Domain, shorten(d))
			}
			for _, r := range text(n, ctx, vocab, rdfsRange) {
				p.Range = add(p.Range, shorten(r))
			}
			s.Properties = append(s.Properties, p)
		}
	}
	if s.Version == "" {
		s.Version = "1.0"
	}
	if s.Base == "" {
		var cs, ps []string
		for _, c := range s.Classes {
			cs = append(cs, c.IRI)
		}
		for _, p := range s.Properties {
			ps = append(ps, p.IRI)
		}
		s.Base = baseOf(cs, ps, s.IRI)
	}
	return s, nil
}

// text reads the values of one property from a JSON-LD node, whichever of the
// shapes JSON-LD allows they were written in.
func text(n map[string]any, ctx map[string]string, vocab, want string) []string {
	var out []string
	for k, v := range n {
		if strings.HasPrefix(k, "@") || expandWith(ctx, vocab, k) != want {
			continue
		}
		for _, item := range values(v) {
			out = append(out, item)
		}
	}
	return out
}

// values flattens the scalar, object and list forms a JSON-LD value takes.
func values(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case float64:
		return []string{fmt.Sprintf("%v", t)}
	case bool:
		return []string{fmt.Sprintf("%v", t)}
	case map[string]any:
		for _, key := range []string{"@id", "@value"} {
			if s, ok := t[key].(string); ok {
				return []string{s}
			}
			if f, ok := t[key].(float64); ok {
				return []string{fmt.Sprintf("%v", f)}
			}
		}
	case []any:
		var out []string
		for _, item := range t {
			out = append(out, values(item)...)
		}
		return out
	}
	return nil
}

func first(list []string) string {
	if len(list) == 0 {
		return ""
	}
	return list[0]
}

// expandWith resolves a term against the context, falling back to the vocab
// for a bare name.
func expandWith(ctx map[string]string, vocab, term string) string {
	if absolute(term) {
		return term
	}
	if i := strings.Index(term, ":"); i > 0 {
		if iri, ok := ctx[term[:i]]; ok {
			return iri + term[i+1:]
		}
		return term
	}
	return vocab + term
}
