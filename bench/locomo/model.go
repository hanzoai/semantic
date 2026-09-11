package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/hanzoai/semantic/extract"
)

// The reader with a model behind it.
//
// Every other column of this report is bounded by Reply, which quotes the
// sentence covering most of the question and keeps the rarest few words of it.
// That bound is not small: handed the turns the dataset itself names as the
// evidence, Reply scores 0.202 on the answerable questions and 0.068 on the
// multi-hop ones. So the answerable half of the table is nearly insensitive to
// which memory found the evidence, and a retrieval result cannot be read off
// it. This reader replaces that half of the machinery and leaves the other
// half — the same memories, the same questions, the same floor, the same
// scorer — exactly as it was.
//
// It is an arm and not a replacement. The deterministic reader is what makes
// the benchmark runnable by anyone without a credential, and both are reported
// side by side.

// Model answers from the evidence by asking a language model for a few words.
//
// The floor stays where it is and is applied before the model is asked: below
// it the memory is saying it has nothing, and that is a property of the memory
// which every arm is entitled to have read the same way. Above it the model
// may still decline, and on an adversarial question it should — the evidence a
// memory returns for a question about the wrong person is real evidence about
// the wrong person, which is precisely the trap.
type Model struct {
	Chat extract.Model
	// Name is the model id, for the line that says what the run cost.
	Name string

	// Calls, Hits and Fails count what the run asked, what it did not have to
	// buy, and what it lost, reported at the end so a number is never quoted
	// without saying how much of it the model actually answered.
	Calls, Hits, Fails *atomic.Int64
}

// NewModel is the reader a run reads with, pointed at a chat-completions
// endpoint and at the answers it already has on disk.
//
// The reply is capped because the metric marks token overlap with a short
// reference answer: a model that writes a paragraph is marked down for the
// paragraph, so nothing is gained by paying for one. The cap is loose enough
// that a model which thinks before answering still reaches its answer.
// Temperature is zero, which is what makes the run repeatable and what makes
// the answers on disk the same answers.
func NewModel(base, name, key, memo string) Model {
	m := Model{Name: name, Calls: new(atomic.Int64), Hits: new(atomic.Int64), Fails: new(atomic.Int64)}
	m.Chat = Memo{
		Model: extract.Chat{Base: base, Model: name, Key: key, Cap: 384, Tries: 7},
		Dir:   memo, Name: name, Hits: m.Hits,
	}
	return m
}

// Cost is what the run asked of the model, in the terms a reader of the report
// needs to judge it: how many questions went to the model, how many were
// answered from a previous run at no cost, and how many failed and were
// therefore scored as an abstention the model never made.
func (m Model) Cost() string {
	return fmt.Sprintf("model %s: %d questions asked, %d answered from disk, %d bought, %d failed",
		m.Name, m.Calls.Load(), m.Hits.Load(), m.Calls.Load()-m.Hits.Load(), m.Fails.Load())
}

// Read puts the evidence and the question to the model and returns the few
// words it answers with.
//
// A failed call is a decline and is counted. That is the conservative choice
// in both directions: a lost answerable question scores zero, and a lost
// adversarial one scores a point it did not earn, so the failure count is
// printed beside the table rather than buried.
func (m Model) Read(ctx context.Context, question string, cites []Cite, sure float64, w *Words, o Options) string {
	if len(cites) == 0 || sure < o.Floor {
		return Decline
	}
	seen := make(map[string]bool, len(cites))
	lines := make([]string, 0, len(cites))
	for _, c := range cites {
		if seen[c.Turn.ID] {
			continue
		}
		seen[c.Turn.ID] = true
		lines = append(lines, c.Turn.Line())
	}

	m.Calls.Add(1)
	said, err := m.Chat.Complete(ctx, fmt.Sprintf(prompt, strings.Join(lines, "\n"), question), nil)
	if err != nil {
		if m.Fails.Add(1) <= 5 {
			fmt.Fprintln(os.Stderr, "locomo:", err)
		}
		return Decline
	}
	return tidy(said)
}

// prompt states the evidence, the question, and the shape of the answer. The
// shape is the metric's: it marks token overlap with a short reference answer
// after stemming, so a sentence of explanation around the right two words
// scores worse than the two words alone. It recognises an abstention by the
// words "no information available", which is why the wording is given exactly
// rather than described.
//
// The evidence is written the way the official RAG harness writes it — the
// date, the speaker, and what they said — so the model reads the same unit the
// retriever ranked.
const prompt = `Below are excerpts from a conversation between two friends held over many months. Each line gives the date it was said, who said it, and their words.

%s

Question: %s

Answer in a few words, using the conversation's own wording where you can. If the question asks for several things, separate them with commas. If it asks when something happened, answer with a date such as "8 May, 2023". If these excerpts do not answer the question, reply exactly: No information available.

Write the answer alone, with nothing before or after it.`

