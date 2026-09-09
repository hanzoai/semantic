package main

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/hanzoai/semantic"
	"github.com/hanzoai/semantic/extract"
	"github.com/hanzoai/semantic/kg"
	"github.com/hanzoai/semantic/reason"
	"github.com/hanzoai/semantic/store"
)

// The other arm: remember a conversation as a graph of who did what, and
// answer by asking the graph about the person the question names.
//
// The whole difference from the baseline is one word in the query. A vector
// index is asked "which turn is nearest these words". A graph is asked "what
// does this person have to do with these words", and the person is a node, so
// the question cannot be answered out of a turn about somebody else however
// close its wording. That distinction is what the adversarial questions are
// built to punish and what the multi-hop questions need, since one person's
// several answers were said months apart and are one node's several edges here.
//
// Both arms rank the same way, by cosine over the same weighted terms, and
// what comes back is turns either way. The one thing that differs is what is
// indexed: the baseline indexes turns, this indexes what one person said in
// one turn, and asks only among the pieces belonging to the person named. That
// makes the comparison an ablation of the index rather than of two systems.

// Knowledge is a conversation kept as a graph.
type Knowledge struct {
	turns  []Turn
	words  *Words
	graph  *kg.Graph
	mem    *store.Mem       // one vector per piece, filtered by whose it is
	piece  map[string]Piece // piece id to what it holds
	people []string         // every name, longest first
	claims int
	facts  int // what the reasoner added on top of what was extracted
}

// Piece is what one person said in one turn: the claims from that turn whose
// subject is that person, and the sentences those claims came from. It is the
// unit this memory indexes, where the baseline indexes the whole turn — one
// turn holds as many pieces as it has people spoken of, and a question reaches
// only the pieces of the person it names.
//
// The claims decide which sentences belong to the piece; the sentences are
// what it is ranked on. Ranking on the claims alone would measure how well the
// extractor paraphrases rather than what the graph knows about whom.
//
// Terms are reduced when the piece is built. A memory reduces what it holds
// once and is asked about it thousands of times.
type Piece struct {
	Turn   int
	Who    string // whose it is, folded, which is what the query filters on
	Said   string // the sentences the claims were read from
	Terms  map[string]bool
	Claims []Held
}

// Held is one claim as the graph keeps it for querying.
type Held struct {
	Object string
	Terms  map[string]bool
}

// NewKnowledge runs the pipeline over a conversation: read every turn for what
// it claims, fold the claims into a graph so that a person spoken of twice is
// one node with two edges, then close the graph under what its links imply.
func NewKnowledge(ctx context.Context, s Sample, turns []Turn, words *Words) (*Knowledge, error) {
	k := &Knowledge{
		turns: turns, words: words,
		mem: &store.Mem{}, piece: map[string]Piece{},
	}

	names := Named(turns)
	for _, w := range s.Who {
		names[strings.ToLower(w)] = true
	}
	for n := range names {
		k.people = append(k.people, n)
	}
	sort.Slice(k.people, func(i, j int) bool {
		if len(k.people[i]) != len(k.people[j]) {
			return len(k.people[i]) > len(k.people[j])
		}
		return k.people[i] < k.people[j]
	})

	var claims []Claim
	var facts []semantic.Triple
	for i, t := range turns {
		other := s.Who[0]
		if t.Who == other {
			other = s.Who[1]
		}
		for _, c := range Read(t, i, t.Who, other, names) {
			claims = append(claims, c)
			facts = append(facts, semantic.Triple{
				Subject:   c.Subject,
				Predicate: c.Predicate,
				Object:    c.Object,
				From:      semantic.Chunk{DocID: t.ID, Index: i},
				Score:     c.Score,
			})
		}
	}
	k.claims = len(facts)

	// Stopping at the bound is the plan, not a failure: the engine reports it
	// because it stopped before it could prove the closure complete, and the
	// facts it did derive are sound and explained either way.
	model, err := reason.Engine{Rules: []reason.Rule{carry}, Depth: rounds}.Run(ctx, facts)
	if err != nil && !errors.Is(err, reason.ErrDepth) {
		return nil, err
	}
	at := make(map[string]int, len(turns))
	for i, t := range turns {
		at[t.ID] = i
	}
	for _, f := range model.Derived() {
		// A derived fact holds no turn of its own. The turns it rests on are
		// the turns behind its premises, so walking the derivation is what
		// carries provenance forward: a claim that cannot be traced back to
		// something somebody said is not evidence.
		why, err := model.Why(f.Key())
		if err != nil {
			return nil, err
		}
		for _, leaf := range why {
			i, said := at[leaf.From.DocID]
			if !said {
				continue
			}
			claims = append(claims, Claim{
				Link: extract.Link{
					Subject:   f.Subject,
					Predicate: f.Predicate,
					Object:    f.Object,
					Score:     f.Score,
					From:      Day(turns[i].Date),
					Cite:      turns[i].ID,
				},
				Turn: i,
			})
		}
		facts = append(facts, f.Triple)
	}
	k.facts = len(model.Derived())
	k.graph = kg.Build(facts)
	return k, k.file(ctx, claims)
}

// rounds bounds the closure. One round is what the naming rule needs — a name
// is one hop from the thing it names — and bounding it is what keeps a rule
// whose predicate is a variable from recombining its own conclusions.
const rounds = 1

