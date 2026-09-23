package extract

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/hanzoai/semantic"
)

// LLM extracts by asking a Model. The call itself is the caller's business;
// what belongs here is everything around it — the prompt that states the shape
// of the answer, the recovery of JSON from whatever the model wrapped it in,
// the aliases providers use for the same field, and the binding of a named
// endpoint back to an entity already found.
//
// Set Schema to refuse an extraction that leaves the ontology. A caller who
// would rather keep the conforming part calls Schema.Keep on the result
// instead.
type LLM struct {
	Model Model

	// Kinds are the entity labels to prefer. Empty asks for the general set.
	Kinds []string
	// Preds are the predicates to prefer, for both relations and triples.
	// Empty asks for whatever fits the text.
	Preds []string
	// Cap is the most entities a relation prompt will name. Zero uses 80,
	// past which the prompt spends more on the list than on the text.
	Cap int
	// When also asks when each relation held.
	When bool
	// Schema, when set, rejects an extraction that does not conform to it.
	Schema *Schema
	// Match is how alike a stated endpoint and a known entity must be for the
	// two to be the same thing. Zero uses 0.8.
	Match float64
}

// Link is a relation as the model stated it: endpoints named by text, not yet
// bound to any entity. Score is the confidence the model stated, zero when it
// stated none.
type Link struct {
	Subject   string
	Predicate string
	Object    string
	Score     float64

	// From and Until are when the relation began and ceased, as an ISO date
	// or as the phrase the text used. Empty when the text said nothing.
	From, Until string
	// When is the confidence that a temporal signal was really there, and
	// Cite the exact words it was read from.
	When float64
	Cite string
}

// Entities asks the model which entities the text names.
func (l LLM) Entities(ctx context.Context, text string) ([]Entity, error) {
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("entities: %w: empty text", ErrInvalid)
	}
	doc, err := l.ask(ctx, fmt.Sprintf(entPrompt, l.kinds(), text), Ents)
	if err != nil {
		return nil, fmt.Errorf("entities: %w", err)
	}
	out, err := items[Entity](doc, string(Ents))
	if err != nil {
		return nil, fmt.Errorf("entities: %w", err)
	}
	for i := range out {
		out[i].Meta = mark(out[i].Meta)
	}
	if err := l.gate(Set{Entities: out}); err != nil {
		return nil, fmt.Errorf("entities: %w", err)
	}
	return out, nil
}

// Relations asks the model how the entities it was given are connected. The
// entities are named in the prompt so the model reuses their surface forms,
// and each endpoint it returns is bound back to one of them; an endpoint that
// matches none is kept as an UNKNOWN entity rather than dropped, because a
// co-founder the entity pass missed is still a co-founder.
func (l LLM) Relations(ctx context.Context, text string, known []Entity) ([]Relation, error) {
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("relations: %w: empty text", ErrInvalid)
	}
	if len(known) == 0 {
		return nil, fmt.Errorf("relations: %w: no entities to relate", ErrInvalid)
	}

	prompt := relPrompt
	if l.When {
		prompt = whenPrompt
	}
	doc, err := l.ask(ctx, fmt.Sprintf(prompt, l.preds(), text, l.list(text, known)), Rels)
	if err != nil {
		return nil, fmt.Errorf("relations: %w", err)
	}
	links, err := items[Link](doc, string(Rels))
	if err != nil {
		return nil, fmt.Errorf("relations: %w", err)
	}
	out := l.bind(links, known, text)
	if err := l.gate(Set{Relations: out}); err != nil {
		return nil, fmt.Errorf("relations: %w", err)
	}
	return out, nil
}

// Triples asks the model for subject-predicate-object statements about a chunk
// directly, without first naming entities. It is the cheap path: one call
// instead of two, at the cost of untyped endpoints.
func (l LLM) Triples(ctx context.Context, c semantic.Chunk) ([]semantic.Triple, error) {
	if strings.TrimSpace(c.Text) == "" {
		return nil, fmt.Errorf("triples: %w: empty text", ErrInvalid)
	}
	doc, err := l.ask(ctx, fmt.Sprintf(triPrompt, l.triples(), c.Text), Tris)
	if err != nil {
		return nil, fmt.Errorf("triples: %w", err)
	}
	links, err := items[Link](doc, string(Tris))
	if err != nil {
		return nil, fmt.Errorf("triples: %w", err)
	}
	out := make([]semantic.Triple, 0, len(links))
	for _, k := range links {
		if k.Subject == "" || k.Object == "" {
			continue
		}
		out = append(out, semantic.Triple{
			Subject:   k.Subject,
			Predicate: pred(k.Predicate),
			Object:    k.Object,
			From:      c,
			Score:     k.Score,
		})
	}
	return out, nil
}

// Find runs both passes: the entities, then the relations between them.
func (l LLM) Find(ctx context.Context, text string) (Set, error) {
	es, err := l.Entities(ctx, text)
	if err != nil {
		return Set{}, err
	}
	if len(es) == 0 {
		return Set{}, nil
	}
	rs, err := l.Relations(ctx, text, es)
	if err != nil {
		return Set{Entities: es}, err
	}
	return Set{Entities: es, Relations: rs}, nil
}

