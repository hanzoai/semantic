package agent

import (
	"context"
	"fmt"
	"sort"
)

// Ken is the range of what an agent knows: the graph of what it has been
// told, the memory of what was said lately, the decisions it took, why they
// followed from each other, and the rules it took them under.
//
// It composes the five rather than replacing them. Every part is a field, so
// a caller who needs one takes that one and leaves the rest; Ken is only here
// to save the caller who would otherwise put the same five together the same
// way every time.
type Ken struct {
	Graph   *Graph
	Memory  *Memory
	Journal *Journal
	Cause   *Cause
	Rules   *Rules

	// Scope narrows what Learn writes and what Recall reads, so one agent's
	// session or task does not answer another's questions.
	Scope Scope
}

// New builds a Ken whose parts share one graph.
func New() *Ken {
	g := &Graph{}
	return &Ken{
		Graph:   g,
		Memory:  &Memory{},
		Journal: &Journal{G: g},
		Cause:   &Cause{G: g},
		Rules:   &Rules{},
	}
}

// The kind of node a note is written to, so a note and the things it is about
// are told apart in the graph.
const kindNote = "note"

// Learn keeps a note and puts what it is about into the graph: the note
// becomes a node, and every entity it names becomes one too, joined to it.
// Two entities named in the same note are then one hop apart through it,
// which is a claim the note actually supports — unlike an edge drawn straight
// between them, which asserts a relation nobody wrote down.
func (k *Ken) Learn(ctx context.Context, n Note) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if n.Text == "" {
		return "", fmt.Errorf("agent: a note needs text")
	}
	if n.Scope == (Scope{}) {
		n.Scope = k.Scope
	}
	id := k.Memory.Add(n)
	note, _ := k.Memory.Get(id)
	k.Graph.Add(Node{ID: id, Kind: kindNote, Text: note.Text, Props: note.Props, Scope: note.Scope, Span: Span{From: note.At}})
	for _, e := range note.Of {
		if e == "" {
			continue
		}
		if _, ok := k.Graph.Node(e); !ok {
			k.Graph.Add(Node{ID: e, Kind: kindEntity, Text: e, Scope: note.Scope})
		}
		k.Graph.Join(Edge{From: id, To: e, Label: About})
	}
	return id, nil
}

// Recall finds what the agent knows about a question: the notes whose words
// answer it, and the notes about an entity the question names. A note found
// both ways ranks above one found only by its words, which is the reason for
// keeping a graph beside the memory rather than either alone.
func (k *Ken) Recall(ctx context.Context, q string, n int) []Found {
	if err := ctx.Err(); err != nil {
		return nil
	}
	found := map[string]Found{}
	var order []string
	for _, f := range k.Memory.Recall(q, 0, k.Scope) {
		found[f.Note.ID] = f
		order = append(order, f.Note.ID)
	}
	for _, hit := range k.Graph.Find(q) {
		if hit.Node.Kind != kindEntity {
			continue
		}
		for _, note := range k.Memory.About(hit.Node.ID) {
			if !k.Scope.Covers(note.Scope) {
				continue
			}
			was, seen := found[note.ID]
			score := hit.Score * byEntity
			switch {
			case !seen:
				found[note.ID] = Found{Note: note, Score: score}
				order = append(order, note.ID)
			default:
				// Found by words and by what it is about: two ways of being
				// right about the same note, so it outranks either alone.
				was.Score = min1(was.Score * 1.2)
				found[note.ID] = was
			}
		}
	}
	var out []Found
	for _, id := range order {
		out = append(out, found[id])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// A note reached through an entity the question named is weaker evidence than
// one whose own words answer it, so it enters at a fraction of the score.
const byEntity = 0.3

func min1(f float64) float64 {
	if f > 1 {
		return 1
	}
	return f
}

// Decide puts a choice to the rules and records what happened either way. A
// refusal is not an error: the agent asked, the rules answered, and the
// answer with its reason is the record. The error return is for a decision
// that could not be written at all — one with no topic, case or choice, which
// is a mistake in the calling code rather than an answer about the world.
func (k *Ken) Decide(ctx context.Context, d Decision) (Decision, error) {
	if d.Agent == "" {
		d.Agent = k.Scope.Agent
	}
	v := k.Rules.Vet(d)
	d.Allowed, d.Gloss = v.OK, v.Why
	if d.Rule == "" {
		d.Rule = v.Policy
	}
	return k.Journal.Write(ctx, d)
}

// Story is why a decision was taken: the decision itself, what it rested on,
// what led to it, and where the explanation stops.
type Story struct {
	Decision Decision
	Because  []Node // the evidence, as it stands in the graph now
	Led      []Away // what led to it, farthest first
	Roots    []Away // where the chain ends
	Gloss    string
}

// Why explains a decision. It reads the graph rather than a stored
// explanation, so what it says is what the graph currently supports: evidence
// that was later retracted shows as retracted rather than quietly staying in
// the story.
func (k *Ken) Why(ctx context.Context, id string) (Story, error) {
	if err := ctx.Err(); err != nil {
		return Story{}, err
	}
	d, ok := k.Journal.Read(id)
	if !ok {
		return Story{}, fmt.Errorf("%w: %s", ErrNoDecision, id)
	}
	s := Story{Decision: d, Led: k.Cause.Up(id, 0), Roots: k.Cause.Roots(id, 0)}
	for _, e := range d.Because {
		if n, ok := k.Graph.Node(e); ok {
			s.Because = append(s.Because, n)
		}
	}
	verdict := "allowed"
	if !d.Allowed {
		verdict = "refused"
	}
	s.Gloss = fmt.Sprintf("%s %q on %s: %s, resting on %s, after %s",
		verdict, d.Choice, d.Topic, d.Gloss,
		count(len(s.Because), "piece of evidence", "pieces of evidence"),
		count(len(s.Led), "earlier decision", "earlier decisions"))
	return s, nil
}
