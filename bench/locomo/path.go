package main

import (
	"context"
	"sort"
	"strings"

	"github.com/hanzoai/semantic/kg"
)

// The arm that reads the graph.
//
// Every other arm ranks text: a turn, or one person's claims inside a turn,
// against the question's words. The graph is built, measured, printed and then
// never read — emptying it leaves every other column of the table byte for
// byte where it was, which is the plainest statement there is that the
// structure does no work. This arm answers out of the structure. It stands on
// the nodes the question names, takes the assertions made out of them, gathers
// those into the relations they belong to, and cites the turns behind the
// relation that answers best. A turn arrives here because an assertion about
// the person the question named matched, not because the turn's wording sat
// near the question's. Run with the graph emptied this arm falls silent, and
// that is how the two claims are told apart.
//
// It walks one hop. Two and three were built and measured: guided, the second
// hop moves the multi-hop questions by nothing and costs 0.029 on the
// adversarial ones, and the third changes no figure at three decimals;
// unguided, the second hop is worth 0.003 on multi-hop and costs 0.258 on
// adversarial, because the neighbourhood of a speaker is most of the
// conversation and the adversarial questions are built to punish exactly that.
// Reach was never the constraint — one hop from a speaker already reaches 90%
// of the evidence for the multi-hop questions, which reach_test.go asserts.
// Choosing among what is reached is the whole problem, and going further out
// only makes more to choose from.

// Path is a conversation remembered as a graph and read by walking it.
type Path struct {
	know *Knowledge
	// says is what each assertion is made of, reduced once. An edge is
	// compared against a question a few thousand times over a run and its
	// wording never changes, so it is reduced when the arm is built.
	says map[string]said
}

// said is one assertion's wording, kept both ways round because the comparison
// is made in both directions.
type said struct {
	list []string
	set  map[string]bool
}

// NewPath prepares a walker over a graph already built.
func NewPath(k *Knowledge) *Path {
	p := &Path{know: k, says: map[string]said{}}
	for _, e := range k.graph.Edges() {
		list := stems(e.Label + " " + e.To)
		p.says[e.ID()] = said{list: list, set: unique(list)}
	}
	return p
}

// Name is what this arm is called in the results.
func (p *Path) Name() string { return "path" }

// reached is one assertion the walk found, and how well it answers the
// question.
type reached struct {
	edge  kg.Edge
	score float64
}

// Recall stands on the people and things the question names and returns the
// turns behind the relations that best answer it.
func (p *Path) Recall(ctx context.Context, question string, n int) ([]Cite, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	subject := p.know.fold(question)
	focus := p.know.words.Focus(question, subject)
	want := unique(focus)

	var found []reached
	seen := map[string]bool{}
	for _, node := range p.starts(question) {
		for _, e := range append(p.know.graph.From(node), p.know.graph.To(node)...) {
			id := e.ID()
			if seen[id] {
				continue
			}
			seen[id] = true
			if score := p.fit(p.says[id], focus, want); score > 0 {
				found = append(found, reached{edge: e, score: score})
			}
		}
	}
	// Ties are settled on the assertion's own identity so that a run is the
	// same run twice; a map has no order and the score is a float.
	sort.Slice(found, func(i, j int) bool {
		if found[i].score != found[j].score {
			return found[i].score > found[j].score
		}
		return found[i].edge.ID() < found[j].edge.ID()
	})

	// The answer comes back a relation at a time, not an assertion at a time.
	//
	// This is where reading a graph stops being a ranking. The assertions out
	// of one node under one predicate are a set — where Melanie camped is the
	// beach and the mountains and the forest, three edges of one adjacency
	// list — and a question about where she camped asks for the set and not
	// for its best member. So the best-fitting assertion brings its whole list
	// with it before the next relation is reached, and how wide an answer runs
	// follows from how wide the relation is.
	//
	// Ranking turns by wording cannot do this. The turns that say a thing most
	// clearly are the ones nearest the question, so the five best turns are
	// five sayings of one thing; the five best relations are five things.
	//
	// The relation leads and each value keeps its own score, so a reader that
	// wants one answer can still tell the value that fits the question from the
	// one that merely shares its relation. Scoring the whole relation instead —
	// its predicate's fit plus its best value's — was tried and is worse: a
	// relation with more values has more chances at a good one, so the score
	// rewards breadth and the walk settles on a large loose relation over a
	// small exact one. Evidence recall on the multi-hop questions falls from
	// 0.222 to 0.050 under it.
	kin := map[link][]reached{}
	for _, r := range found {
		at := link{r.edge.From, relation(r.edge.Label)}
		kin[at] = append(kin[at], r)
	}

	out := make([]Cite, 0, n)
	told := map[string]bool{}
	value := map[string]bool{}
	done := map[link]bool{}
	for _, r := range found {
		at := link{r.edge.From, relation(r.edge.Label)}
		if done[at] {
			continue
		}
		done[at] = true
		for _, m := range kin[at] {
			if value[m.edge.To] {
				continue
			}
			for _, doc := range m.edge.Docs {
				i, held := p.know.at[doc]
				if !held || told[doc] {
					continue
				}
				told[doc], value[m.edge.To] = true, true
				out = append(out, Cite{Turn: p.know.turns[i], Score: m.score, Say: m.edge.To})
				break
			}
			if len(out) == n {
				return out, nil
			}
		}
	}
	return out, nil
}