// Extract makes LLM a pipeline stage.
func (l LLM) Extract(ctx context.Context, c semantic.Chunk) ([]semantic.Triple, error) {
	x, err := l.Find(ctx, c.Text)
	if err != nil {
		return nil, err
	}
	return x.Triples(c), nil
}

// Loose returns the endpoints of a triple that match none of the entities
// already known. A graph builder promotes those into nodes of their own
// instead of leaving the edge dangling.
func Loose(t semantic.Triple, known []Entity, min float64) []string {
	if min == 0 {
		min = 0.8
	}
	var out []string
	for _, end := range []string{t.Subject, t.Object} {
		if _, ok := Bind(end, known, min); !ok {
			out = append(out, end)
		}
	}
	return out
}

// bind turns stated links into relations against the entities already known.
func (l LLM) bind(links []Link, known []Entity, text string) []Relation {
	min := l.Match
	if min == 0 {
		min = 0.8
	}
	out := make([]Relation, 0, len(links))
	for _, k := range links {
		if k.Subject == "" || k.Object == "" {
			continue
		}
		s, ok := Bind(k.Subject, known, min)
		if !ok {
			s = stranger(k.Subject)
		}
		o, ok := Bind(k.Object, known, min)
		if !ok {
			o = stranger(k.Object)
		}
		meta := mark(nil)
		if l.When {
			meta["from"] = k.From
			meta["until"] = k.Until
			meta["when"] = k.When
			meta["cite"] = k.Cite
		}
		out = append(out, Relation{
			Subject:   s,
			Predicate: pred(k.Predicate),
			Object:    o,
			Score:     k.Score,
			Context:   text,
			Meta:      meta,
		})
	}
	return out
}

// gate refuses an extraction that leaves the schema, when one was given.
func (l LLM) gate(x Set) error {
	if l.Schema == nil {
		return nil
	}
	return l.Schema.Check(x).Err()
}

// ask sends one prompt and returns the JSON document the model answered with.
func (l LLM) ask(ctx context.Context, prompt string, k Kind) ([]byte, error) {
	if l.Model == nil {
		return nil, fmt.Errorf("no model")
	}
	reply, err := l.Model.Complete(ctx, prompt, k)
	if err != nil {
		return nil, err
	}
	return JSON(reply)
}

func (l LLM) kinds() string {
	if len(l.Kinds) == 0 {
		return anyKind
	}
	return fmt.Sprintf(someKind, strings.Join(l.Kinds, ", "))
}

func (l LLM) preds() string {
	if len(l.Preds) == 0 {
		return anyPred
	}
	return fmt.Sprintf(somePred, strings.Join(l.Preds, ", "))
}

func (l LLM) triples() string {
	if len(l.Preds) == 0 {
		return anyTri
	}
	return fmt.Sprintf(someTri, strings.Join(l.Preds, ", "))
}

// list is the entity roster a relation prompt carries.
func (l LLM) list(text string, known []Entity) string {
	cap := l.Cap
	if cap == 0 {
		cap = 80
	}
	parts := make([]string, 0, len(known))
	for _, e := range Narrow(text, known, cap) {
		parts = append(parts, e.Text+" ("+e.Label+")")
	}
	return strings.Join(parts, ", ")
}

// stranger is the placeholder for an endpoint the model named that no entity
// pass found. No pass scored it, so it has no Score.
func stranger(text string) Entity {
	return Entity{
		Text: text, Label: "UNKNOWN", End: len(text),
		Meta: map[string]any{"by": "llm", "loose": true},
	}
}

func mark(m map[string]any) map[string]any {
	if m == nil {
		m = map[string]any{}
	}
	m["by"] = "llm"
	return m
}

func pred(p string) string {
	if p == "" {
		return "related_to"
	}
	return p
}

// Narrow trims an entity roster to those the text plausibly mentions, and
// then to the longest max of them. A prompt that spends its budget listing
// entities the chunk never names buys nothing. A roster already within max is
// returned untouched, and one whose entities the text never mentions is kept
// whole rather than emptied — a filter that removes everything has told the
// caller nothing.
func Narrow(text string, known []Entity, max int) []Entity {
	if text == "" || len(known) == 0 || max < 1 {
		return nil
	}
	if len(known) <= max {
		return known
	}
	low := strings.ToLower(text)

	seen := map[string]bool{}
	var hit []Entity
	for _, e := range known {
		key := strings.ToLower(strings.TrimSpace(e.Text))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		if strings.Contains(low, key) {
			hit = append(hit, e)
			continue
		}
		for _, tok := range word.FindAllString(key, -1) {
			if len(tok) < 2 || filler[tok] {
				continue
			}
			if strings.Contains(low, tok) {
				hit = append(hit, e)
				break
			}
		}
	}
	if len(hit) == 0 {
		hit = append(hit, known...)
	}
	if len(hit) > max {
		sort.SliceStable(hit, func(i, j int) bool { return len(hit[i].Text) > len(hit[j].Text) })
		hit = hit[:max]
	}
	return hit
}

var word = regexp.MustCompile(`[a-z0-9]+`)

