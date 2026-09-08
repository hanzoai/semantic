// Package qdrant keeps vectors in Qdrant, over its JSON HTTP API.
//
// It exists to prove that store.Vector is a real seam and not a shape only
// an in-memory map can fill: nothing here is imported by the rest of
// semantica, and a program reaches it the way a program reaches a database
// driver.
//
//	import _ "github.com/hanzoai/semantic/store/qdrant"
//
//	v, err := store.Vectors.Open("qdrant", "http://localhost:6333/docs")
//
// The API is spoken directly rather than through a client library, so the
// module stays on the standard library and the requests are the ones a
// reader can check against Qdrant's own documentation.
package qdrant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/hanzoai/semantic/store"
)

func init() {
	store.Vectors.Add("qdrant", func(dsn string) (store.Vector, error) { return New(dsn) })
}

// Store is one Qdrant collection.
type Store struct {
	// URL is the server, without a path: http://localhost:6333.
	URL string
	// Collection is the collection points are written to and read from.
	Collection string
	// Key is sent as the api-key header. Empty sends none.
	Key string
	// Metric is how Create configures the collection to measure nearness.
	// Empty means cosine.
	Metric store.Metric
	// HTTP is the client used for every request. Nil uses
	// http.DefaultClient.
	HTTP *http.Client
}

// Store answers "what is like this".
var _ store.Vector = (*Store)(nil)

// New reads a store from a URL whose last path segment names the
// collection, so one string carries both the server and what to ask it:
//
//	http://localhost:6333/docs
//	https://key@qdrant.internal:6333/docs
//
// A user in the URL is sent as the api-key header.
func New(dsn string) (*Store, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("qdrant: %q: %w", dsn, err)
	}
	name := strings.Trim(u.Path, "/")
	if u.Host == "" || name == "" || strings.Contains(name, "/") {
		return nil, fmt.Errorf("qdrant: %q: want scheme://host/collection", dsn)
	}
	s := &Store{Collection: name}
	if u.User != nil {
		s.Key = u.User.Username()
		u.User = nil
	}
	u.Path, u.RawQuery, u.Fragment = "", "", ""
	s.URL = strings.TrimSuffix(u.String(), "/")
	return s, nil
}

// Create makes the collection, sized for vectors of dim components and
// measuring nearness the way the store does. It is not needed to use a
// collection that already exists.
func (s *Store) Create(ctx context.Context, dim int) error {
	body := map[string]any{
		"vectors": map[string]any{"size": dim, "distance": distance(s.Metric)},
	}
	return s.call(ctx, http.MethodPut, "/collections/"+s.Collection, body, nil)
}

// Put writes one vector under an id, replacing whatever was there.
func (s *Store) Put(ctx context.Context, id string, v []float32, meta map[string]any) error {
	return s.Add(ctx, store.Item{ID: id, Vec: v, Meta: meta})
}

// Add writes many vectors in one request, which is the reason to have it: a
// point per round trip is the wrong price for a batch.
func (s *Store) Add(ctx context.Context, items ...store.Item) error {
	if len(items) == 0 {
		return nil
	}
	points := make([]map[string]any, 0, len(items))
	for _, it := range items {
		if it.ID == "" {
			return store.ErrID
		}
		p := map[string]any{"id": it.ID, "vector": it.Vec}
		if it.Meta != nil {
			p["payload"] = it.Meta
		}
		points = append(points, p)
	}
	path := "/collections/" + s.Collection + "/points?wait=true"
	return s.call(ctx, http.MethodPut, path, map[string]any{"points": points}, nil)
}

// Near returns the k nearest vectors to v, nearest first.
func (s *Store) Near(ctx context.Context, v []float32, k int) ([]store.Match, error) {
	return s.Search(ctx, store.Query{Vec: v, K: k})
}

// Search returns the k nearest vectors that also match the query's filter,
// which Qdrant applies while it searches rather than after.
//
// Scores are Qdrant's own, on the collection's metric: a cosine collection
// answers with cosine similarity, exactly as store.Mem does, so the two
// stores rank alike and a score means the same thing whichever served it.
//
// A K of zero or less asks for ten. Unlike Mem, the server has no way to be
// asked for everything, and pretending otherwise would answer a different
// question than the one put to it.
//
// A collection measures nearness one way, fixed when it was created, so a
// query that asks for a different metric is refused rather than answered on
// the wrong one.
func (s *Store) Search(ctx context.Context, q store.Query) ([]store.Match, error) {
	if q.Metric != "" && distance(q.Metric) != distance(s.Metric) {
		return nil, fmt.Errorf("qdrant: %s measures %s, not %s",
			s.Collection, distance(s.Metric), distance(q.Metric))
	}
	k := q.K
	if k <= 0 {
		k = 10
	}
	body := map[string]any{"vector": q.Vec, "limit": k, "with_payload": true}
	f, err := filter(q.Filter)
	if err != nil {
		return nil, err
	}
	if f != nil {
		body["filter"] = f
	}
	var out struct {
		Result []struct {
			ID      any            `json:"id"`
			Score   float64        `json:"score"`
			Payload map[string]any `json:"payload"`
		} `json:"result"`
	}
	path := "/collections/" + s.Collection + "/points/search"
	if err := s.call(ctx, http.MethodPost, path, body, &out); err != nil {
		return nil, err
	}
	ms := make([]store.Match, 0, len(out.Result))
	for _, r := range out.Result {
		ms = append(ms, store.Match{ID: id(r.ID), Score: r.Score, Meta: r.Payload})
	}
	return ms, nil
}

