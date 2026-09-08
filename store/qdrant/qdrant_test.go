package qdrant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/hanzoai/semantic/store"
)

// call is one request the store made, decoded far enough to assert on.
type call struct {
	method string
	path   string
	query  string
	key    string
	body   map[string]any
}

// serve stands in for Qdrant: it records what it was asked and answers with
// what it was told to.
func serve(t *testing.T, answers map[string]string) (*Store, *[]call) {
	t.Helper()
	var got []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := call{
			method: r.Method,
			path:   r.URL.Path,
			query:  r.URL.RawQuery,
			key:    r.Header.Get("api-key"),
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("%s %s was sent as %q", r.Method, r.URL.Path, r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&c.body); err != nil {
			t.Errorf("%s %s carried no JSON: %v", r.Method, r.URL.Path, err)
		}
		got = append(got, c)
		w.Header().Set("Content-Type", "application/json")
		if a, ok := answers[r.URL.Path]; ok {
			w.Write([]byte(a))
			return
		}
		w.Write([]byte(`{"status":"ok","time":0.001}`))
	}))
	t.Cleanup(srv.Close)

	s, err := New(srv.URL + "/docs")
	if err != nil {
		t.Fatal(err)
	}
	s.HTTP = srv.Client()
	return s, &got
}

func only(t *testing.T, got []call) call {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("made %d requests, want one: %+v", len(got), got)
	}
	return got[0]
}

func TestNew(t *testing.T) {
	for _, c := range []struct {
		name, dsn       string
		url, collection string
		key             string
		bad             bool
	}{
		{name: "plain", dsn: "http://localhost:6333/docs", url: "http://localhost:6333", collection: "docs"},
		{name: "https", dsn: "https://qdrant.internal/docs", url: "https://qdrant.internal", collection: "docs"},
		{name: "a key in the url", dsn: "https://secret@qdrant.internal:6333/docs", url: "https://qdrant.internal:6333", collection: "docs", key: "secret"},
		{name: "a trailing slash", dsn: "http://localhost:6333/docs/", url: "http://localhost:6333", collection: "docs"},
		{name: "no collection", dsn: "http://localhost:6333", bad: true},
		{name: "no host", dsn: "/docs", bad: true},
		{name: "a path, not a collection", dsn: "http://localhost:6333/a/b", bad: true},
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
			if s.URL != c.url || s.Collection != c.collection || s.Key != c.key {
				t.Errorf("read %q as url %q collection %q key %q", c.dsn, s.URL, s.Collection, s.Key)
			}
		})
	}
}

func TestCreate(t *testing.T) {
	for _, c := range []struct {
		metric store.Metric
		want   string
	}{
		{"", "Cosine"},
		{store.Cosine, "Cosine"},
		{store.Inner, "Dot"},
		{store.Euclid, "Euclid"},
	} {
		t.Run(string(c.metric)+" "+c.want, func(t *testing.T) {
			s, got := serve(t, nil)
			s.Metric = c.metric
			if err := s.Create(t.Context(), 384); err != nil {
				t.Fatal(err)
			}
			r := only(t, *got)
			if r.method != http.MethodPut || r.path != "/collections/docs" {
				t.Errorf("sent %s %s, want PUT /collections/docs", r.method, r.path)
			}
			want := map[string]any{
				"vectors": map[string]any{"size": 384.0, "distance": c.want},
			}
			if !reflect.DeepEqual(r.body, want) {
				t.Errorf("sent %v, want %v", r.body, want)
			}
		})
	}
}

func TestPut(t *testing.T) {
	s, got := serve(t, nil)
	s.Key = "secret"
	err := s.Put(t.Context(), "a", []float32{1, 0.5}, map[string]any{"lang": "en"})
	if err != nil {
		t.Fatal(err)
	}
	r := only(t, *got)
	if r.method != http.MethodPut || r.path != "/collections/docs/points" {
		t.Errorf("sent %s %s, want PUT /collections/docs/points", r.method, r.path)
	}
	if r.query != "wait=true" {
		t.Errorf("sent ?%s, want wait=true so the write is readable when it returns", r.query)
	}
	if r.key != "secret" {
		t.Errorf("sent api-key %q", r.key)
	}
	want := map[string]any{"points": []any{map[string]any{
		"id":      "a",
		"vector":  []any{1.0, 0.5},
		"payload": map[string]any{"lang": "en"},
	}}}
	if !reflect.DeepEqual(r.body, want) {
		t.Errorf("sent %v, want %v", r.body, want)
	}
}

