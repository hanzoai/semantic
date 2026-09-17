// Command store uses the three ways a graph is kept — by similarity, by edge,
// by assertion — and they are three interfaces because they answer three
// questions: what is like this, what is connected to this, what was asserted.
// Mem implements all three, so a caller that wants one and a caller that wants
// all three use the same object.
//
// File is the same store with a log behind it: each write is appended as one
// JSON record and flushed before the call returns, so reopening the file
// replays exactly what was acknowledged.
//
// The vectors come from a bag of words over a fixed vocabulary, so the example
// runs offline and its numbers are reproducible. A real embedder goes behind
// store.Vector without changing anything below.
//
//	go run ./examples/store
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/hanzoai/semantic/store"
)

// docs are what gets indexed. The last two are about a different subject, so
// a query about engines should rank them last.
var docs = []struct {
	id, text, space string
}{
	{"d1", "Babbage Engines builds difference engines in London", "engines"},
	{"d2", "The analytical engine was designed by Charles Babbage", "engines"},
	{"d3", "Ada Lovelace wrote the first program for the analytical engine", "engines"},
	{"d4", "Menabrea Works assembles looms in Montréal", "looms"},
	{"d5", "The Jacquard loom uses punched cards", "looms"},
}

func main() {
	ctx := context.Background()

	m := &store.Mem{}
	vectors(ctx, m)
	graph(ctx, m)
	triples(ctx, m)
	metrics(ctx)
	hybrid(ctx, m)
	durable(ctx, m)
}

