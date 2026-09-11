package extract

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// asked is one request the client sent, decoded far enough to assert on.
type asked struct {
	path string
	auth string
	body map[string]any
}

// endpoint stands in for the gateway: it records what it was sent and answers
// with the statuses it was told to, in order, the last one repeating.
func endpoint(t *testing.T, reply string, status ...int) (Chat, *[]asked, *atomic.Int32) {
	t.Helper()
	var got []asked
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a := asked{path: r.URL.Path, auth: r.Header.Get("Authorization")}
		if err := json.NewDecoder(r.Body).Decode(&a.body); err != nil {
			t.Errorf("request carried no JSON: %v", err)
		}
		got = append(got, a)
		i := int(n.Add(1)) - 1
		code := http.StatusOK
		if len(status) > 0 {
			code = status[min(i, len(status)-1)]
		}
		w.Header().Set("Content-Type", "application/json")
		if code != http.StatusOK {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(code)
			w.Write([]byte(`{"error":{"message":"busy"}}`))
			return
		}
		w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return Chat{Base: srv.URL + "/v1", Model: "test-model", Key: "secret"}, &got, &n
}

const oneChoice = `{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`

func TestChatSends(t *testing.T) {
	c, got, _ := endpoint(t, oneChoice)
	c.Cap, c.Temp = 32, 0.2

	text, err := c.Complete(context.Background(), "say hello", nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if text != "hello" {
		t.Errorf("reply is %q, want %q", text, "hello")
	}
	if len(*got) != 1 {
		t.Fatalf("sent %d requests, want 1", len(*got))
	}
	a := (*got)[0]
	if a.path != "/v1/chat/completions" {
		t.Errorf("posted to %q", a.path)
	}
	if a.auth != "Bearer secret" {
		t.Errorf("authorization is %q", a.auth)
	}
	if a.body["model"] != "test-model" {
		t.Errorf("model is %v", a.body["model"])
	}
	if a.body["max_tokens"] != 32.0 {
		t.Errorf("max_tokens is %v, want 32", a.body["max_tokens"])
	}
	if a.body["temperature"] != 0.2 {
		t.Errorf("temperature is %v, want 0.2", a.body["temperature"])
	}
	if _, ok := a.body["response_format"]; ok {
		t.Error("a prompt with no schema asked for a JSON object anyway")
	}
	msgs, _ := a.body["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("sent %d messages, want 1", len(msgs))
	}
	m, _ := msgs[0].(map[string]any)
	if m["role"] != "user" || m["content"] != "say hello" {
		t.Errorf("message is %v", m)
	}
}

// The zero Temp is temperature 0 and is sent, because leaving it out would
// take whatever the server's default sampling is and make a run unrepeatable.
func TestChatSendsZeroTemperature(t *testing.T) {
	c, got, _ := endpoint(t, oneChoice)
	if _, err := c.Complete(context.Background(), "hi", nil); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if v, ok := (*got)[0].body["temperature"]; !ok || v != 0.0 {
		t.Errorf("temperature is %v, want 0", v)
	}
}

// A Kind that states a schema asks the endpoint for a JSON object.
func TestChatAsksForJSON(t *testing.T) {
	c, got, _ := endpoint(t, `{"choices":[{"message":{"content":"{\"entities\":[]}"}}]}`)
	if _, err := c.Complete(context.Background(), "find them", Ents); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	f, ok := (*got)[0].body["response_format"].(map[string]any)
	if !ok || f["type"] != "json_object" {
		t.Errorf("response_format is %v", (*got)[0].body["response_format"])
	}
}

// A busy endpoint is asked again; the reply that eventually arrives is the one
// the caller gets.
func TestChatRetriesTransient(t *testing.T) {
	c, got, _ := endpoint(t, oneChoice, http.StatusTooManyRequests, http.StatusServiceUnavailable, http.StatusOK)
	text, err := c.Complete(context.Background(), "say hello", nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if text != "hello" {
		t.Errorf("reply is %q", text)
	}
	if len(*got) != 3 {
		t.Errorf("sent %d requests, want 3", len(*got))
	}
}

// A rejected credential will be rejected again, so it is not sent again.
func TestChatDoesNotRetryRefusal(t *testing.T) {
	c, got, _ := endpoint(t, oneChoice, http.StatusUnauthorized)
	_, err := c.Complete(context.Background(), "say hello", nil)
	if err == nil {
		t.Fatal("a 401 was reported as success")
	}
	if len(*got) != 1 {
		t.Errorf("sent %d requests, want 1", len(*got))
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error does not name the status: %v", err)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Errorf("error carries the credential: %v", err)
	}
}

// Every attempt failing reports the last failure rather than nothing.
func TestChatGivesUp(t *testing.T) {
	c, got, _ := endpoint(t, oneChoice, http.StatusServiceUnavailable)
	c.Tries = 2
	if _, err := c.Complete(context.Background(), "say hello", nil); err == nil {
		t.Fatal("a persistent 503 was reported as success")
	}
	if len(*got) != 2 {
		t.Errorf("sent %d requests, want 2", len(*got))
	}
}

func TestChatNeedsBaseAndModel(t *testing.T) {
	for _, c := range []Chat{{Model: "m"}, {Base: "http://x/v1"}} {
		if _, err := c.Complete(context.Background(), "hi", nil); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: error is %v, want ErrInvalid", c, err)
		}
	}
}

func TestChatNeedsPrompt(t *testing.T) {
	c, _, _ := endpoint(t, oneChoice)
	if _, err := c.Complete(context.Background(), "  ", nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("error is %v, want ErrInvalid", err)
	}
}

// A reply with no choices in it is an error and not an empty answer: a caller
// that cannot tell the two apart records silence as a finding.
func TestChatRejectsEmptyReply(t *testing.T) {
	c, _, _ := endpoint(t, `{"choices":[]}`)
	if _, err := c.Complete(context.Background(), "hi", nil); err == nil {
		t.Fatal("a reply with no choices was reported as success")
	}
}

// A cancelled context stops the wait between attempts rather than sleeping
// through it.
func TestChatStopsOnCancel(t *testing.T) {
	c, _, _ := endpoint(t, oneChoice, http.StatusServiceUnavailable)
	ctx, stop := context.WithCancel(context.Background())
	stop()
	done := make(chan error, 1)
	go func() { _, err := c.Complete(ctx, "hi", nil); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a cancelled call was reported as success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled call did not return")
	}
}

// LLM asks a Chat the same way it asks any other Model, so the one shipped
// implementation reaches the prompts this package already writes.
func TestChatServesLLM(t *testing.T) {
	c, got, _ := endpoint(t, `{"choices":[{"message":{"content":"{\"entities\":[{\"text\":\"Ada\",\"label\":\"PERSON\"}]}"}}]}`)
	ents, err := LLM{Model: c}.Entities(context.Background(), "Ada wrote the notes.")
	if err != nil {
		t.Fatalf("Entities: %v", err)
	}
	if len(ents) != 1 || ents[0].Text != "Ada" || ents[0].Label != "PERSON" {
		t.Fatalf("entities are %+v", ents)
	}
	if len(*got) != 1 {
		t.Errorf("sent %d requests, want 1", len(*got))
	}
}

func TestRetryAfter(t *testing.T) {
	for _, c := range []struct {
		header string
		want   time.Duration
	}{
		{"", time.Second},
		{"3", 3 * time.Second},
		{"0", 0},
		{"600", 30 * time.Second},
		{"whenever", time.Second},
	} {
		if got := after(c.header); got != c.want {
			t.Errorf("after(%q) = %v, want %v", c.header, got, c.want)
		}
	}
}
