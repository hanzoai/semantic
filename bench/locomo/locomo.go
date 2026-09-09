package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hanzoai/semantic"
	"github.com/hanzoai/semantic/ingest"
)

// The LoCoMo dataset. Ten conversations, each held over dozens of sessions
// months apart, and about two hundred questions per conversation asking what
// was said. Sessions live under numbered keys rather than in an array, so the
// conversation is read as a map and put back in order here.

// Turn is one utterance: who said it, when, and the identifier the dataset
// uses when it points at it as evidence.
type Turn struct {
	ID      string // dialogue id, "D3:12"
	Session int
	Date    time.Time
	Who     string
	Text    string
}

// Line is the turn as the official retriever sees it, date and speaker
// included, so a retrieved unit here is the same unit measured there.
func (t Turn) Line() string {
	return "(" + Day(t.Date) + ") " + t.Who + ` said, "` + t.Text + `"`
}

// Ask is one question, the answer the dataset holds, and the turns it says
// carry that answer.
type Ask struct {
	Text     string
	Answer   string
	Kind     int
	Evidence []string
}

// Sample is one conversation: two speakers, every turn in order, and the
// questions asked about it.
type Sample struct {
	ID    string
	Who   [2]string
	Turns []Turn
	Asks  []Ask
}

// Day writes a date the way the dataset writes it in prose, which is the form
// an answer to a "when" question is compared against.
func Day(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2 January, 2006")
}

// Source is where the dataset lives. The copy on HuggingFace is empty — the
// repository there holds a README and nothing else — so this is the one that
// works, and fetching the wrong one is what stalls a first attempt at this
// benchmark.
const Source = "https://raw.githubusercontent.com/snap-research/locomo/main/data/locomo10.json"

// Load reads the dataset, fetching it on first use. The fetch goes through the
// library's own web ingester, which is the same stage a pipeline would use to
// read anything else off the network.
func Load(path string) ([]Sample, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		if raw, err = fetch(context.Background(), path); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	var file []struct {
		ID   string                     `json:"sample_id"`
		Conv map[string]json.RawMessage `json:"conversation"`
		QA   []struct {
			Question  string          `json:"question"`
			Answer    json.RawMessage `json:"answer"`
			Adversary json.RawMessage `json:"adversarial_answer"`
			Kind      int             `json:"category"`
			Evidence  []string        `json:"evidence"`
		} `json:"qa"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("locomo: %w", err)
	}

	out := make([]Sample, 0, len(file))
	for _, in := range file {
		s := Sample{ID: in.ID}
		if err := json.Unmarshal(in.Conv["speaker_a"], &s.Who[0]); err != nil {
			return nil, fmt.Errorf("locomo: %s: speaker_a: %w", in.ID, err)
		}
		if err := json.Unmarshal(in.Conv["speaker_b"], &s.Who[1]); err != nil {
			return nil, fmt.Errorf("locomo: %s: speaker_b: %w", in.ID, err)
		}
		s.Turns, err = turns(in.Conv)
		if err != nil {
			return nil, fmt.Errorf("locomo: %s: %w", in.ID, err)
		}
		for _, q := range in.QA {
			// An adversarial question carries its plausible wrong answer
			// under its own key, and the reference answer is that there is
			// none. Reading either into the same field keeps the rest of the
			// program from having to know which kind it is holding.
			answer := text(q.Answer)
			if answer == "" {
				answer = text(q.Adversary)
			}
			s.Asks = append(s.Asks, Ask{
				Text:     q.Question,
				Answer:   answer,
				Kind:     q.Kind,
				Evidence: q.Evidence,
			})
		}
		out = append(out, s)
	}
	return out, nil
}

// fetch downloads the dataset and keeps it, so the next run is offline.
func fetch(ctx context.Context, path string) ([]byte, error) {
	fmt.Fprintf(os.Stderr, "locomo: fetching %s\n", Source)
	docs, err := ingest.Web{Decode: whole{}, Max: 1 << 26}.Ingest(ctx, Source)
	if err != nil {
		return nil, err
	}
	if len(docs) != 1 {
		return nil, fmt.Errorf("locomo: %s returned %d documents", Source, len(docs))
	}
	body := []byte(docs[0].Text)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return body, os.WriteFile(path, body, 0o644)
}

// whole is the decoder that keeps a response as it arrived. The dataset is
// JSON with a shape of its own, so it is decoded here rather than by whatever
// the content type suggests.
type whole struct{}

func (whole) Decode(b []byte, o ingest.Origin) ([]semantic.Doc, error) {
	return []semantic.Doc{{ID: o.Ref, Source: o.Ref, Text: string(b)}}, nil
}

// turns collects the sessions of a conversation into one ordered run of turns.
// A session key with no turns behind it — the dataset carries a few — is a
// date for a session that was never written, and is skipped.
func turns(conv map[string]json.RawMessage) ([]Turn, error) {
	var nums []int
	for k := range conv {
		if m := session.FindStringSubmatch(k); m != nil {
			n, _ := strconv.Atoi(m[1])
			nums = append(nums, n)
		}
	}
	sort.Ints(nums)

	var out []Turn
	for _, n := range nums {
		var when time.Time
		if raw, ok := conv[fmt.Sprintf("session_%d_date_time", n)]; ok {
			var s string
			if err := json.Unmarshal(raw, &s); err != nil {
				return nil, err
			}
			t, err := time.Parse("3:04 pm on 2 January, 2006", strings.TrimSpace(s))
			if err != nil {
				return nil, fmt.Errorf("session %d date %q: %w", n, s, err)
			}
			when = t
		}
		var said []struct {
			ID      string `json:"dia_id"`
			Who     string `json:"speaker"`
			Text    string `json:"text"`
			Caption string `json:"blip_caption"`
		}
		if err := json.Unmarshal(conv[fmt.Sprintf("session_%d", n)], &said); err != nil {
			return nil, fmt.Errorf("session %d: %w", n, err)
		}
		for _, t := range said {
			text := t.Text
			if t.Caption != "" {
				// A shared photograph is part of what was said, and the
				// official retriever indexes its caption alongside the words.
				text += "\n[shares " + t.Caption + "]"
			}
			out = append(out, Turn{ID: t.ID, Session: n, Date: when, Who: t.Who, Text: text})
		}
	}
	return out, nil
}

// text reads a JSON value that may have been written as a string or as a
// number, which the answers are.
func text(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return strings.Trim(string(raw), `"`)
}

var session = regexp.MustCompile(`^session_(\d+)$`)