// vectors is the "what is like this" question. A filter narrows the search
// without being a second kind of store: a namespace is a tag on the way in and
// a condition on the way out.
func vectors(ctx context.Context, m *store.Mem) {
	for _, d := range docs {
		if err := m.Put(ctx, d.id, embed(d.text), map[string]any{
			store.Space: d.space, "text": d.text, "words": len(strings.Fields(d.text)),
		}); err != nil {
			log.Fatal(err)
		}
	}

	fmt.Println("nearest to \"who designed the analytical engine\"")
	q := embed("who designed the analytical engine")
	near, err := m.Near(ctx, q, 3)
	if err != nil {
		log.Fatal(err)
	}
	for _, h := range near {
		fmt.Printf("  %.3f  %s  %s\n", h.Score, h.ID, h.Meta["text"])
	}

	// A filter and a vector, together. The filter is a conjunction, and its
	// zero value passes everything, so a query with no filter is not a
	// special case.
	got, err := m.Search(ctx, store.Query{
		Vec: q, K: 3,
		Filter: store.Filter{}.Eq(store.Space, "looms").Ge("words", 6),
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("\nsame query, restricted to the looms namespace and documents of six words or more")
	for _, h := range got {
		fmt.Printf("  %.3f  %s  %s\n", h.Score, h.ID, h.Meta["text"])
	}
	fmt.Println("  they score zero: the query shares no vocabulary with them, and the filter is what put them here")

	// A nil vector ranks nothing and returns whatever the filter admits,
	// which is how a metadata-only lookup is asked for.
	got, err = m.Search(ctx, store.Query{Filter: store.Filter{}.Has("text", "Babbage")})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\nmetadata only, text contains \"Babbage\": %s\n", ids(got))

	// An absent field fails every operator: an absent field is not evidence
	// of inequality.
	got, _ = m.Search(ctx, store.Query{Filter: store.Filter{}.Ne("missing", "x")})
	fmt.Printf("Ne on a field nothing carries matches %d documents\n", len(got))

	if err := m.Put(ctx, "wrong", []float32{1, 2}, nil); errors.Is(err, store.ErrDim) {
		fmt.Printf("a vector of the wrong length is refused: %v\n", err)
	}
	s := m.Stat()
	fmt.Printf("store holds %d vectors; Metric() on a store nobody set reports %q, and it ranks as cosine\n",
		s.Vectors, m.Metric())
}

// graph is the "what is connected to this" question, on the same object.
func graph(ctx context.Context, m *store.Mem) {
	links := [][3]string{
		{"ada lovelace", "babbage engines", "works_for"},
		{"charles babbage", "babbage engines", "works_for"},
		{"babbage engines", "london", "located_in"},
		{"babbage engines", "analytical engine", "built"},
		{"luigi menabrea", "menabrea works", "works_for"},
		{"menabrea works", "montréal", "located_in"},
	}
	for _, l := range links {
		if err := m.Edge(ctx, l[0], l[1], l[2]); err != nil {
			log.Fatal(err)
		}
	}
	if err := m.Node(ctx, "ada lovelace", map[string]any{"role": "analyst", "born": 1815}); err != nil {
		log.Fatal(err)
	}

	fmt.Println("\ngraph")
	out, _ := m.Out(ctx, "babbage engines")
	in, _ := m.In(ctx, "babbage engines")
	fmt.Printf("  babbage engines points at %v\n", out)
	fmt.Printf("  and is pointed at by      %v\n", in)

	path, _ := m.Path(ctx, "ada lovelace", "london")
	fmt.Printf("  ada lovelace → london: %v\n", path)

	reach, _ := m.Reach(ctx, "ada lovelace", 2)
	fmt.Printf("  within two hops of ada lovelace: %v\n", reach)

	din, dout := m.Degree("babbage engines")
	fmt.Printf("  degree of babbage engines: %d in, %d out\n", din, dout)
	fmt.Printf("  components: %v\n", m.Components())

	props, _ := m.Props("ada lovelace")
	fmt.Printf("  properties: %v\n", props)
}

// triples is the "what was asserted" question. An empty term in a pattern
// stands for any, so one call answers every shape of question.
func triples(ctx context.Context, m *store.Mem) {
	for _, t := range [][3]string{
		{"Babbage Engines", "founded_by", "Charles Babbage"},
		{"Babbage Engines", "located_in", "London"},
		{"Menabrea Works", "located_in", "Montréal"},
		{"Babbage Engines", "founded_by", "Charles Babbage"}, // the same fact twice
	} {
		if err := m.Assert(ctx, t[0], t[1], t[2]); err != nil {
			log.Fatal(err)
		}
	}

	fmt.Println("\ntriples")
	all, _ := m.Match(ctx, "", "", "")
	fmt.Printf("  %d asserted, four calls: the same fact twice is one fact\n", len(all))
	where, _ := m.Match(ctx, "", "located_in", "")
	fmt.Printf("  located_in: %v\n", where)

	if err := m.Retract(ctx, "Menabrea Works", "", ""); err != nil {
		log.Fatal(err)
	}
	all, _ = m.Match(ctx, "", "", "")
	fmt.Printf("  after retracting everything about Menabrea Works: %d\n", len(all))
}

// metrics are the three ways nearness is measured. Larger is nearer under all
// three — Euclid is negated for it — so a caller ranks the same way whichever
// is in use.
func metrics(ctx context.Context) {
	fmt.Println("\nmetrics")
	q := embed("analytical engine")
	fmt.Printf("  %-8s %s\n", "metric", "top three")
	for _, metric := range []store.Metric{store.Cosine, store.Inner, store.Euclid} {
		m := &store.Mem{}
		m.Rank(metric)
		for _, d := range docs {
			if err := m.Put(ctx, d.id, embed(d.text), map[string]any{"text": d.text}); err != nil {
				log.Fatal(err)
			}
		}
		got, err := m.Near(ctx, q, 3)
		if err != nil {
			log.Fatal(err)
		}
		var parts []string
		for _, h := range got {
			parts = append(parts, fmt.Sprintf("%s %.2f", h.ID, h.Score))
		}
		fmt.Printf("  %-8s %s\n", metric, strings.Join(parts, "   "))
	}
	fmt.Println("  cosine ignores length, inner counts it, euclid is distance negated —")
	fmt.Println("  and these vectors are unit length, so cosine and inner agree here")
}

// hybrid combines two rankings. Which combiner to use is decided by whether
// the sources score on one scale: Blend sums the scores, Fuse uses only the
// orders, which is what to reach for when a vector store and a text index
// disagree about what a number means.
func hybrid(ctx context.Context, m *store.Mem) {
	dense, err := m.Near(ctx, embed("engine designed in London"), 5)
	if err != nil {
		log.Fatal(err)
	}
	// A keyword ranking, scored on its own scale: how many query words a
	// document carries.
	sparse := keyword("engine designed London", 5)

	fmt.Println("\nhybrid")
	fmt.Printf("  vectors  %s\n", ids(dense))
	fmt.Printf("  keywords %s\n", ids(sparse))
	fmt.Printf("  Fuse     %s   (reciprocal rank: orders only)\n", ids(store.Fuse(60, dense, sparse)))
	fmt.Printf("  Blend    %s   (weighted sum: needs one scale)\n", ids(store.Blend([]float64{0.7, 0.3}, dense, sparse)))
}

// durable is the same store with a file behind it. A record counts only if it
// is terminated and decodes, so a process that dies mid-write leaves a partial
// last line that the next open discards: the write that never returned never
// happened.
func durable(ctx context.Context, m *store.Mem) {
	dir, err := os.MkdirTemp("", "semantic-store")
	if err != nil {
		log.Fatal(err)
	}
	path := filepath.Join(dir, "graph.log")

	f, err := store.Open(path)
	if err != nil {
		log.Fatal(err)
	}
	// Dump and Load are the two halves of a snapshot: what one writes, the
	// other reads back.
	if err := loadInto(ctx, f, m.Dump()); err != nil {
		log.Fatal(err)
	}
	if err := f.Close(); err != nil {
		log.Fatal(err)
	}
	grown := size(path)

	again, err := store.Open(path)
	if err != nil {
		log.Fatal(err)
	}
	defer again.Close()
	s := again.Stat()
	fmt.Printf("\nfile  reopened %s\n", filepath.Base(again.Name()))
	fmt.Printf("  %d vectors, %d nodes, %d edges, %d facts replayed from a %d byte log\n",
		s.Vectors, s.Nodes, s.Edges, s.Facts, grown)

	near, err := again.Near(ctx, embed("who designed the analytical engine"), 2)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  and it answers the same query: %s\n", ids(near))

	// The log grows with every write. Compact folds it into one snapshot,
	// written beside the file and renamed over it, so an interrupted
	// compaction leaves the old log intact.
	if err := again.Compact(); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  compacted: %d bytes → %d\n", grown, size(path))
}

// loadInto writes a snapshot through the store interfaces rather than by
// assignment, which is what a driver that is not Mem would have to do.
func loadInto(ctx context.Context, f *store.File, s store.Snap) error {
	for _, it := range s.Items {
		if err := f.Put(ctx, it.ID, it.Vec, it.Meta); err != nil {
			return err
		}
	}
	for _, n := range s.Nodes {
		if err := f.Node(ctx, n.ID, n.Meta); err != nil {
			return err
		}
	}
	for _, l := range s.Links {
		if err := f.Edge(ctx, l.From, l.To, l.Label); err != nil {
			return err
		}
	}
	for _, t := range s.Facts {
		if err := f.Assert(ctx, t[0], t[1], t[2]); err != nil {
			return err
		}
	}
	return nil
}

// vocab is the whole vocabulary of the toy embedder below. A dimension per
// word, set to how often a text uses it.
var vocab = []string{"babbage", "engines", "engine", "analytical", "difference",
	"london", "montréal", "ada", "lovelace", "charles", "program", "designed",
	"menabrea", "works", "loom", "looms", "jacquard", "punched", "cards", "builds"}

func embed(text string) []float32 {
	v := make([]float32, len(vocab))
	low := strings.ToLower(text)
	for w := range strings.FieldsSeq(low) {
		w = strings.Trim(w, ".,")
		for i, t := range vocab {
			if w == t {
				v[i]++
			}
		}
	}
	return store.Unit(v)
}

// keyword is a second ranking on its own scale: the share of query words a
// document carries.
func keyword(query string, k int) []store.Match {
	words := strings.Fields(strings.ToLower(query))
	var out []store.Match
	for _, d := range docs {
		low := strings.ToLower(d.text)
		hit := 0
		for _, w := range words {
			if strings.Contains(low, w) {
				hit++
			}
		}
		if hit > 0 {
			out = append(out, store.Match{ID: d.id, Score: float64(hit) / float64(len(words))})
		}
	}
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j].Score > out[i].Score {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	if len(out) > k {
		out = out[:k]
	}
	return out
}

func ids(ms []store.Match) string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = fmt.Sprintf("%s(%.2f)", m.ID, m.Score)
	}
	return strings.Join(out, " ")
}

func size(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		log.Fatal(err)
	}
	return fi.Size()
}