func TestPutWithoutMetadataSendsNoPayload(t *testing.T) {
	s, got := serve(t, nil)
	if err := s.Put(t.Context(), "a", []float32{1}, nil); err != nil {
		t.Fatal(err)
	}
	point := only(t, *got).body["points"].([]any)[0].(map[string]any)
	if _, ok := point["payload"]; ok {
		t.Errorf("sent an empty payload: %v", point)
	}
}

func TestAddBatchesIntoOneRequest(t *testing.T) {
	s, got := serve(t, nil)
	err := s.Add(t.Context(),
		store.Item{ID: "a", Vec: []float32{1, 0}},
		store.Item{ID: "b", Vec: []float32{0, 1}},
	)
	if err != nil {
		t.Fatal(err)
	}
	points := only(t, *got).body["points"].([]any)
	if len(points) != 2 {
		t.Fatalf("sent %d points in the batch, want 2", len(points))
	}
	if points[0].(map[string]any)["id"] != "a" || points[1].(map[string]any)["id"] != "b" {
		t.Errorf("sent %v, want a then b", points)
	}
}

func TestAddChecksIDs(t *testing.T) {
	s, got := serve(t, nil)
	err := s.Add(t.Context(), store.Item{ID: "", Vec: []float32{1}})
	if !errors.Is(err, store.ErrID) {
		t.Errorf("gave %v, want store.ErrID", err)
	}
	if len(*got) != 0 {
		t.Errorf("asked the server anyway: %+v", *got)
	}
	if err := s.Add(t.Context()); err != nil {
		t.Errorf("adding nothing gave %v", err)
	}
	if len(*got) != 0 {
		t.Errorf("adding nothing made a request: %+v", *got)
	}
}

func TestNear(t *testing.T) {
	s, got := serve(t, map[string]string{
		"/collections/docs/points/search": `{"result":[
			{"id":"a","version":3,"score":0.94,"payload":{"lang":"en"}},
			{"id":"b","version":3,"score":0.11,"payload":null}
		],"status":"ok","time":0.002}`,
	})
	ms, err := s.Near(t.Context(), []float32{1, 0}, 2)
	if err != nil {
		t.Fatal(err)
	}
	r := only(t, *got)
	if r.method != http.MethodPost || r.path != "/collections/docs/points/search" {
		t.Errorf("sent %s %s", r.method, r.path)
	}
	want := map[string]any{
		"vector":       []any{1.0, 0.0},
		"limit":        2.0,
		"with_payload": true,
	}
	if !reflect.DeepEqual(r.body, want) {
		t.Errorf("sent %v, want %v", r.body, want)
	}

	if len(ms) != 2 {
		t.Fatalf("read %d matches, want 2", len(ms))
	}
	if ms[0].ID != "a" || ms[0].Score != 0.94 || ms[0].Meta["lang"] != "en" {
		t.Errorf("first match is %+v", ms[0])
	}
	if ms[1].ID != "b" || ms[1].Score != 0.11 || ms[1].Meta != nil {
		t.Errorf("second match is %+v", ms[1])
	}
}

func TestNearDefaultsTheLimit(t *testing.T) {
	s, got := serve(t, map[string]string{"/collections/docs/points/search": `{"result":[]}`})
	if _, err := s.Near(t.Context(), []float32{1}, 0); err != nil {
		t.Fatal(err)
	}
	if k := only(t, *got).body["limit"]; k != 10.0 {
		t.Errorf("asked for %v, want the default 10: Qdrant has no way to say 'everything'", k)
	}
}

