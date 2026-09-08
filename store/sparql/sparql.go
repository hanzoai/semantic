// Package sparql keeps a graph on a SPARQL 1.1 endpoint over HTTP.
//
// One driver reaches every server that speaks the protocol — Fuseki,
// Blazegraph, RDF4J, GraphDB — because the protocol is the interface. The
// Python it is ported from has a file per server; the differences between
// them are the URLs of the query and update endpoints, which are two fields
// here rather than four modules.
//
//	import _ "github.com/hanzoai/semantic/store/sparql"
//
//	t, err := store.Triples.Open("sparql", "http://localhost:3030/ds/query")
//
// It answers both store.Graph and store.Triple, because RDF is one store
// answering both questions.
package sparql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/hanzoai/semantic/store"
)

func init() {
	// Every server that speaks the protocol is reached the same way. The
	// names are here so a configuration written against the Python, which
	// named the product, still opens.
	for _, n := range []string{"sparql", "blazegraph", "fuseki", "graphdb", "jena", "rdf4j"} {
		store.Graphs.Add(n, func(dsn string) (store.Graph, error) { return New(dsn) })
		store.Triples.Add(n, func(dsn string) (store.Triple, error) { return New(dsn) })
	}
}

// Store is one SPARQL endpoint.
type Store struct {
	// Query is the URL that answers SELECT, ASK and CONSTRUCT.
	Query string
	// Update is the URL that accepts INSERT and DELETE. Empty sends updates
	// to Query, which is where a server that has one endpoint wants them.
	Update string
	// Graph is the IRI of the named graph read and written. Empty uses the
	// default graph.
	Graph string
	// User and Pass are sent as basic authentication when User is set.
	User, Pass string
	// Base turns a bare name into an IRI: a subject or predicate that is not
	// already an IRI is read as Base + name. Empty requires every subject
	// and predicate to be an IRI already.
	Base string
	// HTTP is the client used for every request. Nil uses
	// http.DefaultClient.
	HTTP *http.Client
}

// Store answers what is connected and what was asserted.
var (
	_ store.Graph  = (*Store)(nil)
	_ store.Triple = (*Store)(nil)
)

// New reads a store from the URL of its query endpoint. A server that keeps
// updates at a second URL — Fuseki's /update beside its /query — gets it by
// setting Update.
func New(dsn string) (*Store, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("sparql: %q: %w", dsn, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("sparql: %q: want an http or https endpoint", dsn)
	}
	s := &Store{}
	if u.User != nil {
		s.User = u.User.Username()
		s.Pass, _ = u.User.Password()
		u.User = nil
	}
	s.Query = u.String()
	return s, nil
}

// Answer is what a SELECT returns: the variables it projected, and one
// binding per solution from variable to value. A value is the IRI or the
// literal's lexical form, with nothing around it, which is what a caller
// wants to compare or store.
type Answer struct {
	Vars []string
	Rows []map[string]string
}

// Assert records one triple. Subject and predicate must name things, so
// they are IRIs; an object that is not an IRI is stored as a literal.
func (s *Store) Assert(ctx context.Context, sub, pred, obj string) error {
	return s.Add(ctx, [3]string{sub, pred, obj})
}

// Add records many triples in one update, which is the reason to have it: a
// round trip per triple is the wrong price for a graph.
func (s *Store) Add(ctx context.Context, facts ...[3]string) error {
	if len(facts) == 0 {
		return nil
	}
	var b strings.Builder
	for _, f := range facts {
		sub, err := s.iri(f[0])
		if err != nil {
			return err
		}
		pred, err := s.iri(f[1])
		if err != nil {
			return err
		}
		obj, err := s.term(f[2])
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s %s %s . ", sub, pred, obj)
	}
	return s.update(ctx, "INSERT DATA { "+s.wrap(b.String())+"}")
}

// Match returns the triples that fit a pattern. An empty subject, predicate
// or object matches any.
func (s *Store) Match(ctx context.Context, sub, pred, obj string) ([][3]string, error) {
	t, err := s.triple(sub, pred, obj)
	if err != nil {
		return nil, err
	}
	a, err := s.Ask(ctx, "SELECT ?s ?p ?o WHERE "+s.where(t))
	if err != nil {
		return nil, err
	}
	out := make([][3]string, 0, len(a.Rows))
	for _, r := range a.Rows {
		out = append(out, [3]string{pick(r, "s", sub), pick(r, "p", pred), pick(r, "o", obj)})
	}
	return out, nil
}

// Retract removes the triples that fit a pattern, on the same wildcards as
// Match. Retract(ctx, "", "", "") empties the graph.
func (s *Store) Retract(ctx context.Context, sub, pred, obj string) error {
	t, err := s.triple(sub, pred, obj)
	if err != nil {
		return err
	}
	return s.update(ctx, "DELETE WHERE "+s.where(t))
}

