package agent

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/hanzoai/semantic/decision"
)

// Decision is one choice an agent made. The embedded record says who chose
// what, on which evidence, under which rule and when; the rest says what the
// choice was about, which is what makes two decisions comparable and a
// precedent findable.
//
// Allowed and Gloss carry the verdict of the rules. A refused choice is still
// a decision and is still written down — that record is the only place the
// reason for a refusal survives.
type Decision struct {
	decision.Record

	Topic      string  // the class of question: loan, triage, release
	Case       string  // the situation decided
	Reason     string  // why the agent chose as it did
	Confidence float64 // how sure it was, from 0 to 1
	Allowed    bool    // whether the rules permitted the choice
	Gloss      string  // what the rules said, permitted or not
	Props      map[string]any
	Span       Span // when this decision is in force
}

// The property keys a decision is written under. They are fixed so a
// decision written by one caller reads back the same for every other.
const (
	keyTopic  = "topic"
	keyCase   = "case"
	keyReason = "reason"
	keyChoice = "choice"
	keySure   = "confidence"
	keyAgent  = "agent"
	keyRule   = "rule"
	keyOK     = "allowed"
	keyGloss  = "gloss"
	keyAt     = "at"
)

// Kinds of node the journal writes.
const (
	kindDecision = "decision"
	kindEntity   = "entity"
)

// About is the edge from a decision to a thing the decision was about. It is
// how evidence is attached: the decision's Because is the ends of these
// edges, so the evidence is in the graph rather than in a list beside it.
const About = "about"

// Journal writes decisions into a graph and reads them back. A decision is a
// node and its evidence is edges, so nothing is stored twice and a decision
// is reachable by the same walk as anything else in the graph.
//
// Its zero value writes to a graph of its own. It satisfies
// decision.Recorder.
type Journal struct{ G *Graph }

var _ decision.Recorder = (*Journal)(nil)

func (j *Journal) graph() *Graph {
	if j.G == nil {
		j.G = &Graph{}
	}
	return j.G
}

// ErrNoDecision is returned when a decision id names nothing.
var ErrNoDecision = errors.New("agent: no such decision")

// Write records a decision. It fills in an id and a time if the caller left
// them empty, writes the evidence as edges, and returns the decision as
// stored.
func (j *Journal) Write(ctx context.Context, d Decision) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	switch {
	case d.Topic == "":
		return Decision{}, fmt.Errorf("agent: decision needs a topic")
	case d.Case == "":
		return Decision{}, fmt.Errorf("agent: decision needs a case")
	case d.Choice == "":
		return Decision{}, fmt.Errorf("agent: decision needs a choice")
	case d.Confidence < 0 || d.Confidence > 1:
		return Decision{}, fmt.Errorf("agent: confidence %v is outside 0 to 1", d.Confidence)
	}
	if d.ID == "" {
		d.ID = mint("decision")
	}
	if d.At.IsZero() {
		d.At = time.Now().UTC()
	}
	props := map[string]any{}
	for k, v := range d.Props {
		props[k] = v
	}
	props[keyTopic] = d.Topic
	props[keyCase] = d.Case
	props[keyReason] = d.Reason
	props[keyChoice] = d.Choice
	props[keySure] = d.Confidence
	props[keyAgent] = d.Agent
	props[keyRule] = d.Rule
	props[keyOK] = d.Allowed
	props[keyGloss] = d.Gloss
	props[keyAt] = d.At

	g := j.graph()
	g.Add(Node{ID: d.ID, Kind: kindDecision, Text: d.Case, Props: props, Span: d.Span})
	for _, e := range d.Because {
		if e == "" {
			continue
		}
		if _, ok := g.Node(e); !ok {
			g.Add(Node{ID: e, Kind: kindEntity, Text: e})
		}
		g.Join(Edge{Link: Link{From: d.ID, To: e, Label: About}})
	}
	return j.read(d.ID)
}

// Record writes the root form of a decision: everything decision.Recorder
// carries, and nothing this package adds. It is the interface's way in and
// calls Write. The root record names no class of question, so one written
// this way is filed under the topic "decision" and reads as allowed, since
// nothing was asked of the rules before it was written.
func (j *Journal) Record(ctx context.Context, r decision.Record) error {
	d := Decision{Record: r, Topic: "decision", Case: r.Choice, Allowed: true, Confidence: 1}
	_, err := j.Write(ctx, d)
	return err
}

// By lists what one agent decided, oldest first.
func (j *Journal) By(ctx context.Context, agent string) ([]decision.Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var out []decision.Record
	for _, d := range j.All() {
		if d.Agent == agent {
			out = append(out, d.Record)
		}
	}
	return out, nil
}

// Read reads one decision back.
func (j *Journal) Read(id string) (Decision, bool) {
	d, err := j.read(id)
	return d, err == nil
}

func (j *Journal) read(id string) (Decision, error) {
	n, ok := j.graph().Node(id)
	if !ok || n.Kind != kindDecision {
		return Decision{}, fmt.Errorf("%w: %s", ErrNoDecision, id)
	}
	return j.decide(n), nil
}

// decide reads a decision out of the node it was written to.
func (j *Journal) decide(n Node) Decision {
	d := Decision{
		Record: decision.Record{
			ID:     n.ID,
			Agent:  str(n.Props[keyAgent]),
			Choice: str(n.Props[keyChoice]),
			Rule:   str(n.Props[keyRule]),
			At:     stamp(n.Props[keyAt]),
		},
		Topic:      str(n.Props[keyTopic]),
		Case:       str(n.Props[keyCase]),
		Reason:     str(n.Props[keyReason]),
		Confidence: num(n.Props[keySure]),
		Allowed:    n.Props[keyOK] == true,
		Gloss:      str(n.Props[keyGloss]),
		Span:       n.Span,
	}
	if d.Case == "" {
		d.Case = n.Text
	}
	for _, e := range j.graph().Out(n.ID) {
		if e.Label == About {
			d.Because = append(d.Because, e.To)
		}
	}
	for k, v := range n.Props {
		switch k {
		case keyTopic, keyCase, keyReason, keyChoice, keySure, keyAgent, keyRule, keyOK, keyGloss, keyAt:
			continue
		}
		if d.Props == nil {
			d.Props = map[string]any{}
		}
		d.Props[k] = v
	}
	return d
}