func TestNumericIDs(t *testing.T) {
	s, _ := serve(t, map[string]string{
		"/collections/docs/points/search": `{"result":[{"id":42,"score":0.5}]}`,
	})
	ms, err := s.Near(t.Context(), []float32{1}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if ms[0].ID != "42" {
		t.Errorf("read a numeric point id as %q, want \"42\"", ms[0].ID)
	}
}

func TestSearchFilter(t *testing.T) {
	for _, c := range []struct {
		name string
		f    store.Filter
		want map[string]any
	}{
		{"none", nil, nil},
		{
			"equal",
			store.Filter{}.Eq("lang", "en"),
			map[string]any{"must": []any{
				map[string]any{"key": "lang", "match": map[string]any{"value": "en"}},
			}},
		},
		{
			"not equal",
			store.Filter{}.Ne("lang", "en"),
			map[string]any{"must_not": []any{
				map[string]any{"key": "lang", "match": map[string]any{"value": "en"}},
			}},
		},
		{
			"one of",
			store.Filter{}.In("lang", "en", "fr"),
			map[string]any{"must": []any{
				map[string]any{"key": "lang", "match": map[string]any{"any": []any{"en", "fr"}}},
			}},
		},
		{
			"range",
			store.Filter{}.Ge("year", 2020).Lt("year", 2024),
			map[string]any{"must": []any{
				map[string]any{"key": "year", "range": map[string]any{"gte": 2020.0}},
				map[string]any{"key": "year", "range": map[string]any{"lt": 2024.0}},
			}},
		},
		{
			"greater and at most",
			store.Filter{}.Gt("year", 2020).Le("year", 2024),
			map[string]any{"must": []any{
				map[string]any{"key": "year", "range": map[string]any{"gt": 2020.0}},
				map[string]any{"key": "year", "range": map[string]any{"lte": 2024.0}},
			}},
		},
		{
			"a string is matched as text",
			store.Filter{}.Has("title", "world"),
			map[string]any{"must": []any{
				map[string]any{"key": "title", "match": map[string]any{"text": "world"}},
			}},
		},
		{
			"anything else is matched by value",
			store.Filter{}.Has("year", 2020),
			map[string]any{"must": []any{
				map[string]any{"key": "year", "match": map[string]any{"value": 2020.0}},
			}},
		},
		{
			"both directions at once",
			store.Filter{}.Eq("lang", "en").Ne("draft", true),
			map[string]any{
				"must":     []any{map[string]any{"key": "lang", "match": map[string]any{"value": "en"}}},
				"must_not": []any{map[string]any{"key": "draft", "match": map[string]any{"value": true}}},
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, got := serve(t, map[string]string{"/collections/docs/points/search": `{"result":[]}`})
			_, err := s.Search(t.Context(), store.Query{Vec: []float32{1}, K: 3, Filter: c.f})
			if err != nil {
				t.Fatal(err)
			}
			sent := only(t, *got).body["filter"]
			if c.want == nil {
				if sent != nil {
					t.Errorf("sent a filter for nothing: %v", sent)
				}
				return
			}
			if !reflect.DeepEqual(sent, any(c.want)) {
				t.Errorf("sent %v, want %v", sent, c.want)
			}
		})
	}
}

func TestGet(t *testing.T) {
	s, got := serve(t, map[string]string{
		"/collections/docs/points": `{"result":[
			{"id":"a","vector":[1,0],"payload":{"lang":"en"}}
		]}`,
	})
	items, err := s.Get(t.Context(), "a", "b")
	if err != nil {
		t.Fatal(err)
	}
	r := only(t, *got)
	want := map[string]any{
		"ids":          []any{"a", "b"},
		"with_payload": true,
		"with_vector":  true,
	}
	if r.method != http.MethodPost || r.path != "/collections/docs/points" {
		t.Errorf("sent %s %s", r.method, r.path)
	}
	if !reflect.DeepEqual(r.body, want) {
		t.Errorf("sent %v, want %v", r.body, want)
	}
	if len(items) != 1 || items[0].ID != "a" || items[0].Vec[0] != 1 || items[0].Meta["lang"] != "en" {
		t.Errorf("read %+v", items)
	}
}

func TestDrop(t *testing.T) {
	s, got := serve(t, nil)
	if err := s.Drop(t.Context(), "a", "b"); err != nil {
		t.Fatal(err)
	}
	r := only(t, *got)
	if r.method != http.MethodPost || r.path != "/collections/docs/points/delete" || r.query != "wait=true" {
		t.Errorf("sent %s %s?%s", r.method, r.path, r.query)
	}
	want := map[string]any{"points": []any{"a", "b"}}
	if !reflect.DeepEqual(r.body, want) {
		t.Errorf("sent %v, want %v", r.body, want)
	}

	if err := s.Drop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 1 {
		t.Errorf("dropping nothing made a request")
	}
}

func TestCount(t *testing.T) {
	s, got := serve(t, map[string]string{
		"/collections/docs/points/count": `{"result":{"count":17},"status":"ok"}`,
	})
	n, err := s.Count(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if n != 17 {
		t.Errorf("counted %d, want 17", n)
	}
	if body := only(t, *got).body; body["exact"] != true {
		t.Errorf("asked for %v, want an exact count", body)
	}
}

func TestReportsWhatTheServerSaid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"status":{"error":"Collection docs not found"}}`))
	}))
	defer srv.Close()

	s, err := New(srv.URL + "/docs")
	if err != nil {
		t.Fatal(err)
	}
	s.HTTP = srv.Client()
	err = s.Put(t.Context(), "a", []float32{1}, nil)
	if err == nil {
		t.Fatal("a 404 was taken for success")
	}
	for _, want := range []string{"404", "Collection docs not found", "/collections/docs/points"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q does not say %q", err, want)
		}
	}
}

func TestReportsUnreadableAnswers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("this is not JSON"))
	}))
	defer srv.Close()

	s, _ := New(srv.URL + "/docs")
	s.HTTP = srv.Client()
	if _, err := s.Near(t.Context(), []float32{1}, 1); err == nil {
		t.Fatal("nonsense was taken for an answer")
	}
}

func TestHonoursContext(t *testing.T) {
	s, got := serve(t, nil)
	ctx, stop := context.WithCancel(t.Context())
	stop()
	if err := s.Put(ctx, "a", []float32{1}, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("gave %v, want context.Canceled", err)
	}
	if len(*got) != 0 {
		t.Errorf("asked the server after the caller gave up: %+v", *got)
	}
}

func TestRegistered(t *testing.T) {
	if !store.Vectors.Has("qdrant") {
		t.Fatal("importing the package did not register the driver")
	}
	v, err := store.Vectors.Open("qdrant", "http://localhost:6333/docs")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := v.(*Store); !ok {
		t.Errorf("the driver opened a %T", v)
	}
	if _, err := store.Vectors.Open("qdrant", "nonsense"); err == nil {
		t.Error("the driver opened a source name that is not a URL")
	}
}

func TestSearchRefusesAFilterItCannotSend(t *testing.T) {
	s, got := serve(t, nil)
	f := store.Filter{{Field: "lang", Op: "sideways", Value: "en"}}
	_, err := s.Search(t.Context(), store.Query{Vec: []float32{1}, K: 3, Filter: f})
	if err == nil {
		t.Fatal("a condition the server cannot be asked was dropped instead")
	}
	if !strings.Contains(err.Error(), "sideways") {
		t.Errorf("%q does not say which condition", err)
	}
	if len(*got) != 0 {
		t.Errorf("searched anyway, on a filter that admits too much: %+v", *got)
	}
}

func TestSearchRefusesAMetricTheCollectionDoesNotUse(t *testing.T) {
	s, got := serve(t, nil)
	s.Metric = store.Cosine
	_, err := s.Search(t.Context(), store.Query{Vec: []float32{1}, K: 1, Metric: store.Euclid})
	if err == nil {
		t.Fatal("a query on the wrong metric was answered on the collection's")
	}
	if !strings.Contains(err.Error(), "Cosine") || !strings.Contains(err.Error(), "Euclid") {
		t.Errorf("%q does not say which metric is which", err)
	}
	if len(*got) != 0 {
		t.Errorf("asked the server anyway: %+v", *got)
	}

	// The store's own metric, said out loud, is not a disagreement.
	if _, err := s.Search(t.Context(), store.Query{Vec: []float32{1}, K: 1, Metric: store.Cosine}); err != nil {
		t.Errorf("asking for the metric the collection uses: %v", err)
	}
}