// Node writes a node and its properties. A property becomes a triple whose
// predicate is the property's name as an IRI and whose object is its value,
// so a node is not a second kind of thing in an RDF store — it is the
// triples about it.
func (s *Store) Node(ctx context.Context, id string, props map[string]any) error {
	if id == "" {
		return store.ErrID
	}
	if len(props) == 0 {
		return nil
	}
	facts := make([][3]string, 0, len(props))
	for k, v := range props {
		facts = append(facts, [3]string{id, k, fmt.Sprint(v)})
	}
	sortFacts(facts)
	return s.Add(ctx, facts...)
}

// Edge links two nodes under a label. Both ends name things, so both are
// IRIs, which is what separates an edge from a property.
func (s *Store) Edge(ctx context.Context, from, to, label string) error {
	if from == "" || to == "" {
		return store.ErrID
	}
	sub, err := s.iri(from)
	if err != nil {
		return err
	}
	pred, err := s.iri(label)
	if err != nil {
		return err
	}
	obj, err := s.iri(to)
	if err != nil {
		return err
	}
	return s.update(ctx, fmt.Sprintf("INSERT DATA { %s}", s.wrap(sub+" "+pred+" "+obj+" . ")))
}

// Out lists the things a node points at, an IRI object at a time. Literal
// objects are properties, not edges, so they are left out.
func (s *Store) Out(ctx context.Context, id string) ([]string, error) {
	sub, err := s.iri(id)
	if err != nil {
		return nil, err
	}
	q := fmt.Sprintf("SELECT DISTINCT ?o WHERE %s", s.where(sub+" ?p ?o . FILTER(isIRI(?o)) "))
	a, err := s.Ask(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(a.Rows))
	for _, r := range a.Rows {
		out = append(out, r["o"])
	}
	return out, nil
}

// In lists the things that point at a node, the mirror of Out.
func (s *Store) In(ctx context.Context, id string) ([]string, error) {
	obj, err := s.iri(id)
	if err != nil {
		return nil, err
	}
	q := fmt.Sprintf("SELECT DISTINCT ?s WHERE %s", s.where("?s ?p "+obj+" . "))
	a, err := s.Ask(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(a.Rows))
	for _, r := range a.Rows {
		out = append(out, r["s"])
	}
	return out, nil
}

// Cut removes a node: everything said about it and everything said of it.
// In RDF there is nothing else a node is.
func (s *Store) Cut(ctx context.Context, id string) error {
	at, err := s.iri(id)
	if err != nil {
		return err
	}
	q := fmt.Sprintf("DELETE WHERE %s ; DELETE WHERE %s",
		s.where(at+" ?p ?o . "), s.where("?s ?p "+at+" . "))
	return s.update(ctx, q)
}

// Unlink removes the edges that match a pattern. An empty from, to or label
// matches any. Only edges go: a property is a literal object, and this
// leaves those alone.
func (s *Store) Unlink(ctx context.Context, from, to, label string) error {
	t, err := s.triple(from, label, to)
	if err != nil {
		return err
	}
	if to == "" {
		// The object is a variable, so it would match a property as
		// readily as an edge. Only a node is at the end of an edge.
		t += "FILTER(isIRI(?o)) "
	}
	return s.update(ctx, "DELETE WHERE "+s.where(t))
}

