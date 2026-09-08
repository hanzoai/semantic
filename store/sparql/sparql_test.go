package sparql

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/hanzoai/semantic/store"
)

// ask is one request the store made: which endpoint it went to and the one
// query or update it carried.
type ask struct {
	path   string
	query  string
	update string
	accept string
	user   string
}

// serve stands in for a SPARQL endpoint. It records what it was asked and
// answers with the results JSON it was told to.
func serve(t *testing.T, answer string) (*Store, *[]ask) {
	t.Helper()
	var got []ask
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			t.Errorf("sent as %q, want a form: it is what the protocol says", ct)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		form, err := url.ParseQuery(string(body))
		if err != nil {
			t.Fatalf("body %q is not a form: %v", body, err)
		}
		a := ask{
			path:   r.URL.Path,
			query:  form.Get("query"),
			update: form.Get("update"),
			accept: r.Header.Get("Accept"),
		}
		if u, _, ok := r.BasicAuth(); ok {
			a.user = u
		}
		got = append(got, a)
		w.Header().Set("Content-Type", "application/sparql-results+json")
		if answer == "" {
			answer = `{"head":{"vars":[]},"results":{"bindings":[]}}`
		}
		w.Write([]byte(answer))
	}))
	t.Cleanup(srv.Close)

	s, err := New(srv.URL + "/ds/query")
	if err != nil {
		t.Fatal(err)
	}
	s.HTTP = srv.Client()
	return s, &got
}

func one(t *testing.T, got []ask) ask {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("made %d requests, want one: %+v", len(got), got)
	}
	return got[0]
}

