package ontology

import (
	"strings"
	"unicode"
)

// Names are conventional in RDF vocabularies: a class is a singular noun in
// PascalCase, a property is a verb phrase in camelCase, and an ontology is
// title case ending in "Ontology". The conventions carry meaning — reading
// "worksFor" tells you it is a relation without looking it up — so the
// package normalizes to them and reports where a name departs.

// ClassName is name written the way a class is written: PascalCase, singular.
func ClassName(name string) string { return Singular(Pascal(name)) }

// PropertyName is name written the way a property is written: camelCase.
func PropertyName(name string) string { return Camel(name) }

// SchemaName is name written the way an ontology is written: title case with
// an "Ontology" suffix.
func SchemaName(name string) string {
	name = strings.TrimSuffix(name, "Ontology")
	t := strings.ReplaceAll(Title(name), " ", "")
	if t == "" {
		return "Ontology"
	}
	if strings.HasSuffix(t, "Ontology") {
		return t
	}
	return t + "Ontology"
}

// CheckClass reports whether a class name follows the conventions, and what
// it would be if it did not.
func CheckClass(name string) (suggestion string, ok bool) {
	if pascalCase(name) && singular(name) && nounPhrase(name) {
		return "", true
	}
	return ClassName(name), false
}

// CheckProperty reports whether a property name follows the conventions for
// its kind, and what it would be if it did not. An object property reads as a
// verb phrase; a data property only has to be lower or camel case.
func CheckProperty(name string, kind Kind) (suggestion string, ok bool) {
	if kind == Object {
		if camelCase(name) && verbPhrase(name) {
			return "", true
		}
	} else if camelCase(name) || name == strings.ToLower(name) {
		return "", true
	}
	return PropertyName(name), false
}

// CheckSchema reports whether an ontology name follows the conventions, and
// what it would be if it did not.
func CheckSchema(name string) (suggestion string, ok bool) {
	if titleCase(name) && strings.HasSuffix(name, "Ontology") {
		return "", true
	}
	return SchemaName(name), false
}

// Pascal joins the words of name with each capitalised: "my class" is
// "MyClass", and so is "MY_CLASS".
func Pascal(name string) string {
	w := words(name)
	if len(w) == 0 {
		return "Entity"
	}
	var b strings.Builder
	for _, s := range w {
		b.WriteString(capitalize(s))
	}
	return b.String()
}

// Camel is Pascal with the first word left lower: "has name" is "hasName". A
// name already in camelCase is left alone, so normalizing is idempotent and
// "worksFor" does not become "worksfor".
func Camel(name string) string {
	if camelAlready(name) {
		return name
	}
	w := words(name)
	if len(w) == 0 {
		return "hasProperty"
	}
	var b strings.Builder
	b.WriteString(strings.ToLower(w[0]))
	for _, s := range w[1:] {
		b.WriteString(capitalize(s))
	}
	return b.String()
}

// Title capitalises each word and separates them with spaces.
func Title(name string) string {
	w := words(name)
	if len(w) == 0 {
		return "Ontology"
	}
	for i, s := range w {
		w[i] = capitalize(s)
	}
	return strings.Join(w, " ")
}

// Singular strips a regular English plural ending. It knows nothing of
// irregular plurals, and leaves words ending in a double s alone.
func Singular(name string) string {
	l := strings.ToLower(name)
	switch {
	case strings.HasSuffix(l, "ies") && len(name) > 3:
		return name[:len(name)-3] + "y"
	case strings.HasSuffix(l, "es") && !strings.HasSuffix(l, "ss") && len(name) > 2:
		return name[:len(name)-2]
	case strings.HasSuffix(l, "s") && !strings.HasSuffix(l, "ss") && len(name) > 1 && !keepsItsS[l]:
		return name[:len(name)-1]
	}
	return name
}

// keepsItsS are singular words that end in s, where stripping it would invent
// a word that does not exist.
var keepsItsS = map[string]bool{"class": true, "process": true, "analysis": true, "basis": true, "status": true}

func words(name string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range name {
		if isWordRune(r) {
			cur.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return out
}

func isWordRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	r := []rune(strings.ToLower(s))
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// camelAlready recognises a name that is one word, starts lower and carries an
// inner capital — the shape splitting would destroy.
func camelAlready(name string) bool {
	if name == "" || !unicode.IsLower(rune(name[0])) {
		return false
	}
	if strings.ContainsAny(name, " _-") {
		return false
	}
	return strings.IndexFunc(name, unicode.IsUpper) > 0
}

func pascalCase(name string) bool {
	if name == "" {
		return false
	}
	if !unicode.IsUpper([]rune(name)[0]) {
		return false
	}
	return alnum(strings.NewReplacer("_", "", "-", "").Replace(name))
}

func camelCase(name string) bool {
	if name == "" {
		return false
	}
	if !unicode.IsLower([]rune(name)[0]) {
		return false
	}
	return alnum(strings.NewReplacer("_", "", "-", "").Replace(name))
}

func titleCase(name string) bool {
	for _, w := range strings.Fields(name) {
		if !unicode.IsUpper([]rune(w)[0]) {
			return false
		}
	}
	return name != ""
}

func singular(name string) bool {
	l := strings.ToLower(name)
	if keepsItsS[l] {
		return true
	}
	return !strings.HasSuffix(l, "s")
}

func nounPhrase(name string) bool { return pascalCase(name) && alnum(name) }

// verbPhrase is a heuristic, not a parser: a property that starts with one of
// the relational verbs reads as a relation.
func verbPhrase(name string) bool {
	l := strings.ToLower(name)
	for _, v := range []string{"has", "is", "can", "does", "performs", "contains", "relates", "works", "belongs", "owns", "uses"} {
		if strings.HasPrefix(l, v) {
			return true
		}
	}
	return false
}

func alnum(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !isWordRune(r) {
			return false
		}
	}
	return true
}
