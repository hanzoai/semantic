package extract

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Chat is a Model served by a chat-completions endpoint — the shape OpenAI
// published and most gateways now speak, including Hanzo's own at
// https://api.hanzo.ai/v1. It is the one implementation of Model this package
// ships: everything else here works from rules and needs no network, and the
// prompts in prompt.go had nowhere to be sent until now.
//
// The wire is spoken directly rather than through a client library, so the
// module stays on the standard library and the request is the one a reader can
// check against the endpoint's own documentation.
//
//	m := extract.Chat{Base: "https://api.hanzo.ai/v1", Model: "gpt-oss-120b", Key: key}
//	set, err := extract.LLM{Model: m}.Extract(ctx, chunk)
type Chat struct {
	// Base is the API root, without a trailing slash: https://api.hanzo.ai/v1.
	Base string
	// Model is the model id, as /v1/models lists it.
	Model string
	// Key is sent as a bearer credential. Empty sends no Authorization
	// header, which is what an endpoint on a trusted socket wants.
	Key string
	// Cap is the most tokens the reply may run to. Zero leaves it to the
	// server.
	Cap int
	// Temp is the sampling temperature, sent as given — so the zero value is
	// temperature 0, which is what an extraction wants and what makes a run
	// repeatable.
	Temp float64
	// Tries is how many times a request that failed in a way that may pass on
	// a second attempt — a timeout, a 429, a 5xx — is sent again, with a delay
	// that honours Retry-After. Zero means three.
	Tries int
	// HTTP sends the request. Nil uses a client with a two-minute timeout.
	HTTP *http.Client
}

// Chat answers "what does the model say".
var _ Model = Chat{}

// Complete sends one prompt and returns the reply's text.
//
// When schema is a Kind that states one, the request asks for a JSON object,
// which is what stops a model wrapping its answer in prose the caller then has
// to cut back out. A model that ignores the request is not punished for it:
// JSON recovers the object from whatever it was wrapped in.
func (c Chat) Complete(ctx context.Context, prompt string, schema any) (string, error) {
	if strings.TrimSpace(c.Base) == "" || strings.TrimSpace(c.Model) == "" {
		return "", fmt.Errorf("chat: %w: base and model are required", ErrInvalid)
	}
	if strings.TrimSpace(prompt) == "" {
		return "", fmt.Errorf("chat: %w: empty prompt", ErrInvalid)
	}

	ask := map[string]any{
		"model":       c.Model,
		"messages":    []map[string]string{{"role": "user", "content": prompt}},
		"temperature": c.Temp,
	}
	if c.Cap > 0 {
		ask["max_tokens"] = c.Cap
	}
	if k, ok := schema.(Kind); ok && k.JSON() != "" {
		ask["response_format"] = map[string]string{"type": "json_object"}
	}
	body, err := json.Marshal(ask)
	if err != nil {
		return "", fmt.Errorf("chat: %w", err)
	}

	tries := c.Tries
	if tries <= 0 {
		tries = 3
	}
	var last error
	for try := 0; try < tries; try++ {
		text, wait, err := c.once(ctx, body)
		if err == nil {
			return text, nil
		}
		last = err
		if wait < 0 || try == tries-1 {
			return "", err
		}
		// An endpoint that says how long to wait is obeyed. One that only says
		// it is busy is backed away from, doubling each time, because several
		// callers retrying a shared limit in step is how a burst becomes a
		// queue that never drains.
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(min(wait<<try, most)):
		}
	}
	return "", last
}

// once sends the request once. The duration it returns beside an error is how
// long to wait before sending it again; negative means the failure will not
// pass — a rejected key, a model that does not exist, a prompt the endpoint
// refuses — and sending it again would only ask the same question twice.
func (c Chat) once(ctx context.Context, body []byte) (string, time.Duration, error) {
	url := strings.TrimRight(c.Base, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", -1, fmt.Errorf("chat: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Key != "" {
		req.Header.Set("Authorization", "Bearer "+c.Key)
	}

	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	resp, err := client.Do(req)
	if err != nil {
		// The request never arrived, so it is safe to send again.
		return "", time.Second, fmt.Errorf("chat: %s: %w", c.Model, err)
	}
	defer resp.Body.Close()

	// Enough of the body to say what went wrong, and not so much that a
	// server having a bad day fills the caller's log.
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("chat: %s: %s: %s", c.Model, resp.Status, trimTo(raw, 400))
		if again(resp.StatusCode) {
			return "", after(resp.Header.Get("Retry-After")), err
		}
		return "", -1, err
	}

	var reply struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &reply); err != nil {
		return "", -1, fmt.Errorf("chat: %s: %w: %s", c.Model, err, trimTo(raw, 200))
	}
	if len(reply.Choices) == 0 {
		return "", -1, fmt.Errorf("chat: %s: no choices: %s", c.Model, trimTo(raw, 200))
	}
	return reply.Choices[0].Message.Content, 0, nil
}

// again is whether a status is worth a second attempt: the endpoint is busy,
// or something behind it broke in a way that is nobody's fault here.
func again(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// most is the longest this client waits between attempts, so that a server
// asking for an hour does not hang the caller.
const most = 30 * time.Second

// after reads a Retry-After header as a delay. Seconds and HTTP dates are both
// spelled there; anything else waits a second.
func after(header string) time.Duration {
	if s, err := strconv.Atoi(strings.TrimSpace(header)); err == nil && s >= 0 {
		return min(time.Duration(s)*time.Second, most)
	}
	if t, err := http.ParseTime(strings.TrimSpace(header)); err == nil {
		if d := time.Until(t); d > 0 {
			return min(d, most)
		}
	}
	return time.Second
}

// trimTo is the first n bytes of a server's reply, on one line, for an error
// message. The credential is never in the reply, and the prompt is never in
// the error.
func trimTo(b []byte, n int) string {
	s := strings.Join(strings.Fields(string(b)), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