func TestNew(t *testing.T) {
	for _, c := range []struct {
		name, dsn  string
		query      string
		user, pass string
		bad        bool
	}{
		{name: "fuseki", dsn: "http://localhost:3030/ds/query", query: "http://localhost:3030/ds/query"},
		{name: "blazegraph", dsn: "http://localhost:9999/blazegraph/namespace/kb/sparql", query: "http://localhost:9999/blazegraph/namespace/kb/sparql"},
		{name: "credentials", dsn: "https://ada:secret@graph.internal/repositories/kb", query: "https://graph.internal/repositories/kb", user: "ada", pass: "secret"},
		{name: "not a url", dsn: "::nonsense", bad: true},
		{name: "not http", dsn: "ftp://localhost/ds", bad: true},
		{name: "no host", dsn: "http:///ds", bad: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, err := New(c.dsn)
			if c.bad {
				if err == nil {
					t.Fatalf("read %q as %+v, want a complaint", c.dsn, s)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if s.Query != c.query || s.User != c.user || s.Pass != c.pass {
				t.Errorf("read %q as query %q user %q pass %q", c.dsn, s.Query, s.User, s.Pass)
			}
		})
	}
}

func TestAssert(t *testing.T) {
	s, got := serve(t, "")
	err := s.Assert(t.Context(), "http://x/ada", "http://x/wrote", "http://x/notes")
	if err != nil {
		t.Fatal(err)
	}
	want := "INSERT DATA { <http://x/ada> <http://x/wrote> <http://x/notes> . }"
	if u := one(t, *got).update; u != want {
		t.Errorf("sent %q,\n want %q", u, want)
	}
}

func TestAssertLiteralObject(t *testing.T) {
	s, got := serve(t, "")
	err := s.Assert(t.Context(), "http://x/ada", "http://x/name", "Ada Lovelace")
	if err != nil {
		t.Fatal(err)
	}
	want := `INSERT DATA { <http://x/ada> <http://x/name> "Ada Lovelace" . }`
	if u := one(t, *got).update; u != want {
		t.Errorf("sent %q,\n want %q", u, want)
	}
}

func TestAssertWithBase(t *testing.T) {
	s, got := serve(t, "")
	s.Base = "http://x/"
	// The subject and predicate name things and become IRIs; the object
	// does not read as one, so it stays a literal.
	if err := s.Assert(t.Context(), "ada", "name", "Ada"); err != nil {
		t.Fatal(err)
	}
	want := `INSERT DATA { <http://x/ada> <http://x/name> "Ada" . }`
	if u := one(t, *got).update; u != want {
		t.Errorf("sent %q,\n want %q", u, want)
	}
}

func TestAssertWithoutBaseRefusesABareName(t *testing.T) {
	s, got := serve(t, "")
	err := s.Assert(t.Context(), "ada", "http://x/name", "Ada")
	if !errors.Is(err, ErrIRI) {
		t.Errorf("gave %v, want ErrIRI", err)
	}
	if len(*got) != 0 {
		t.Errorf("asked the server anyway: %+v", *got)
	}
}

func TestAddBatchesIntoOneUpdate(t *testing.T) {
	s, got := serve(t, "")
	err := s.Add(t.Context(),
		[3]string{"http://x/a", "http://x/p", "http://x/b"},
		[3]string{"http://x/b", "http://x/p", "http://x/c"},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := "INSERT DATA { <http://x/a> <http://x/p> <http://x/b> . <http://x/b> <http://x/p> <http://x/c> . }"
	if u := one(t, *got).update; u != want {
		t.Errorf("sent %q,\n want %q", u, want)
	}
	if err := s.Add(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 1 {
		t.Error("adding nothing made a request")
	}
}

func TestNamedGraph(t *testing.T) {
	s, got := serve(t, "")
	s.Graph = "http://x/kb"
	if err := s.Assert(t.Context(), "http://x/a", "http://x/p", "http://x/b"); err != nil {
		t.Fatal(err)
	}
	want := "INSERT DATA { GRAPH <http://x/kb> { <http://x/a> <http://x/p> <http://x/b> . } }"
	if u := one(t, *got).update; u != want {
		t.Errorf("sent %q,\n want %q", u, want)
	}
}

func TestMatchQueries(t *testing.T) {
	for _, c := range []struct {
		name    string
		s, p, o string
		want    string
	}{
		{"everything", "", "", "", "SELECT ?s ?p ?o WHERE { ?s ?p ?o . }"},
		{"by subject", "http://x/ada", "", "", "SELECT ?s ?p ?o WHERE { <http://x/ada> ?p ?o . }"},
		{"by predicate", "", "http://x/wrote", "", "SELECT ?s ?p ?o WHERE { ?s <http://x/wrote> ?o . }"},
		{"by an iri object", "", "", "http://x/notes", "SELECT ?s ?p ?o WHERE { ?s ?p <http://x/notes> . }"},
		{"by a literal object", "", "", "Ada", `SELECT ?s ?p ?o WHERE { ?s ?p "Ada" . }`},
		{"fully bound", "http://x/ada", "http://x/wrote", "http://x/notes", "SELECT ?s ?p ?o WHERE { <http://x/ada> <http://x/wrote> <http://x/notes> . }"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, got := serve(t, "")
			if _, err := s.Match(t.Context(), c.s, c.p, c.o); err != nil {
				t.Fatal(err)
			}
			r := one(t, *got)
			if q := strings.Join(strings.Fields(r.query), " "); q != c.want {
				t.Errorf("sent %q,\n want %q", q, c.want)
			}
			if r.accept != "application/sparql-results+json" {
				t.Errorf("asked for %q, want the results JSON", r.accept)
			}
		})
	}
}

func TestMatchReadsResults(t *testing.T) {
	s, _ := serve(t, `{
		"head": {"vars": ["s","p","o"]},
		"results": {"bindings": [
			{"s":{"type":"uri","value":"http://x/ada"},
			 "p":{"type":"uri","value":"http://x/name"},
			 "o":{"type":"literal","value":"Ada Lovelace"}},
			{"s":{"type":"uri","value":"http://x/ada"},
			 "p":{"type":"uri","value":"http://x/wrote"},
			 "o":{"type":"uri","value":"http://x/notes"}}
		]}
	}`)
	got, err := s.Match(t.Context(), "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	want := [][3]string{
		{"http://x/ada", "http://x/name", "Ada Lovelace"},
		{"http://x/ada", "http://x/wrote", "http://x/notes"},
	}
	if len(got) != len(want) {
		t.Fatalf("read %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d is %v, want %v", i, got[i], want[i])
		}
	}
}

func TestMatchFillsInWhatItBound(t *testing.T) {
	// A bound term is not a variable, so the server never sends it back.
	// The answer still has to carry it.
	s, _ := serve(t, `{
		"head": {"vars": ["p","o"]},
		"results": {"bindings": [
			{"p":{"type":"uri","value":"http://x/name"},
			 "o":{"type":"literal","value":"Ada"}}
		]}
	}`)
	got, err := s.Match(t.Context(), "http://x/ada", "", "")
	if err != nil {
		t.Fatal(err)
	}
	want := [3]string{"http://x/ada", "http://x/name", "Ada"}
	if len(got) != 1 || got[0] != want {
		t.Errorf("read %v, want %v", got, want)
	}
}

func TestRetract(t *testing.T) {
	for _, c := range []struct {
		name    string
		s, p, o string
		want    string
	}{
		{"one triple", "http://x/a", "http://x/p", "http://x/b", "DELETE WHERE { <http://x/a> <http://x/p> <http://x/b> . }"},
		{"by subject", "http://x/a", "", "", "DELETE WHERE { <http://x/a> ?p ?o . }"},
		{"everything", "", "", "", "DELETE WHERE { ?s ?p ?o . }"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, got := serve(t, "")
			if err := s.Retract(t.Context(), c.s, c.p, c.o); err != nil {
				t.Fatal(err)
			}
			if u := strings.Join(strings.Fields(one(t, *got).update), " "); u != c.want {
				t.Errorf("sent %q,\n want %q", u, c.want)
			}
		})
	}
}

