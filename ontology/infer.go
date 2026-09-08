package ontology

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Inference derives a schema from data that has already been observed. It is
// the way the package is mostly used: entities and links come out of
// extraction, and the schema that describes them is read back off the data
// rather than written by hand.
//
// The zero value infers a class from any type seen twice, mints IRIs in
// DefaultBase, and looks for a hierarchy.
type Inference struct {
	Min  int    // observations a type needs before it becomes a class; zero means two
	Base string // namespace for minted IRIs; empty means DefaultBase
	Flat bool   // leave the class hierarchy alone
}

func (in Inference) least() int {
	if in.Min <= 0 {
		return 2
	}
	return in.Min
}

func (in Inference) space() Namespace { return Namespace{Base: in.Base} }

// Schema infers classes from the entities, properties from the links and the
// entity attributes, and returns them as one schema.
func (in Inference) Schema(g Graph) (Schema, error) {
	classes, err := in.Classes(g.Entities)
	if err != nil {
		return Schema{}, err
	}
	props, err := in.Properties(g, classes)
	if err != nil {
		return Schema{}, err
	}
	ns := in.space()
	return Schema{
		IRI:        ns.Root(),
		Name:       "InferredOntology",
		Version:    "1.0",
		Base:       ns.Root(),
		Prefixes:   ns.All(),
		Classes:    classes,
		Properties: props,
	}, nil
}

