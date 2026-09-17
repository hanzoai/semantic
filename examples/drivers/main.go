// Command drivers is the set of stores semantica knows about, and what each
// name means. A driver in another package adds itself in an init and a caller
// reaches it by importing that package for its effect, the way a program
// reaches a database driver.
//
// Three answers are possible and they are different: a name that opens, a name
// this build knows of and cannot serve, and a name nobody has heard of. The
// middle one is why the unimplemented drivers are registered at all — a caller
// can tell "not in this build" from "not a thing".
//
// The two remote drivers are exercised against stub servers this program
// starts on loopback. The stubs answer the requests the drivers make and
// nothing else; what the run shows is the wire protocol each driver speaks, so
// pointing the same DSN at a real Qdrant or a real Fuseki changes only the
// address.
//
//	go run ./examples/drivers
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/hanzoai/semantic/store"
	qdrantdriver "github.com/hanzoai/semantic/store/qdrant"
	sparqldriver "github.com/hanzoai/semantic/store/sparql"
)

func main() {
	ctx := context.Background()

	registry()
	answers(ctx)
	own(ctx)
	qdrant(ctx)
	sparql(ctx)
}

// registry is the list. Importing a driver package for its effect is what puts
// its name here, so the set is one list a program can read rather than
// something a caller learns by an import failing.
func registry() {
	fmt.Println("registered drivers")
	fmt.Printf("  vectors  %v\n", store.Vectors.Names())
	fmt.Printf("  graphs   %v\n", store.Graphs.Names())
	fmt.Printf("  triples  %v\n", store.Triples.Names())
	fmt.Println("  qdrant is in the vector list because this file imports its package for effect;")
	fmt.Println("  the sparql names are there for the same reason, under graphs and triples both,")
	fmt.Println("  because RDF is one store answering two questions")
}

// answers are the three outcomes, side by side.
func answers(ctx context.Context) {
	fmt.Println("\nopening")

	v, err := store.Vectors.Open("mem", "")
	if err != nil {
		log.Fatal(err)
	}
	if err := v.Put(ctx, "a", []float32{1, 0, 0}, map[string]any{"n": 1}); err != nil {
		log.Fatal(err)
	}
	near, err := v.Near(ctx, []float32{1, 0, 0}, 1)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  mem       opens and answers: %s scored %.2f\n", near[0].ID, near[0].Score)

	// A name this build knows of and cannot serve, asked of the registry that
	// holds it. Each is a driver in its own package, waiting to be written
	// against the same three interfaces.
	for _, d := range []struct {
		name, kind string
		open       func(string) error
	}{
		{"faiss", "vectors", func(n string) error { _, err := store.Vectors.Open(n, ""); return err }},
		{"neo4j", "graphs", func(n string) error { _, err := store.Graphs.Open(n, ""); return err }},
		{"oxigraph", "triples", func(n string) error { _, err := store.Triples.Open(n, ""); return err }},
	} {
		err := d.open(d.name)
		fmt.Printf("  %-9s in %-7s and not built in: %v (errors.Is ErrDriver: %v)\n",
			d.name, d.kind, err, errors.Is(err, store.ErrDriver))
	}

	// And a name nobody registered.
	if _, err := store.Vectors.Open("hyperdrive", ""); err != nil {
		fmt.Printf("  %-9s %v\n", "unknown", err)
	}
	fmt.Printf("  Has(\"neo4j\") is %v even though Open fails: registered is not the same as available\n",
		store.Graphs.Has("neo4j"))
}

// own is a driver of the caller's own, reached the same way as the rest.
func own(ctx context.Context) {
	store.Graphs.Add("null", func(dsn string) (store.Graph, error) { return null{dsn}, nil })
	g, err := store.Graphs.Open("null", "null://nowhere")
	if err != nil {
		log.Fatal(err)
	}
	if err := g.Edge(ctx, "a", "b", "knows"); err != nil {
		log.Fatal(err)
	}
	out, err := g.Out(ctx, "a")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\na driver of your own: Open(\"null\", \"null://nowhere\") → Out(\"a\") = %v\n", out)
}