func TestNode(t *testing.T) {
	s, got := serve(t, "")
	s.Base = "http://x/"
	err := s.Node(t.Context(), "ada", map[string]any{"name": "Ada", "born": 1815})
	if err != nil {
		t.Fatal(err)
	}
	// Properties are sorted, so the same node is always the same update.
	want := `INSERT DATA { <http://x/ada> <http://x/born> "1815" . <http://x/ada> <http://x/name> "Ada" . }`
	if u := one(t, *got).update; u != want {
		t.Errorf("sent %q,\n want %q", u, want)
	}

	if err := s.Node(t.Context(), "ada", nil); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 1 {
		t.Error("a node with no properties made a request: there is nothing to say")
	}
	if err := s.Node(t.Context(), "", map[string]any{"name": "Ada"}); !errors.Is(err, store.ErrID) {
		t.Errorf("gave %v, want store.ErrID", err)
	}
}

func TestEdge(t *testing.T) {
	s, got := serve(t, "")
	s.Base = "http://x/"
	if err := s.Edge(t.Context(), "ada", "charles", "knows"); err != nil {
		t.Fatal(err)
	}
	// Both ends of an edge name things, so both are IRIs. That is what
	// separates an edge from a property.
	want := "INSERT DATA { <http://x/ada> <http://x/knows> <http://x/charles> . }"
	if u := one(t, *got).update; u != want {
		t.Errorf("sent %q,\n want %q", u, want)
	}
	if err := s.Edge(t.Context(), "ada", "", "knows"); !errors.Is(err, store.ErrID) {
		t.Errorf("gave %v, want store.ErrID", err)
	}
}

func TestOut(t *testing.T) {
	s, got := serve(t, `{
		"head": {"vars": ["o"]},
		"results": {"bindings": [
			{"o":{"type":"uri","value":"http://x/charles"}},
			{"o":{"type":"uri","value":"http://x/notes"}}
		]}
	}`)
	out, err := s.Out(t.Context(), "http://x/ada")
	if err != nil {
		t.Fatal(err)
	}
	want := "SELECT DISTINCT ?o WHERE { <http://x/ada> ?p ?o . FILTER(isIRI(?o)) }"
	if q := strings.Join(strings.Fields(one(t, *got).query), " "); q != want {
		t.Errorf("sent %q,\n want %q", q, want)
	}
	if len(out) != 2 || out[0] != "http://x/charles" || out[1] != "http://x/notes" {
		t.Errorf("read %v", out)
	}
}