// Classes groups the entities by type and returns one class per type seen at
// least Min times. Two source types that normalize to the same class name are
// a collision, not a merge: silently folding them would lose the distinction
// the data drew.
func (in Inference) Classes(ents []Entity) ([]Class, error) {
	order, byType := group(ents)
	ns := in.space()

	collide := map[string][]string{}
	for _, t := range order {
		if len(byType[t]) < in.least() {
			continue
		}
		n := ClassName(t)
		collide[n] = append(collide[n], t)
	}
	var bad []string
	for n, from := range collide {
		if len(from) > 1 {
			sort.Strings(from)
			bad = append(bad, fmt.Sprintf("%s from %s", n, strings.Join(from, ", ")))
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return nil, fmt.Errorf("%w: entity types normalize to duplicate class names: %s", ErrCollision, strings.Join(bad, "; "))
	}

	var out []Class
	for _, t := range order {
		group := byType[t]
		if len(group) < in.least() {
			continue
		}
		name := ClassName(t)
		out = append(out, Class{
			Name:    name,
			IRI:     ns.Class(name),
			Label:   name,
			Comment: "Class representing " + strings.ToLower(name) + " entities",
			Attrs:   shared(group),
			Seen:    len(group),
			From:    t,
		})
	}
	if !in.Flat {
		out = Hierarchy(out)
	}
	return out, nil
}

// Hierarchy fills in the parent of every class that has none. Only the roots
// a vocabulary conventionally provides — Entity, Thing, Resource — are
// inferred: guessing a parent from a name overlap invents a taxonomy the data
// never showed.
func Hierarchy(classes []Class) []Class {
	present := make(map[string]bool, len(classes))
	for _, c := range classes {
		present[c.Name] = true
	}
	out := make([]Class, len(classes))
	copy(out, classes)
	for i := range out {
		if out[i].Parent != "" {
			continue
		}
		for _, root := range []string{"Entity", "Thing", "Resource"} {
			if present[root] && out[i].Name != root {
				out[i].Parent = root
				break
			}
		}
	}
	return out
}

// Cycles lists the classes that reach themselves through the hierarchy. A
// cycle makes the subclass relation meaningless, so it is reported rather
// than followed.
func Cycles(classes []Class) []string {
	parent := make(map[string]string, len(classes))
	for _, c := range classes {
		if c.Parent != "" {
			parent[c.Name] = c.Parent
		}
	}
	var out []string
	for _, c := range classes {
		seen := map[string]bool{}
		for cur := c.Name; cur != ""; cur = parent[cur] {
			if seen[cur] {
				out = append(out, c.Name)
				break
			}
			seen[cur] = true
		}
	}
	return out
}

// Properties infers object properties from the links and datatype properties
// from the entity attributes, then merges the ones that normalize to the same
// name. A name claimed by both an object and a datatype property is a
// collision: the two cannot share an IRI and mean different things.
func (in Inference) Properties(g Graph, classes []Class) ([]Property, error) {
	props := append(in.object(g, classes), in.data(g.Entities, classes)...)
	return merge(props)
}

func (in Inference) object(g Graph, classes []Class) []Property {
	alias := index(g.Entities)
	known := byName(classes)
	ns := in.space()

	var order []string
	byType := map[string][]Link{}
	for _, l := range g.Links {
		t := l.Type
		if t == "" {
			t = "relatedTo"
		}
		if _, ok := byType[t]; !ok {
			order = append(order, t)
		}
		byType[t] = append(byType[t], l)
	}

	var out []Property
	for _, t := range order {
		links := byType[t]
		if len(links) < in.least() {
			continue
		}
		var domain, rang []string
		for _, l := range links {
			if s := endpoint(l.From, l.FromType, alias); s != "" {
				domain = add(domain, class(s, known))
			}
			if o := endpoint(l.To, l.ToType, alias); o != "" {
				rang = add(rang, class(o, known))
			}
		}
		if len(domain) == 0 {
			domain = []string{"owl:Thing"}
		}
		if len(rang) == 0 {
			rang = []string{"owl:Thing"}
		}
		name := PropertyName(t)
		out = append(out, Property{
			Name:    name,
			IRI:     ns.Property(name),
			Kind:    Object,
			Label:   name,
			Comment: "Object property representing " + t + " relationships",
			Domain:  domain,
			Range:   rang,
			Seen:    len(links),
		})
	}
	return out
}

func (in Inference) data(ents []Entity, classes []Class) []Property {
	lookup := byName(classes)
	ns := in.space()

	var order []string
	byClass := map[string][]Entity{}
	for _, e := range ents {
		t := e.Type
		if t == "" {
			t = "Entity"
		}
		c, ok := lookup[t]
		if !ok {
			c, ok = lookup[ClassName(t)]
		}
		if !ok {
			continue
		}
		if _, seen := byClass[c.Name]; !seen {
			order = append(order, c.Name)
		}
		byClass[c.Name] = append(byClass[c.Name], e)
	}

	var out []Property
	for _, cname := range order {
		attrs, types := attrTypes(byClass[cname])
		for _, a := range attrs {
			name := PropertyName(a)
			out = append(out, Property{
				Name:    name,
				IRI:     ns.Property(name),
				Kind:    Data,
				Label:   name,
				Comment: "Data property for " + a,
				Domain:  []string{cname},
				Range:   []string{types[a]},
				Seen:    len(byClass[cname]),
			})
		}
	}
	return out
}

// merge folds properties that normalized to the same name into one, and
// refuses the merge when the two are not the same kind of thing.
func merge(props []Property) ([]Property, error) {
	kinds := map[string]map[Kind]bool{}
	for _, p := range props {
		if kinds[p.Name] == nil {
			kinds[p.Name] = map[Kind]bool{}
		}
		kinds[p.Name][p.Kind] = true
	}
	var bad []string
	for name, ks := range kinds {
		if len(ks) > 1 {
			bad = append(bad, name)
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return nil, fmt.Errorf("%w: %s cannot be both an object and a data property", ErrCollision, strings.Join(bad, ", "))
	}

	var out []Property
	at := map[string]int{}
	for _, p := range props {
		i, ok := at[p.Name]
		if !ok {
			at[p.Name] = len(out)
			out = append(out, p)
			continue
		}
		out[i].Domain = union(out[i].Domain, p.Domain)
		out[i].Seen += p.Seen
		if p.Kind == Object {
			out[i].Range = union(out[i].Range, p.Range)
			continue
		}
		if len(out[i].Range) == 1 && len(p.Range) == 1 && out[i].Range[0] != p.Range[0] {
			out[i].Range = []string{wider(out[i].Range[0], p.Range[0])}
		}
	}
	return out, nil
}

// group buckets entities by type, keeping the order the types first appeared
// in so that inference is deterministic.
func group(ents []Entity) ([]string, map[string][]Entity) {
	var order []string
	by := map[string][]Entity{}
	for _, e := range ents {
		t := e.Type
		if t == "" {
			t = "Entity"
		}
		if _, ok := by[t]; !ok {
			order = append(order, t)
		}
		by[t] = append(by[t], e)
	}
	return order, by
}

// shared lists the attributes carried by at least half the group, which is
// what makes them properties of the class rather than of one instance.
func shared(ents []Entity) []string {
	var order []string
	count := map[string]int{}
	for _, e := range ents {
		for k := range e.Attrs {
			if _, ok := count[k]; !ok {
				order = append(order, k)
			}
			count[k]++
		}
	}
	sort.Strings(order)
	var out []string
	for _, k := range order {
		if float64(count[k]) >= float64(len(ents))*0.5 {
			out = append(out, k)
		}
	}
	return out
}

// attrTypes returns the attributes of a group and the datatype each one takes.
// Where instances disagree, the wider of the two datatypes wins, because the
// narrower one would reject values the data actually holds.
func attrTypes(ents []Entity) ([]string, map[string]string) {
	var order []string
	types := map[string]string{}
	for _, e := range ents {
		for k, v := range e.Attrs {
			t := XSD(v)
			cur, ok := types[k]
			if !ok {
				order = append(order, k)
				types[k] = t
				continue
			}
			if cur != t {
				types[k] = wider(cur, t)
			}
		}
	}
	sort.Strings(order)
	return order, types
}

// index maps every name an entity answers to onto the types that name is
// used for. An endpoint only resolves when exactly one type claims it.
func index(ents []Entity) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, e := range ents {
		if e.Type == "" {
			continue
		}
		for _, k := range []string{e.ID, e.Name} {
			if k == "" {
				continue
			}
			if out[k] == nil {
				out[k] = map[string]bool{}
			}
			out[k][e.Type] = true
		}
	}
	return out
}

// byName indexes classes under every spelling a source might use for them:
// the class name, the source type it was inferred from, and both normalized.
func byName(classes []Class) map[string]Class {
	out := make(map[string]Class, len(classes)*2)
	for _, c := range classes {
		for _, k := range []string{c.Name, ClassName(c.Name), c.From, ClassName(c.From)} {
			if k == "" {
				continue
			}
			if _, ok := out[k]; !ok {
				out[k] = c
			}
		}
	}
	return out
}

// class is the schema's name for an observed type. A property's endpoints
// have to be spelled the way the classes are spelled, or the schema refers to
// classes it does not contain. A type with no class of its own — one seen too
// few times to earn one — is still written the way a class is written, so
// that lowering Min resolves it rather than changing it.
func class(observed string, known map[string]Class) string {
	if c, ok := known[observed]; ok {
		return c.Name
	}
	if c, ok := known[ClassName(observed)]; ok {
		return c.Name
	}
	if strings.Contains(observed, ":") {
		return observed // already a qualified term, such as owl:Thing
	}
	return ClassName(observed)
}

// endpoint resolves the class at one end of a link. A type the link states
// wins, unless it is the placeholder "Entity"; otherwise the endpoint is
// looked up among the entities, and only an unambiguous answer is used.
func endpoint(value, declared string, alias map[string]map[string]bool) string {
	if declared != "" && declared != "Entity" {
		return declared
	}
	if types := alias[value]; len(types) == 1 {
		for t := range types {
			return t
		}
	}
	return declared
}

var (
	dateForm     = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}|\d{2}/\d{2}/\d{4}|\d{2}-\d{2}-\d{4})$`)
	dateTimeForm = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}`)
)

