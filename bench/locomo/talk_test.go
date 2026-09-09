package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The claims a turn makes, and the claims it must not make. The second list is
// the one that matters: a memory that binds every topic to everybody present
// answers an adversarial question confidently and wrongly, and the whole
// benchmark turns on that.

func TestReadBindsTheSubject(t *testing.T) {
	for _, c := range []struct {
		name    string
		speaker string
		text    string
		want    []string // subject/object pairs that must be claimed
		wrong   []string // pairs that must not be
	}{{
		name:    "first person is the speaker",
		speaker: "Caroline",
		text:    "I went to a LGBTQ support group yesterday and it was so powerful.",
		want:    []string{"caroline/lgbtq support group"},
	}, {
		name:    "second person is the other one",
		speaker: "Caroline",
		text:    "You should try the pottery class.",
		want:    []string{"melanie/pottery class"},
		wrong:   []string{"caroline/pottery class"},
	}, {
		name:    "a named third party is themselves",
		speaker: "Caroline",
		text:    "Melanie adopted a puppy last month.",
		want:    []string{"melanie/puppy last month"},
		wrong:   []string{"caroline/puppy last month"},
	}, {
		name:    "a question claims nothing",
		speaker: "Tim",
		text:    "How's it going with yoga? Have you noticed any improvements?",
		wrong:   []string{"tim/yoga", "melanie/yoga"},
	}, {
		name:    "a negation is kept as a negation",
		speaker: "Caroline",
		text:    "I don't eat dairy any more.",
		want:    []string{"caroline/dairy"},
	}, {
		name:    "a name reaches the thing it names",
		speaker: "Jolene",
		text:    "I have a snake named Susie.",
		want:    []string{"jolene/snake", "snake/susie"},
	}} {
		t.Run(c.name, func(t *testing.T) {
			turn := Turn{ID: "D1:1", Who: c.speaker, Text: c.text, Date: day}
			claims := Read(turn, 0, c.speaker, "Melanie", map[string]bool{"melanie": true})
			var held []string
			for _, claim := range claims {
				held = append(held, strings.ToLower(claim.Subject)+"/"+claim.Object)
			}
			for _, want := range c.want {
				if !among(held, want) {
					t.Errorf("did not claim %q; claimed %v", want, held)
				}
			}
			for _, wrong := range c.wrong {
				if among(held, wrong) {
					t.Errorf("claimed %q, which the turn does not say", wrong)
				}
			}
		})
	}
}

func TestReadCarriesTheDateAndTheTurn(t *testing.T) {
	turn := Turn{ID: "D7:3", Who: "Evan", Text: "I started lifting weights.", Date: day}
	claims := Read(turn, 12, "Evan", "John", nil)
	if len(claims) == 0 {
		t.Fatal("no claims")
	}
	for _, c := range claims {
		if c.Cite != "D7:3" || c.Turn != 12 {
			t.Errorf("claim points at %q/%d, not D7:3/12", c.Cite, c.Turn)
		}
		if c.From != "8 May, 2023" {
			t.Errorf("claim is dated %q, not the session's own date", c.From)
		}
		if c.Said != "I started lifting weights." {
			t.Errorf("claim quotes %q, not the sentence it came from", c.Said)
		}
	}
}

// The adversarial question the benchmark turns on, end to end. John does yoga
// and Tim asks him about it. A memory that indexes words has both of them near
// the word; a memory that indexes who did what has only John.
func TestGraphDoesNotLendOneSpeakersTopicToTheOther(t *testing.T) {
	s := Sample{
		ID:  "test",
		Who: [2]string{"Tim", "John"},
		Turns: []Turn{
			{ID: "D20:1", Session: 20, Date: day, Who: "Tim",
				Text: "Hey John! I had a tough exam last week that had me doubting myself."},
			{ID: "D20:2", Session: 20, Date: day, Who: "John",
				Text: "Congrats on your success! I'm also trying out yoga to get a little extra strength and flexibility."},
			{ID: "D20:3", Session: 20, Date: day, Who: "Tim",
				Text: "Thanks! I appreciate your encouragement. How's it going with yoga? Have you noticed any improvements?"},
		},
	}
	words := Vocabulary(s.Turns)
	graph, err := NewKnowledge(context.Background(), s, s.Turns, words)
	if err != nil {
		t.Fatal(err)
	}

	const about = "What is Tim trying out to improve his strength and flexibility?"
	cites, err := graph.Recall(context.Background(), about, 5)
	if err != nil {
		t.Fatal(err)
	}
	sure := Sure(words, about, cites)
	for _, c := range cites {
		if c.Turn.ID == "D20:2" {
			t.Errorf("asked about Tim and offered John's turn %q", c.Turn.Text)
		}
	}
	if reply := Reply(about, cites, sure, words, quiz); !declined(reply) {
		t.Errorf("answered %q; nothing in the conversation says Tim does yoga", reply)
	}

	// The same memory must still find John, or it has learnt nothing at all.
	const his = "What is John trying out to improve his strength and flexibility?"
	cites, err = graph.Recall(context.Background(), his, 5)
	if err != nil {
		t.Fatal(err)
	}
	sure = Sure(words, his, cites)
	if len(cites) == 0 || cites[0].Turn.ID != "D20:2" {
		t.Fatalf("asked about John and did not offer his own turn: %v", ids(cites))
	}
	if reply := Reply(his, cites, sure, words, quiz); declined(reply) {
		t.Error("declined a question its own graph answers")
	}
}

// quiz is the reader's settings for the tests: answer whenever there is
// anything to answer with, and keep it short.
var quiz = Options{K: 5, Floor: 0.3, Span: 4, Band: 0.9, Parts: 2}

var day = time.Date(2023, time.May, 8, 13, 56, 0, 0, time.UTC)

// among reports whether any claim was made about that subject and covered that
// object. The object may say more than the test names — "a support group
// yesterday" is one phrase — so it is looked for inside rather than equal to.
func among(held []string, want string) bool {
	who, what, _ := strings.Cut(want, "/")
	for _, h := range held {
		got, said, _ := strings.Cut(h, "/")
		if got == who && strings.Contains(said, what) {
			return true
		}
	}
	return false
}