// filler are the tokens too common to prove an entity is mentioned.
var filler = map[string]bool{
	"inc": true, "incorporated": true, "corp": true, "corporation": true,
	"co": true, "company": true, "ltd": true, "llc": true, "plc": true,
	"group": true, "holdings": true, "limited": true, "the": true,
	"and": true, "or": true, "of": true, "in": true, "on": true,
	"at": true, "for": true, "to": true, "a": true, "an": true,
}

// JSON recovers the JSON document from a model reply. It strips a markdown
// fence, and when what is left still does not parse it takes the outermost
// braces or brackets, drops a trailing comma before a closing one, and closes
// what the model left open when it ran into a token limit. Those three faults
// are what models actually produce; anything else is ErrParse.
func JSON(reply string) ([]byte, error) {
	text := strings.TrimSpace(reply)
	if text == "" {
		return nil, fmt.Errorf("%w: empty reply", ErrParse)
	}

	if i := strings.Index(text, "```json"); i >= 0 {
		rest := text[i+len("```json"):]
		if before, _, ok := strings.Cut(rest, "```"); ok {
			text = strings.TrimSpace(before)
		} else {
			text = strings.TrimSpace(rest)
		}
	} else if strings.Contains(text, "```") {
		for block := range strings.SplitSeq(text, "```") {
			block = strings.TrimSpace(block)
			if (strings.HasPrefix(block, "{") && strings.HasSuffix(block, "}")) ||
				(strings.HasPrefix(block, "[") && strings.HasSuffix(block, "]")) {
				text = block
				break
			}
		}
	}

	if json.Valid([]byte(text)) {
		return []byte(text), nil
	}

	start := -1
	if o, a := strings.Index(text, "{"), strings.Index(text, "["); o >= 0 && (a < 0 || o < a) {
		start = o
	} else if a >= 0 {
		start = a
	}
	end := strings.LastIndex(text, "}")
	if b := strings.LastIndex(text, "]"); b > end {
		end = b
	}
	if start < 0 || end <= start {
		return nil, fmt.Errorf("%w: %.100s", ErrParse, reply)
	}

	// The outermost brace is where the document ends when the reply is whole.
	// When it was cut off, the last brace is wherever the model stopped —
	// possibly inside a string — so the repair is tried on everything from
	// the opening brace on, and only then on the narrower span.
	candidate := text[start : end+1]
	for _, try := range []string{
		candidate,
		comma.ReplaceAllString(candidate, "$1"),
		shut(text[start:]),
		shut(candidate),
	} {
		if json.Valid([]byte(try)) {
			return []byte(try), nil
		}
	}
	return nil, fmt.Errorf("%w: %.100s", ErrParse, reply)
}

var comma = regexp.MustCompile(`,\s*([\]}])`)

// shut closes what a reply truncated by a token limit left open, innermost
// first — a count of each kind is not enough, since {"a":[ must be closed ]}
// and not }]. Braces inside strings are not structure and are not counted. A
// reply cut off in the middle of a string is not recoverable and is left to
// fail: closing the quote would invent a value the model never sent.
func shut(s string) string {
	s = strings.TrimSuffix(strings.TrimSpace(s), "...")
	s = comma.ReplaceAllString(strings.TrimSpace(s), "$1")
	s = strings.TrimSuffix(strings.TrimSpace(s), ",")

	var open []byte
	quoted, esc := false, false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case esc:
			esc = false
		case quoted && c == '\\':
			esc = true
		case c == '"':
			quoted = !quoted
		case quoted:
		case c == '{' || c == '[':
			open = append(open, c)
		case (c == '}' || c == ']') && len(open) > 0:
			open = open[:len(open)-1]
		}
	}

	var b strings.Builder
	b.WriteString(s)
	for _, o := range slices.Backward(open) {
		if o == '{' {
			b.WriteByte('}')
		} else {
			b.WriteByte(']')
		}
	}
	return b.String()
}

// items pulls a list out of a model reply. Providers wrap it under the key the
// prompt named, or under "data" or "results"; some send a bare list, and some
// send one lone object. All four arrive here. A reply with none of them is not
// an error — the model said there was nothing.
func items[T any](doc []byte, key string) ([]T, error) {
	doc = bytes.TrimSpace(doc)
	if len(doc) == 0 {
		return nil, fmt.Errorf("%w: empty document", ErrParse)
	}
	if doc[0] == '[' {
		var out []T
		if err := json.Unmarshal(doc, &out); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrParse, err)
		}
		return out, nil
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(doc, &obj); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrParse, err)
	}
	for _, k := range []string{key, "data", "results"} {
		raw, ok := obj[k]
		if !ok {
			continue
		}
		var out []T
		if err := json.Unmarshal(raw, &out); err == nil {
			return out, nil
		}
	}
	if _, ok := obj["subject"]; ok {
		return one[T](doc)
	}
	if _, ok := obj["text"]; ok {
		return one[T](doc)
	}
	return nil, nil
}

func one[T any](doc []byte) ([]T, error) {
	var v T
	if err := json.Unmarshal(doc, &v); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrParse, err)
	}
	return []T{v}, nil
}