// tidy is the model's reply reduced to the answer. A model asked for a few
// words sometimes sends a sentence around them anyway; what is stripped here
// is only wrapping — quotes, a leading "Answer:", a trailing full stop — and
// never content, because trimming towards the reference answer would be
// marking the model's homework before scoring it.
func tidy(said string) string {
	out := strings.TrimSpace(said)
	if i := strings.LastIndex(out, "</think>"); i >= 0 {
		out = strings.TrimSpace(out[i+len("</think>"):])
	}
	out = strings.TrimSpace(strings.TrimPrefix(out, "Answer:"))
	out = strings.Trim(out, "\"'“”")
	if len(out) > 1 && strings.HasSuffix(out, ".") && !strings.HasSuffix(out, "..") {
		out = out[:len(out)-1]
	}
	if out == "" {
		return Decline
	}
	return out
}

// Memo is a model whose answers are kept on disk under the hash of what was
// asked, so the same question is paid for once.
//
// A run of this benchmark is thousands of requests against a quota that can
// end mid-table, and losing four hours of answers to one 429 would make the
// expensive arms unreportable. With the answers on disk the run resumes where
// it stopped, a sweep over a reader setting re-asks only what the setting
// changed, and the cost quoted in the report is the cost of the questions the
// model actually saw.
//
// Temperature is zero, so keeping the first answer is not an approximation of
// asking again — it is the same answer.
type Memo struct {
	extract.Model
	// Dir holds one file per question, named by hash. Empty asks every time.
	Dir string
	// Name distinguishes two models answering the same prompt.
	Name string
	// Hits counts the answers that cost nothing, which is the difference
	// between what was asked and what was bought.
	Hits *atomic.Int64
}

// Complete answers from disk when it can and writes down what it had to ask.
func (m Memo) Complete(ctx context.Context, prompt string, schema any) (string, error) {
	if m.Dir == "" {
		return m.Model.Complete(ctx, prompt, schema)
	}
	sum := sha256.Sum256([]byte(m.Name + "\n" + prompt))
	path := filepath.Join(m.Dir, hex.EncodeToString(sum[:])[:32])
	if said, err := os.ReadFile(path); err == nil {
		m.Hits.Add(1)
		return string(said), nil
	}
	said, err := m.Model.Complete(ctx, prompt, schema)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(m.Dir, 0o755); err == nil {
		os.WriteFile(path, []byte(said), 0o644)
	}
	return said, nil
}

// twin is the model-read counterpart of every distinct memory in a set.
//
// One per memory rather than one per arm: two arms over the same memory are
// two ways of reading it, and this reader is a third, so twinning graph+edge
// as well as graph would send the model the same evidence twice and charge for
// it twice. The evidence is what the memory returned; how the deterministic
// column chose to read it is not the model's business.
func twin(arms []Arm, read Reader) []Arm {
	seen := make(map[string]bool, len(arms))
	out := make([]Arm, 0, len(arms))
	for _, a := range arms {
		if seen[a.Memory.Name()] {
			continue
		}
		seen[a.Memory.Name()] = true
		out = append(out, Arm{Name: a.Memory.Name() + "+model", Memory: a.Memory, Read: read})
	}
	return out
}

// Alone is the same reader with the evidence withheld: the model is shown the
// question and nothing else.
//
// It is the contamination control, and it is the reason a model arm can be
// quoted at all. LoCoMo has been public since 2024 and a model may have read
// it. If it has, a model arm is not reading the turns it was handed but
// recalling an answer it already had, and the lift would belong to the
// training set rather than to the memory that found the evidence. What this
// arm scores is what the model produces with no conversation in front of it,
// so the part of a model arm worth attributing to retrieval is what it has
// over this one.
//
// The floor and the evidence are both ignored, so that this arm is asked every
// question the model arms are asked at floor zero and the two columns are
// comparable question for question.
func (m Model) Alone(ctx context.Context, question string, cites []Cite, sure float64, w *Words, o Options) string {
	m.Calls.Add(1)
	said, err := m.Chat.Complete(ctx, fmt.Sprintf(bare, question), nil)
	if err != nil {
		if m.Fails.Add(1) <= 5 {
			fmt.Fprintln(os.Stderr, "locomo:", err)
		}
		return Decline
	}
	return tidy(said)
}

// bare is the prompt with the conversation taken out of it. Everything else —
// what an answer should look like, how to abstain, how to write a date — is
// word for word the prompt the model arms use, so the difference between the
// two columns is the evidence and not the wording.
const bare = `Two friends held a conversation over many months. Answer this question about it.

Question: %s

Answer in a few words. If the question asks for several things, separate them with commas. If it asks when something happened, answer with a date such as "8 May, 2023". If you do not know, reply exactly: No information available.

Write the answer alone, with nothing before or after it.`

// Nothing is a memory holding nothing, which is what the contamination control
// is asked to answer from. Its recall is zero and says so, rather than
// borrowing another arm's column and reading as though evidence was used.
type Nothing struct{}

// Name is what this arm is called in the results.
func (Nothing) Name() string { return "closed" }

// Recall returns nothing, which is the whole point.
func (Nothing) Recall(ctx context.Context, question string, k int) ([]Cite, error) {
	return nil, ctx.Err()
}