// Get returns the vectors and payloads stored under a set of ids, in
// whatever order Qdrant answers.
func (s *Store) Get(ctx context.Context, ids ...string) ([]store.Item, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	body := map[string]any{"ids": ids, "with_payload": true, "with_vector": true}
	var out struct {
		Result []struct {
			ID      any            `json:"id"`
			Vector  []float32      `json:"vector"`
			Payload map[string]any `json:"payload"`
		} `json:"result"`
	}
	path := "/collections/" + s.Collection + "/points"
	if err := s.call(ctx, http.MethodPost, path, body, &out); err != nil {
		return nil, err
	}
	items := make([]store.Item, 0, len(out.Result))
	for _, r := range out.Result {
		items = append(items, store.Item{ID: id(r.ID), Vec: r.Vector, Meta: r.Payload})
	}
	return items, nil
}

// Drop removes points by id.
func (s *Store) Drop(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	path := "/collections/" + s.Collection + "/points/delete?wait=true"
	return s.call(ctx, http.MethodPost, path, map[string]any{"points": ids}, nil)
}

// Count reports how many points the collection holds, counted exactly
// rather than estimated.
func (s *Store) Count(ctx context.Context) (int, error) {
	var out struct {
		Result struct {
			Count int `json:"count"`
		} `json:"result"`
	}
	path := "/collections/" + s.Collection + "/points/count"
	if err := s.call(ctx, http.MethodPost, path, map[string]any{"exact": true}, &out); err != nil {
		return 0, err
	}
	return out.Result.Count, nil
}

// call sends one request and decodes the answer into out, which may be nil
// when the answer says nothing but whether it worked.
func (s *Store) call(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("qdrant: encode %s: %w", path, err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.URL+path, r)
	if err != nil {
		return fmt.Errorf("qdrant: %s %s: %w", method, path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.Key != "" {
		req.Header.Set("api-key", s.Key)
	}
	client := s.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("qdrant: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("qdrant: %s %s: %s: %s", method, path, resp.Status, bytes.TrimSpace(b))
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("qdrant: decode %s: %w", path, err)
	}
	return nil
}

// distance names a metric the way Qdrant does.
func distance(m store.Metric) string {
	switch m {
	case store.Inner:
		return "Dot"
	case store.Euclid:
		return "Euclid"
	default:
		return "Cosine"
	}
}

// filter renders a store filter as a Qdrant payload filter. Every condition
// must hold, so all but Ne become must clauses and Ne becomes must_not.
//
// Has has no exact counterpart: Qdrant matches a value against an array
// field by equality and a string field by full text, and full text matches
// whole words where store.Has matches any substring. A Has on a string
// field is therefore narrower here than in memory.
//
// A condition that cannot be expressed is refused. Dropping it would widen
// the search silently, and a filter that quietly admits what it was asked
// to exclude is worse than no answer.
func filter(f store.Filter) (map[string]any, error) {
	var must, not []map[string]any
	for _, c := range f {
		switch c.Op {
		case store.Eq:
			must = append(must, match(c.Field, c.Value))
		case store.Ne:
			not = append(not, match(c.Field, c.Value))
		case store.In:
			must = append(must, map[string]any{
				"key": c.Field, "match": map[string]any{"any": c.Value},
			})
		case store.Has:
			if s, ok := c.Value.(string); ok {
				must = append(must, map[string]any{
					"key": c.Field, "match": map[string]any{"text": s},
				})
				continue
			}
			must = append(must, match(c.Field, c.Value))
		case store.Gt, store.Ge, store.Lt, store.Le:
			must = append(must, map[string]any{
				"key": c.Field, "range": map[string]any{span(c.Op): c.Value},
			})
		default:
			return nil, fmt.Errorf("qdrant: cannot ask for %q on %q", c.Op, c.Field)
		}
	}
	if must == nil && not == nil {
		return nil, nil
	}
	out := map[string]any{}
	if must != nil {
		out["must"] = must
	}
	if not != nil {
		out["must_not"] = not
	}
	return out, nil
}

func match(field string, v any) map[string]any {
	return map[string]any{"key": field, "match": map[string]any{"value": v}}
}

// span names a bound the way Qdrant's range clause does.
func span(op store.Op) string {
	switch op {
	case store.Gt:
		return "gt"
	case store.Ge:
		return "gte"
	case store.Lt:
		return "lt"
	}
	return "lte"
}

// id reads a point id, which Qdrant answers as a string or as a number
// depending on what it was given.
func id(v any) string {
	switch n := v.(type) {
	case string:
		return n
	case float64:
		return fmt.Sprintf("%d", int64(n))
	case nil:
		return ""
	}
	return fmt.Sprint(v)
}