// qdrant speaks Qdrant's JSON HTTP API directly rather than through a client
// library, so the module stays on the standard library and the requests are
// the ones a reader can check against Qdrant's own documentation.
func qdrant(ctx context.Context) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen = append(seen, fmt.Sprintf("%s %s  %s", r.Method, r.URL.Path, clip(string(body), 76)))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/points/search"):
			fmt.Fprint(w, `{"result":[{"id":"d2","score":0.77,"payload":{"text":"the analytical engine"}}]}`)
		case strings.HasSuffix(r.URL.Path, "/points/count"):
			fmt.Fprint(w, `{"result":{"count":1}}`)
		default:
			fmt.Fprint(w, `{"result":true,"status":"ok"}`)
		}
	}))
	defer srv.Close()

	// Through the interface: the two methods every vector store has.
	v, err := store.Vectors.Open("qdrant", srv.URL+"/docs")
	if err != nil {
		log.Fatal(err)
	}
	if err := v.Put(ctx, "d2", []float32{0, 1, 0}, map[string]any{"text": "the analytical engine"}); err != nil {
		log.Fatal(err)
	}
	near, err := v.Near(ctx, []float32{0, 1, 0}, 3)
	if err != nil {
		log.Fatal(err)
	}

	// And through the driver's own type, which carries more than the
	// interface does: a filter Qdrant applies while it searches, and an exact
	// count. The interface is the common denominator, not the whole store.
	q, err := qdrantdriver.New(srv.URL + "/docs")
	if err != nil {
		log.Fatal(err)
	}
	got, err := q.Search(ctx, store.Query{
		Vec: []float32{0, 1, 0}, K: 3,
		Filter: store.Filter{}.Eq("space", "engines"),
	})
	if err != nil {
		log.Fatal(err)
	}
	n, err := q.Count(ctx)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("\nqdrant  dsn %s/docs — the last path segment names the collection\n", "http://…")
	for _, line := range seen {
		fmt.Printf("  → %s\n", line)
	}
	fmt.Printf("  ← Near: %s scored %.2f\n", near[0].ID, near[0].Score)
	fmt.Printf("  ← Search: %s scored %.2f, payload %v; Count: %d\n", got[0].ID, got[0].Score, got[0].Meta, n)

	// The DSN is one string carrying the server and what to ask it, so a
	// deployment is configuration rather than code.
	if _, err := store.Vectors.Open("qdrant", "http://127.0.0.1:1/docs"); err == nil {
		fmt.Println("  a DSN with nothing behind it opens: the driver connects on the first call, not on Open")
	}
}

// sparql reaches every server that speaks the protocol — Fuseki, Blazegraph,
// RDF4J, GraphDB — because the protocol is the interface. The Python this is
// ported from has a file per server; the difference between them is the URL of
// the query and update endpoints, which is two fields here rather than four
// modules.
func sparql(ctx context.Context) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		text := string(body)
		if v, err := parseForm(text); err == nil {
			text = v
		}
		seen = append(seen, oneline(text))
		w.Header().Set("Content-Type", "application/sparql-results+json")
		json.NewEncoder(w).Encode(map[string]any{
			"head": map[string]any{"vars": []string{"o"}},
			"results": map[string]any{"bindings": []map[string]any{
				{"o": map[string]string{"type": "uri", "value": "https://example.org/London"}},
			}},
		})
	}))
	defer srv.Close()

	st := &sparqldriver.Store{
		Query: srv.URL + "/ds/query", Update: srv.URL + "/ds/update",
		Base: "https://example.org/", Graph: "https://example.org/graph/engines",
	}
	if err := st.Assert(ctx, "BabbageEngines", "locatedIn", "London"); err != nil {
		log.Fatal(err)
	}
	got, err := st.Match(ctx, "BabbageEngines", "locatedIn", "")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("\nsparql  the statements the driver actually sends")
	for _, q := range seen {
		fmt.Printf("  → %s\n", clip(q, 108))
	}
	fmt.Printf("  ← %v\n", got)

	// A term that is not an IRI is refused rather than interpolated, which is
	// what keeps a name out of the query it was supposed to be a value in.
	if _, err := sparqldriver.IRI("not an iri"); errors.Is(err, sparqldriver.ErrIRI) {
		fmt.Printf("  IRI(%q) → %v\n", "not an iri", err)
	}
	fmt.Printf("  Lit(%q) → %s\n", `he said "no"`, sparqldriver.Lit(`he said "no"`))
}

// null is a store that keeps its edges in a map, so the registry has something
// of the caller's own to open.
type null struct{ dsn string }

func (null) Node(context.Context, string, map[string]any) error { return nil }
func (n null) Edge(_ context.Context, from, to, label string) error {
	edges[from] = append(edges[from], to)
	return nil
}
func (n null) Out(_ context.Context, id string) ([]string, error) { return edges[id], nil }

var edges = map[string][]string{}

// parseForm pulls the query or update out of a form-encoded body, which is how
// the protocol carries it.
func parseForm(body string) (string, error) {
	for part := range strings.SplitSeq(body, "&") {
		name, value, ok := strings.Cut(part, "=")
		if ok && (name == "query" || name == "update") {
			return unescape(value)
		}
	}
	return "", errors.New("no query")
}

func unescape(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '+':
			b.WriteByte(' ')
		case s[i] == '%' && i+2 < len(s):
			var v int
			if _, err := fmt.Sscanf(s[i+1:i+3], "%02x", &v); err != nil {
				return "", err
			}
			b.WriteByte(byte(v))
			i += 2
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String(), nil
}

func oneline(s string) string { return strings.Join(strings.Fields(s), " ") }

func clip(s string, n int) string {
	s = oneline(s)
	if len([]rune(s)) > n {
		return string([]rune(s)[:n-1]) + "…"
	}
	return s
}