func TestUpdateGoesToItsOwnEndpoint(t *testing.T) {
	s, got := serve(t, "")
	s.Update = strings.TrimSuffix(s.Query, "/query") + "/update"
	if err := s.Assert(t.Context(), "http://x/a", "http://x/p", "http://x/b"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Match(t.Context(), "", "", ""); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 2 {
		t.Fatalf("made %d requests, want two", len(*got))
	}
	if (*got)[0].path != "/ds/update" {
		t.Errorf("the update went to %q", (*got)[0].path)
	}
	if (*got)[1].path != "/ds/query" {
		t.Errorf("the query went to %q", (*got)[1].path)
	}
}

func TestUpdateGoesToTheQueryEndpointWhenThereIsOnlyOne(t *testing.T) {
	s, got := serve(t, "")
	if err := s.Assert(t.Context(), "http://x/a", "http://x/p", "http://x/b"); err != nil {
		t.Fatal(err)
	}
	if one(t, *got).path != "/ds/query" {
		t.Errorf("the update went to %q", (*got)[0].path)
	}
}

func TestSendsCredentials(t *testing.T) {
	s, got := serve(t, "")
	s.User, s.Pass = "ada", "secret"
	if _, err := s.Match(t.Context(), "", "", ""); err != nil {
		t.Fatal(err)
	}
	if u := one(t, *got).user; u != "ada" {
		t.Errorf("sent user %q", u)
	}
}

// A term that escapes its brackets or its quotes is a second statement, and
// a store that lets one through will run whatever it is handed.
func TestRefusesTermsThatWouldEscape(t *testing.T) {
	for _, c := range []struct {
		name string
		term string
	}{
		{"an angle bracket ends the iri", "http://x/a> <http://x/p> <http://x/b> . } ; CLEAR ALL ; INSERT DATA { <http://x/z"},
		{"whitespace", "http://x/a b"},
		{"a brace", "http://x/a{b}"},
		{"a backslash", `http://x/a\b`},
		{"a quote", `http://x/a"b`},
		{"a pipe", "http://x/a|b"},
		{"a caret", "http://x/a^b"},
		{"a backtick", "http://x/a`b"},
		{"a newline", "http://x/a\nb"},
		{"no scheme at all", "ada"},
		{"a scheme nobody asked for", "javascript:alert(1)"},
		{"a wrapped iri is checked inside its brackets", "<http://x/a> . } ; DROP ALL ; INSERT DATA { <http://x/z>"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, got := serve(t, "")
			for _, where := range []struct {
				name string
				call func() error
			}{
				{"subject", func() error { return s.Assert(t.Context(), c.term, "http://x/p", "http://x/o") }},
				{"predicate", func() error { return s.Assert(t.Context(), "http://x/s", c.term, "http://x/o") }},
				{"match subject", func() error { _, err := s.Match(t.Context(), c.term, "", ""); return err }},
				{"retract subject", func() error { return s.Retract(t.Context(), c.term, "", "") }},
				{"edge", func() error { return s.Edge(t.Context(), c.term, "http://x/b", "http://x/p") }},
			} {
				if err := where.call(); err == nil {
					t.Errorf("%s: %q was accepted", where.name, c.term)
				}
			}
			if len(*got) != 0 {
				t.Errorf("a refused term still reached the server: %+v", *got)
			}
		})
	}
}

// An empty term is a wildcard where a pattern is being matched and a
// missing term where a fact is being written. It cannot be both, and which
// it is depends on the question, not on the term.
func TestEmptyTerm(t *testing.T) {
	s, got := serve(t, "")
	ctx := t.Context()
	for _, c := range []struct {
		name string
		call func() error
	}{
		{"assert", func() error { return s.Assert(ctx, "", "http://x/p", "http://x/o") }},
		{"assert predicate", func() error { return s.Assert(ctx, "http://x/s", "", "http://x/o") }},
		{"assert object", func() error { return s.Assert(ctx, "http://x/s", "http://x/p", "") }},
		{"edge", func() error { return s.Edge(ctx, "", "http://x/b", "http://x/p") }},
	} {
		if err := c.call(); err == nil {
			t.Errorf("%s: a missing term was written", c.name)
		}
	}
	if len(*got) != 0 {
		t.Errorf("a missing term reached the server: %+v", *got)
	}

	if _, err := s.Match(ctx, "", "", ""); err != nil {
		t.Errorf("matching on wildcards: %v", err)
	}
	if err := s.Retract(ctx, "", "", ""); err != nil {
		t.Errorf("retracting on wildcards: %v", err)
	}
	if len(*got) != 2 {
		t.Errorf("made %d requests for two wildcard patterns", len(*got))
	}
}