// link names one relation: a node and what it asserts. Its edges are that
// relation's values, and they are what a question about the relation is
// asking for.
type link struct{ from, label string }

// relation is the form of a predicate two tenses of one verb share, which is
// what decides whether two assertions belong to one adjacency list.
//
// A conversation held over months says the same thing in whatever tense the
// month calls for. Melanie read a book in May and is reading another in
// September, and a graph that files those under "read" and "reading" has two
// relations with one value each where it should have one with two — so a
// question about what she has read finds half the answer and has no way of
// knowing it. Stemming is the same reduction the vocabulary and the scorer
// already apply to every other word here, applied to the predicate. It folds
// 3,472 relations into 2,866 and takes the average relation from 6.15 values
// to 7.45. The negation stays a word of its own, because not reading is not a
// tense of reading.
func relation(label string) string {
	parts := strings.Fields(kg.Fold(label))
	for i, w := range parts {
		parts[i] = stem(w)
	}
	return strings.Join(parts, " ")
}

// fit is how well one assertion answers a question, read in both directions.
//
// How much of the question the assertion accounts for is what makes it
// relevant, and it is the only thing the rest of this benchmark measures. It
// is not enough here, because a speaker is the subject of about a thousand
// assertions and a question's focus is often one word, so relevance alone
// saturates: everything Melanie said about camping covers "camp" completely
// and the order among them is then arbitrary. How much of the assertion is
// what was asked breaks that tie, and it is the right tie to break — "camped
// at the beach" answers where she camped, and "went on a camping trip with the
// kids last summer" is the same relevance wrapped in five other things.
func (p *Path) fit(s said, focus []string, want map[string]bool) float64 {
	hit := p.know.words.Cover(focus, s.set)
	if hit == 0 {
		return 0
	}
	return hit * p.know.words.Cover(s.list, want)
}

// starts are the nodes the walk stands on: every person or thing the question
// names that the graph holds.
//
// The scoped arms resolve a question to one subject because a vector filter
// takes one namespace. A graph has no such limit, and a question relating two
// people is precisely the kind it should be able to answer, so every name it
// holds is a foot on the ground.
func (p *Path) starts(question string) []string {
	low := strings.ToLower(question)
	var out []string
	for _, name := range p.know.people {
		if !mentions(low, name) {
			continue
		}
		id := kg.Fold(name)
		if _, held := p.know.graph.Node(id); held && !contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}