// XSD names the datatype a value takes. It is deliberately coarse: the point
// is a range a validator can check, not an exact Go type.
func XSD(v any) string {
	switch t := v.(type) {
	case bool:
		return "xsd:boolean"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return "xsd:integer"
	case float32, float64:
		return "xsd:double"
	case time.Time:
		return "xsd:dateTime"
	case string:
		switch {
		case dateTimeForm.MatchString(t):
			return "xsd:dateTime"
		case dateForm.MatchString(t):
			return "xsd:date"
		}
		return "xsd:string"
	}
	return "xsd:string"
}

// rank orders datatypes from narrowest to widest, so that two disagreeing
// observations can be reconciled without losing a value.
var rank = map[string]int{
	"xsd:boolean":  0,
	"xsd:integer":  1,
	"xsd:double":   2,
	"xsd:decimal":  2,
	"xsd:date":     3,
	"xsd:dateTime": 4,
	"xsd:string":   5,
}

func wider(a, b string) string {
	ra, ok := rank[a]
	if !ok {
		ra = 5
	}
	rb, ok := rank[b]
	if !ok {
		rb = 5
	}
	if ra >= rb {
		return a
	}
	return b
}

func add(list []string, v string) []string {
	for _, s := range list {
		if s == v {
			return list
		}
	}
	return append(list, v)
}

func union(a, b []string) []string {
	out := append([]string(nil), a...)
	for _, v := range b {
		out = add(out, v)
	}
	return out
}
