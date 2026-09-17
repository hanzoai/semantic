package main

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

// The model reader, without a model. What is tested here is everything around
// the call: what the model is shown, what is kept of what it says, what a
// failure costs, and that the same question is bought once.

// stub is a model that answers with a fixed string and counts how often it was
// asked, and what it was asked.
type stub struct {
	text  string
	err   error
	asked []string
	n     *atomic.Int64
}

func (s *stub) Complete(ctx context.Context, prompt string, schema any) (string, error) {
	s.n.Add(1)
	s.asked = append(s.asked, prompt)
	return s.text, s.err
}

func talker(text string, err error) (Model, *stub) {
	m := &stub{text: text, err: err, n: new(atomic.Int64)}
	return Model{Chat: m, Calls: new(atomic.Int64), Hits: new(atomic.Int64), Fails: new(atomic.Int64)}, m
}

// The floor belongs to the memory and is applied before the model is asked, so
// an arm that abstains deterministically abstains with a model behind it too —
// and costs nothing to do so.
func TestModelHonoursTheFloor(t *testing.T) {
	turn := Turn{ID: "D1:1", Who: "Evan", Date: day, Text: "Nothing to see."}
	words := Vocabulary([]Turn{turn})
	m, chat := talker("Paris", nil)

	if got := m.Read(context.Background(), "Where?", nil, 1, words, quiz); !declined(got) {
		t.Errorf("answered %q with no evidence at all", got)
	}
	cites := []Cite{{Turn: turn, Score: 1}}
	if got := m.Read(context.Background(), "Where?", cites, 0.1, words, quiz); !declined(got) {
		t.Errorf("answered %q under the floor of %v", got, quiz.Floor)
	}
	if n := chat.n.Load(); n != 0 {
		t.Errorf("asked the model %d times for questions the memory had already declined", n)
	}
}

// The model is shown the date, the speaker and the words of every turn it was
// given, each once, and the question.
func TestModelShowsItsEvidence(t *testing.T) {
	turns := []Turn{
		{ID: "D1:1", Who: "Jolene", Date: day, Text: "I have two snakes, Susie and Seraphim."},
		{ID: "D1:2", Who: "Nate", Date: day, Text: "Snakes are such good company."},
	}
	words := Vocabulary(turns)
	m, chat := talker("Susie, Seraphim", nil)
	cites := []Cite{{Turn: turns[0], Score: 1}, {Turn: turns[1], Score: 1}, {Turn: turns[0], Score: 1}}

	got := m.Read(context.Background(), "What are Jolene's snakes called?", cites, 1, words, quiz)
	if got != "Susie, Seraphim" {
		t.Errorf("kept %q of the model's answer", got)
	}
	if len(chat.asked) != 1 {
		t.Fatalf("sent %d requests for one question", len(chat.asked))
	}
	prompt := chat.asked[0]
	for _, want := range []string{"Susie and Seraphim", "good company", "What are Jolene's snakes called?", "No information available"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt never mentions %q", want)
		}
	}
	if n := strings.Count(prompt, "I have two snakes"); n != 1 {
		t.Errorf("showed the same turn %d times", n)
	}
}

// A model that cannot be reached is an abstention, and is counted as one, so
// the number of questions the run lost is reportable beside the table.
func TestModelCountsWhatItLost(t *testing.T) {
	turn := Turn{ID: "D1:1", Who: "Evan", Date: day, Text: "I moved to Paris in March."}
	words := Vocabulary([]Turn{turn})
	m, _ := talker("", errors.New("gateway said no"))
	cites := []Cite{{Turn: turn, Score: 1}}

	if got := m.Read(context.Background(), "Where did Evan move?", cites, 1, words, quiz); !declined(got) {
		t.Errorf("answered %q from a model that failed", got)
	}
	if c, f := m.Calls.Load(), m.Fails.Load(); c != 1 || f != 1 {
		t.Errorf("counted %d questions and %d failures, wanted 1 and 1", c, f)
	}
}

// What is stripped from a reply is wrapping and never content.
func TestTidy(t *testing.T) {
	for _, c := range []struct{ said, want string }{
		{"  Paris  ", "Paris"},
		{"Answer: Paris", "Paris"},
		{`"Paris"`, "Paris"},
		{"Paris.", "Paris"},
		{"<think>she said March</think>\n8 May, 2023", "8 May, 2023"},
		{"Susie, Seraphim.", "Susie, Seraphim"},
		{"", Decline},
		{"   ", Decline},
		{"No information available", Decline},
		{"Paris, France...", "Paris, France..."},
	} {
		if got := tidy(c.said); got != c.want {
			t.Errorf("tidy(%q) = %q, wanted %q", c.said, got, c.want)
		}
	}
}