// Ask runs a SELECT and returns its bindings. It is the way out for a
// question this package does not have a method for; the query is sent as
// written, so a caller that builds one from untrusted parts must escape
// them with IRI and Lit.
func (s *Store) Ask(ctx context.Context, query string) (Answer, error) {
	body := url.Values{"query": {query}}.Encode()
	req, err := s.request(ctx, s.Query, body)
	if err != nil {
		return Answer{}, err
	}
	req.Header.Set("Accept", "application/sparql-results+json")
	b, err := s.send(req, query)
	if err != nil {
		return Answer{}, err
	}
	var raw struct {
		Head struct {
			Vars []string `json:"vars"`
		} `json:"head"`
		Results struct {
			Bindings []map[string]struct {
				Value string `json:"value"`
			} `json:"bindings"`
		} `json:"results"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return Answer{}, fmt.Errorf("sparql: decode results: %w", err)
	}
	a := Answer{Vars: raw.Head.Vars}
	for _, row := range raw.Results.Bindings {
		r := make(map[string]string, len(row))
		for k, v := range row {
			r[k] = v.Value
		}
		a.Rows = append(a.Rows, r)
	}
	return a, nil
}

// update sends one SPARQL update.
func (s *Store) update(ctx context.Context, q string) error {
	at := s.Update
	if at == "" {
		at = s.Query
	}
	req, err := s.request(ctx, at, url.Values{"update": {q}}.Encode())
	if err != nil {
		return err
	}
	_, err = s.send(req, q)
	return err
}

func (s *Store) request(ctx context.Context, at, body string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, at, strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("sparql: %s: %w", at, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if s.User != "" {
		req.SetBasicAuth(s.User, s.Pass)
	}
	return req, nil
}

func (s *Store) send(req *http.Request, q string) ([]byte, error) {
	client := s.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sparql: %s: %w", req.URL, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("sparql: read %s: %w", req.URL, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("sparql: %s: %s: %s", req.URL, resp.Status, strings.TrimSpace(string(b)))
	}
	return b, nil
}

// triple builds one triple pattern, binding the terms that were given and
// leaving variables for the rest. Callers put it inside the query form they
// want, which is the only difference between reading and deleting.
func (s *Store) triple(sub, pred, obj string) (string, error) {
	parts := [3]string{"?s", "?p", "?o"}
	if sub != "" {
		t, err := s.iri(sub)
		if err != nil {
			return "", err
		}
		parts[0] = t
	}
	if pred != "" {
		t, err := s.iri(pred)
		if err != nil {
			return "", err
		}
		parts[1] = t
	}
	if obj != "" {
		t, err := s.term(obj)
		if err != nil {
			return "", err
		}
		parts[2] = t
	}
	return strings.Join(parts[:], " ") + " . ", nil
}

// where wraps a triple pattern in braces, inside a GRAPH clause when the
// store names one.
func (s *Store) where(body string) string { return "{ " + s.wrap(body) + "}" }

// wrap puts a body inside the store's named graph, if it has one.
func (s *Store) wrap(body string) string {
	if s.Graph == "" {
		return body
	}
	return "GRAPH <" + s.Graph + "> { " + body + "} "
}

// iri renders a term that must name something. A bare name is read against
// Base; without a Base there is nothing to read it against, so it is
// refused rather than guessed at.
func (s *Store) iri(v string) (string, error) {
	if v == "" {
		return "", store.ErrTerm
	}
	if strings.HasPrefix(v, "<") && strings.HasSuffix(v, ">") {
		return IRI(v[1 : len(v)-1])
	}
	if absolute(v) {
		return IRI(v)
	}
	if s.Base == "" {
		return "", fmt.Errorf("%w: %q is not an IRI and the store has no Base", ErrIRI, v)
	}
	return IRI(s.Base + v)
}

// term renders an object, which may name something or say something: an IRI
// when it reads as one, a literal otherwise. It is the one place the two
// are told apart, and it is why "Paris" is stored as a name and not as a
// location nobody can resolve.
func (s *Store) term(v string) (string, error) {
	if v == "" {
		return "", store.ErrTerm
	}
	if strings.HasPrefix(v, "<") && strings.HasSuffix(v, ">") {
		return IRI(v[1 : len(v)-1])
	}
	if absolute(v) {
		return IRI(v)
	}
	return Lit(v), nil
}

// ErrIRI means a term could not be rendered as an IRI.
var ErrIRI = errors.New("sparql: not an IRI")

// bad is the character set an IRI may not contain: whitespace, and the
// characters that would end the IRI or start something else inside a query.
// Rejecting them is what stops a term from becoming a second statement.
var bad = regexp.MustCompile("[\\s<>\"{}|\\\\^`]")

// scheme matches an absolute IRI in one of the schemes an RDF store is
// expected to hold.
var scheme = regexp.MustCompile(`^(?i:https?|urn):`)

func absolute(v string) bool { return scheme.MatchString(v) && !bad.MatchString(v) }

// IRI renders a string as an angle-bracketed IRI, refusing anything that is
// not an absolute http, https or urn IRI, or that carries a character which
// would let it escape the brackets.
func IRI(v string) (string, error) {
	if v == "" {
		return "", fmt.Errorf("%w: empty", ErrIRI)
	}
	if bad.MatchString(v) {
		return "", fmt.Errorf("%w: %q holds a character an IRI may not", ErrIRI, v)
	}
	if !scheme.MatchString(v) {
		return "", fmt.Errorf("%w: %q has no http, https or urn scheme", ErrIRI, v)
	}
	return "<" + v + ">", nil
}

// Lit renders a string as a quoted SPARQL literal, escaping what would
// otherwise end the quotation.
func Lit(v string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"\n", `\n`,
		"\r", `\r`,
		"\t", `\t`,
	)
	return `"` + r.Replace(v) + `"`
}

// pick reads a variable from a solution, falling back to the value that was
// bound in the query and so never came back in the answer.
func pick(row map[string]string, name, bound string) string {
	if v, ok := row[name]; ok {
		return v
	}
	return bound
}

// sortFacts orders triples so an update built from a map is the same query
// every time, which is what makes it testable.
func sortFacts(fs [][3]string) {
	slices.SortFunc(fs, func(a, b [3]string) int {
		for i := range a {
			if c := strings.Compare(a[i], b[i]); c != 0 {
				return c
			}
		}
		return 0
	})
}