// All lists every decision, in the order they were written.
func (j *Journal) All() []Decision {
	nodes := j.graph().Nodes(kindDecision)
	out := make([]Decision, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, j.decide(n))
	}
	return out
}

// On lists the decisions of one topic, in the order they were written.
func (j *Journal) On(topic string) []Decision {
	var out []Decision
	for _, d := range j.All() {
		if d.Topic == topic {
			out = append(out, d)
		}
	}
	return out
}

// About lists the decisions that rested on one entity, in the order they were
// written.
func (j *Journal) About(entity string) []Decision {
	g := j.graph()
	var out []Decision
	for _, e := range g.In(entity) {
		if e.Label != About {
			continue
		}
		if n, ok := g.Node(e.From); ok && n.Kind == kindDecision {
			out = append(out, j.decide(n))
		}
	}
	return out
}

// Lead records that one decision led to another: it caused it, influenced it,
// or is the precedent it followed. Both ends must be decisions — causality
// between a decision and a document is not causality, it is evidence, and
// evidence is what About is for.
func (j *Journal) Lead(from, to, how string) error {
	switch how {
	case Caused, Influenced, Precedes:
	default:
		return fmt.Errorf("agent: %q is not one of %q, %q, %q", how, Caused, Influenced, Precedes)
	}
	g := j.graph()
	for _, id := range [2]string{from, to} {
		n, ok := g.Node(id)
		if !ok {
			return fmt.Errorf("%w: %s", ErrNoDecision, id)
		}
		if n.Kind != kindDecision {
			return fmt.Errorf("agent: %s is a %s, not a decision", id, n.Kind)
		}
	}
	g.Join(Edge{Link: Link{From: from, To: to, Label: how}})
	return nil
}

// Before lists the decisions recorded as precedents for this one, in the
// order the links were made.
func (j *Journal) Before(id string) []Decision {
	var out []Decision
	for _, e := range j.graph().In(id) {
		if e.Label != Precedes {
			continue
		}
		if n, ok := j.graph().Node(e.From); ok && n.Kind == kindDecision {
			out = append(out, j.decide(n))
		}
	}
	return out
}

// Ask is a search for the decisions most like the one at hand.
type Ask struct {
	Case  string    // the situation to match
	Topic string    // only this topic; empty asks every topic
	Of    []string  // only decisions that rested on these entities
	Floor float64   // ignore anything scoring under this
	At    time.Time // as the journal stood then; zero means now
	Old   bool      // include decisions whose span has closed
	N     int       // at most this many, best first
}

// Match is a past decision and how like the present one it is.
type Match struct {
	Decision Decision
	Score    float64 // the two below, weighted
	Words    float64 // how much of the case it shares
	Shape    float64 // how alike its place in the graph is
}

// A precedent is scored mostly on what it was about and partly on where it
// sits, because two decisions worded alike but standing alone in the graph
// are less of a precedent than two that share the ground around them.
const (
	byWords = 0.7
	byShape = 0.3
)

// Like finds the decisions most like a case, best first. It is the search a
// caller makes before deciding: what did we do the last time this came up.
func (j *Journal) Like(a Ask) []Match {
	if a.Case == "" {
		return nil
	}
	g := j.graph()
	want := set(a.Of)
	var out []Match
	for _, d := range j.All() {
		if a.Topic != "" && d.Topic != a.Topic {
			continue
		}
		if !a.live(d.Span) {
			continue
		}
		if len(want) > 0 && !anyOf(d.Because, want) {
			continue
		}
		m := Match{Decision: d, Words: alike(a.Case, said(d))}
		g.mu.RLock()
		if ref, ok := g.nodes[d.ID]; ok {
			best := 0.0
			for _, other := range g.order {
				o := g.nodes[other]
				if other == d.ID || o == nil || o.Kind != kindDecision {
					continue
				}
				if s := g.shape(ref, o); s > best {
					best = s
				}
			}
			m.Shape = best
		}
		g.mu.RUnlock()
		m.Score = byWords*m.Words + byShape*m.Shape
		if m.Score < a.Floor {
			continue
		}
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, k int) bool { return out[i].Score > out[k].Score })
	if a.N > 0 && len(out) > a.N {
		out = out[:a.N]
	}
	return out
}

// said is everything about a decision that a case can be compared against.
func said(d Decision) string {
	s := d.Case + " " + d.Reason
	for _, e := range d.Because {
		s += " " + e
	}
	return s
}

// live answers the temporal half of a search. Naming a moment asks what held
// then. Naming none asks what holds now, unless the caller wants what has
// since been superseded as well — which is a different question, and worth
// asking, since the precedent that was overruled is often the interesting one.
func (a Ask) live(s Span) bool {
	if !a.At.IsZero() {
		return s.Holds(a.At)
	}
	if a.Old {
		return true
	}
	return s.Holds(time.Now().UTC())
}

func anyOf(have []string, want map[string]bool) bool {
	for _, h := range have {
		if want[h] {
			return true
		}
	}
	return false
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func num(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}

func stamp(v any) time.Time {
	t, _ := v.(time.Time)
	return t
}