// file gathers the claims into pieces, one per person per turn, and puts each
// into the store under whose it is. Whose it is rides along as metadata, which
// is what the query filters on: a namespace is a filter, not another store.
func (k *Knowledge) file(ctx context.Context, claims []Claim) error {
	// Weigh returns unit vectors, so the inner product is the cosine.
	k.mem.Rank(store.Inner)

	type where struct {
		who  string
		turn int
	}
	order := []where{}
	group := map[where][]Claim{}
	for _, c := range claims {
		at := where{kg.Fold(c.Subject), c.Turn}
		if group[at] == nil {
			order = append(order, at)
		}
		group[at] = append(group[at], c)
	}
	for _, at := range order {
		piece := Piece{Turn: at.turn, Who: at.who, Terms: map[string]bool{}}
		var said []string
		for _, c := range group[at] {
			held := Held{Object: c.Object, Terms: Terms(c.Predicate + " " + c.Object)}
			for term := range held.Terms {
				piece.Terms[term] = true
			}
			piece.Claims = append(piece.Claims, held)
			// A claim carries the sentence it was read from; one the reasoner
			// derived carries none, and contributes its own words instead, so
			// that everything the piece holds is reachable by a query.
			words := c.Said
			if words == "" {
				words = c.Predicate + " " + c.Object
			}
			if !contains(said, words) {
				said = append(said, words)
			}
		}
		piece.Said = strings.Join(said, " ")
		id := at.who + "@" + k.turns[at.turn].ID
		k.piece[id] = piece
		if err := k.mem.Put(ctx, id, k.words.Weigh(piece.Said),
			map[string]any{store.Space: at.who}); err != nil {
			return err
		}
	}
	return nil
}

// Name is what this arm is called in the results.
func (k *Knowledge) Name() string { return "graph" }

// Nodes, Edges, Claims and Facts report what was built, for the run's summary.
func (k *Knowledge) Nodes() int  { n, _ := k.graph.Size(); return n }
func (k *Knowledge) Edges() int  { _, e := k.graph.Size(); return e }
func (k *Knowledge) Claims() int { return k.claims }
func (k *Knowledge) Facts() int  { return k.facts }

// Recall answers a question by looking up the person it names and ranking what
// the graph knows about them. Every turn returned is a turn in which that
// person was the subject of a sentence.
//
// A question naming nobody the graph knows falls back to ranking everything,
// which is the honest thing for a memory asked about a stranger. It happens
// for about one question in a hundred here.
func (k *Knowledge) Recall(ctx context.Context, question string, n int) ([]Cite, error) {
	return k.recall(ctx, question, n, true)
}

// recall ranks the pieces, either the named person's or everybody's. Scoping
// is the one factor separating this arm from the baseline that is not about
// what is indexed, so it is a parameter here and an arm in factor.go rather
// than something only the graph does.
func (k *Knowledge) recall(ctx context.Context, question string, n int, scope bool) ([]Cite, error) {
	subject := k.fold(question)
	query := store.Query{Vec: k.words.Weigh(question), K: n}
	if scope && subject != "" {
		query.Filter = store.Filter{}.Eq(store.Space, subject)
	}
	near, err := k.mem.Search(ctx, query)
	if err != nil {
		return nil, err
	}

	focus := k.words.Focus(question, subject)
	out := make([]Cite, 0, len(near))
	for _, m := range near {
		if m.Score <= 0 {
			break // a piece with no word in common with the question is not evidence
		}
		p := k.piece[m.ID]
		say, top := "", -1.0
		for _, held := range p.Claims {
			if c := k.words.Cover(focus, held.Terms); c > top {
				say, top = held.Object, c
			}
		}
		out = append(out, Cite{Turn: k.turns[p.Turn], Score: m.Score, Say: say})
	}
	return out, nil
}

// fold is the subject of a question, in the form the store files people under.
func (k *Knowledge) fold(question string) string { return kg.Fold(k.subject(question)) }

// subject is the person a question is about: the longest name the graph knows
// that the question contains. LoCoMo questions name their subject 98.9% of the
// time, which is what makes a lookup on a person the right query for them.
func (k *Knowledge) subject(question string) string {
	low := strings.ToLower(question)
	for _, name := range k.people {
		if mentions(low, name) {
			return name
		}
	}
	return ""
}

// mentions reports whether a text names someone: the name on its own, rather
// than inside a longer word.
func mentions(text, name string) bool {
	for i := 0; ; {
		j := strings.Index(text[i:], name)
		if j < 0 {
			return false
		}
		j += i
		before := j == 0 || !letter(rune(text[j-1]))
		after := j+len(name) == len(text) || !letter(rune(text[j+len(name)]))
		if before && after {
			return true
		}
		i = j + 1
	}
}

// carry is the one thing worth inferring here: what a thing is called is also
// what its owner has. "I have a snake named Susie" says the speaker has a
// snake and that the snake is Susie; a question asking the snake's name needs
// Susie on the speaker, and this is the rule that puts it there.
var carry = reason.Rule{
	Name: "naming",
	Body: []reason.Pattern{
		{Subject: "?who", Predicate: "?did", Object: "?what"},
		{Subject: "?what", Predicate: "named", Object: "?name"},
	},
	Head: reason.Pattern{Subject: "?who", Predicate: "?did", Object: "?name"},
}