// The same question asked twice is bought once, and the second answer is the
// first one — which is what makes a stopped run resumable and a sweep cheap.
func TestMemoBuysOnce(t *testing.T) {
	chat := &stub{text: "Paris", n: new(atomic.Int64)}
	hits := new(atomic.Int64)
	memo := Memo{Model: chat, Dir: t.TempDir(), Name: "test-model", Hits: hits}

	for range 3 {
		got, err := memo.Complete(context.Background(), "where?", nil)
		if err != nil || got != "Paris" {
			t.Fatalf("answered %q, %v", got, err)
		}
	}
	if n := chat.n.Load(); n != 1 {
		t.Errorf("bought the same question %d times", n)
	}
	if h := hits.Load(); h != 2 {
		t.Errorf("counted %d answers off disk, wanted 2", h)
	}
	if _, err := memo.Complete(context.Background(), "when?", nil); err != nil {
		t.Fatalf("a different question: %v", err)
	}
	if n := chat.n.Load(); n != 2 {
		t.Errorf("a different question was bought %d times", n-1)
	}
}

// A failed call is not written down, so the next run asks it again rather than
// inheriting an abstention nobody made.
func TestMemoKeepsNoFailure(t *testing.T) {
	chat := &stub{err: errors.New("busy"), n: new(atomic.Int64)}
	memo := Memo{Model: chat, Dir: t.TempDir(), Name: "test-model", Hits: new(atomic.Int64)}
	for range 2 {
		if _, err := memo.Complete(context.Background(), "where?", nil); err == nil {
			t.Fatal("a failed call came back as an answer")
		}
	}
	if n := chat.n.Load(); n != 2 {
		t.Errorf("asked %d times after a failure, wanted 2", n)
	}
}

// One model arm per memory, not one per arm: two readings of the same memory
// return the same evidence, and the model would be shown it twice.
func TestTwinIsOnePerMemory(t *testing.T) {
	turns := []Turn{{ID: "D1:1", Who: "Evan", Date: day, Text: "Nothing to see."}}
	words := Vocabulary(turns)
	vector, err := NewVector(context.Background(), turns, words)
	if err != nil {
		t.Fatalf("NewVector: %v", err)
	}
	m, _ := talker("Paris", nil)
	arms := []Arm{
		{Name: "vector", Memory: vector, Read: Reply},
		{Name: "vector+set", Memory: vector, Read: Gather},
	}
	got := twin(arms, m.Read)
	if len(got) != 1 || got[0].Name != "vector+model" {
		t.Fatalf("twinned %d arms %v, wanted one called vector+model", len(got), armNames(got))
	}
	if got[0].Memory != vector {
		t.Error("the model arm reads a different memory from the arm it twins")
	}
}

func armNames(arms []Arm) []string {
	out := make([]string, 0, len(arms))
	for _, a := range arms {
		out = append(out, a.Name)
	}
	return out
}

// The contamination control is asked the question and shown nothing, whatever
// evidence happened to be lying around, and whatever the floor says.
func TestAloneShowsNothing(t *testing.T) {
	turn := Turn{ID: "D1:1", Who: "Jolene", Date: day, Text: "I have two snakes, Susie and Seraphim."}
	words := Vocabulary([]Turn{turn})
	m, chat := talker("Susie, Seraphim", nil)
	cites := []Cite{{Turn: turn, Score: 1}}

	got := m.Alone(context.Background(), "What are Jolene's snakes called?", cites, 0, words, quiz)
	if got != "Susie, Seraphim" {
		t.Errorf("kept %q of the model's answer", got)
	}
	if len(chat.asked) != 1 {
		t.Fatalf("sent %d requests for one question", len(chat.asked))
	}
	prompt := chat.asked[0]
	if strings.Contains(prompt, "Susie and Seraphim") {
		t.Error("the control was shown the evidence it is a control for")
	}
	for _, want := range []string{"What are Jolene's snakes called?", "No information available"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt never mentions %q", want)
		}
	}

	if cites, err := (Nothing{}).Recall(context.Background(), "anything?", 5); err != nil || cites != nil {
		t.Errorf("the empty memory returned %d turns, %v", len(cites), err)
	}
}