func TestEscapesLiterals(t *testing.T) {
	for _, c := range []struct {
		name string
		in   string
		want string
	}{
		{"plain", "Ada", `"Ada"`},
		{"a quote", `say "hi"`, `"say \"hi\""`},
		{"a backslash", `a\b`, `"a\\b"`},
		{"a newline", "a\nb", `"a\nb"`},
		{"a carriage return", "a\rb", `"a\rb"`},
		{"a tab", "a\tb", `"a\tb"`},
		{"an escape attempt", `x" . } ; CLEAR ALL ; INSERT DATA { <a> <b> "y`, `"x\" . } ; CLEAR ALL ; INSERT DATA { <a> <b> \"y"`},
		{"a backslash before a quote", `a\"`, `"a\\\""`},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := Lit(c.in); got != c.want {
				t.Errorf("Lit(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestLiteralObjectsAreEscapedInTheUpdate(t *testing.T) {
	s, got := serve(t, "")
	obj := `Paris" . } ; CLEAR ALL ; INSERT DATA { <http://x/a> <http://x/b> "c`
	if err := s.Assert(t.Context(), "http://x/s", "http://x/p", obj); err != nil {
		t.Fatal(err)
	}
	u := one(t, *got).update
	if strings.Contains(u, "CLEAR ALL ;") && !strings.Contains(u, `\"`) {
		t.Errorf("an object escaped its quotes: %q", u)
	}
	if !strings.HasSuffix(u, `\"c" . }`) {
		t.Errorf("sent %q, want the whole object inside one literal", u)
	}
}

func TestIRI(t *testing.T) {
	for _, c := range []struct {
		in   string
		want string
		bad  bool
	}{
		{in: "http://x/a", want: "<http://x/a>"},
		{in: "https://x/a", want: "<https://x/a>"},
		{in: "HTTP://x/a", want: "<HTTP://x/a>"},
		{in: "urn:uuid:1234", want: "<urn:uuid:1234>"},
		{in: "ftp://x/a", bad: true},
		{in: "x/a", bad: true},
		{in: "", bad: true},
		{in: "http://x/ a", bad: true},
	} {
		t.Run(c.in, func(t *testing.T) {
			got, err := IRI(c.in)
			if c.bad {
				if err == nil {
					t.Fatalf("IRI(%q) = %q, want a complaint", c.in, got)
				}
				if !errors.Is(err, ErrIRI) {
					t.Errorf("gave %v, want ErrIRI", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("IRI(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestReportsWhatTheServerSaid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("Lexical error at line 1"))
	}))
	defer srv.Close()

	s, err := New(srv.URL + "/ds/query")
	if err != nil {
		t.Fatal(err)
	}
	s.HTTP = srv.Client()
	_, err = s.Match(t.Context(), "", "", "")
	if err == nil {
		t.Fatal("a 400 was taken for an answer")
	}
	for _, want := range []string{"400", "Lexical error at line 1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q does not say %q", err, want)
		}
	}
}

func TestReportsUnreadableAnswers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>not results</html>"))
	}))
	defer srv.Close()

	s, _ := New(srv.URL + "/ds/query")
	s.HTTP = srv.Client()
	if _, err := s.Match(t.Context(), "", "", ""); err == nil {
		t.Fatal("HTML was taken for a result set")
	}
}

func TestHonoursContext(t *testing.T) {
	s, got := serve(t, "")
	ctx, stop := context.WithCancel(t.Context())
	stop()
	if err := s.Assert(ctx, "http://x/a", "http://x/p", "http://x/b"); !errors.Is(err, context.Canceled) {
		t.Errorf("gave %v, want context.Canceled", err)
	}
	if _, err := s.Match(ctx, "", "", ""); !errors.Is(err, context.Canceled) {
		t.Errorf("gave %v, want context.Canceled", err)
	}
	if len(*got) != 0 {
		t.Errorf("asked the server after the caller gave up: %+v", *got)
	}
}

func TestRegistered(t *testing.T) {
	// Every server that speaks the protocol is the same driver.
	for _, name := range []string{"sparql", "blazegraph", "fuseki", "graphdb", "jena", "rdf4j"} {
		t.Run(name, func(t *testing.T) {
			if !store.Graphs.Has(name) || !store.Triples.Has(name) {
				t.Fatal("importing the package did not register the driver")
			}
			g, err := store.Graphs.Open(name, "http://localhost:3030/ds/query")
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := g.(*Store); !ok {
				t.Errorf("the driver opened a %T", g)
			}
			if _, err := store.Triples.Open(name, "nonsense"); err == nil {
				t.Error("the driver opened a source name that is not a URL")
			}
		})
	}
}

func TestIn(t *testing.T) {
	s, got := serve(t, `{
		"head": {"vars": ["s"]},
		"results": {"bindings": [{"s":{"type":"uri","value":"http://x/ada"}}]}
	}`)
	in, err := s.In(t.Context(), "http://x/notes")
	if err != nil {
		t.Fatal(err)
	}
	want := "SELECT DISTINCT ?s WHERE { ?s ?p <http://x/notes> . }"
	if q := strings.Join(strings.Fields(one(t, *got).query), " "); q != want {
		t.Errorf("sent %q,\n want %q", q, want)
	}
	if len(in) != 1 || in[0] != "http://x/ada" {
		t.Errorf("read %v", in)
	}
}

func TestCut(t *testing.T) {
	s, got := serve(t, "")
	if err := s.Cut(t.Context(), "http://x/ada"); err != nil {
		t.Fatal(err)
	}
	// Both directions, because a node is everything said about it and
	// everything said of it.
	want := "DELETE WHERE { <http://x/ada> ?p ?o . } ; DELETE WHERE { ?s ?p <http://x/ada> . }"
	if u := strings.Join(strings.Fields(one(t, *got).update), " "); u != want {
		t.Errorf("sent %q,\n want %q", u, want)
	}
	if err := s.Cut(t.Context(), "ada"); !errors.Is(err, ErrIRI) {
		t.Errorf("gave %v, want ErrIRI", err)
	}
}

func TestUnlink(t *testing.T) {
	for _, c := range []struct {
		name            string
		from, to, label string
		want            string
	}{
		{"one edge", "http://x/a", "http://x/b", "http://x/p", "DELETE WHERE { <http://x/a> <http://x/p> <http://x/b> . }"},
		{"everything leaving a", "http://x/a", "", "", "DELETE WHERE { <http://x/a> ?p ?o . FILTER(isIRI(?o)) }"},
		{"every edge under a label", "", "", "http://x/p", "DELETE WHERE { ?s <http://x/p> ?o . FILTER(isIRI(?o)) }"},
		{"everything into b", "", "http://x/b", "", "DELETE WHERE { ?s ?p <http://x/b> . }"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, got := serve(t, "")
			if err := s.Unlink(t.Context(), c.from, c.to, c.label); err != nil {
				t.Fatal(err)
			}
			if u := strings.Join(strings.Fields(one(t, *got).update), " "); u != c.want {
				t.Errorf("sent %q,\n want %q", u, c.want)
			}
		})
	}
}

// A store that removes only what it was asked to remove is the whole point
// of the filter: an unbound object matches literals as well as nodes, and a
// property is not an edge.
func TestUnlinkLeavesPropertiesAlone(t *testing.T) {
	s, got := serve(t, "")
	if err := s.Unlink(t.Context(), "http://x/a", "", ""); err != nil {
		t.Fatal(err)
	}
	if u := one(t, *got).update; !strings.Contains(u, "isIRI(?o)") {
		t.Errorf("sent %q, want it to spare the literals", u)
	}
}
