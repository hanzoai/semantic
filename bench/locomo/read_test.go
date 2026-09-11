package main

import (
	"context"
	"testing"
	"time"
)

// The reader, on its own. It is shared by every arm, so a mistake here moves
// every column of the report by the same amount and hides itself.

func TestReplyReadsTheCalendar(t *testing.T) {
	may := time.Date(2023, time.May, 8, 13, 56, 0, 0, time.UTC)
	for _, c := range []struct {
		name     string
		question string
		text     string
		want     string
	}{{
		name:     "a month later in the year is the year before",
		question: "When did Evan start lifting weights?",
		text:     "I started lifting weights back in October and I feel stronger already.",
		want:     "October 2022",
	}, {
		name:     "a month earlier in the year is this year",
		question: "When did Evan join the gym?",
		text:     "I joined the gym in March, so it has been a while.",
		want:     "March 2023",
	}, {
		name:     "a day beside the month is taken with it",
		question: "When did Evan run the race?",
		text:     "I ran the race on 3 April and it nearly finished me.",
		want:     "3 April, 2023",
	}, {
		name:     "yesterday is the day before the session",
		question: "When did Evan go to the support group?",
		text:     "I went to the support group yesterday and it was powerful.",
		want:     "7 May, 2023",
	}, {
		name:     "with no date in the words, the session's own date stands",
		question: "When did Evan adopt the puppy?",
		text:     "I adopted a puppy and she is already ruling the house.",
		want:     "8 May, 2023",
	}} {
		t.Run(c.name, func(t *testing.T) {
			turn := Turn{ID: "D1:1", Who: "Evan", Text: c.text, Date: may}
			words := Vocabulary([]Turn{turn})
			cites := []Cite{{Turn: turn, Score: 1}}
			if got := Reply(context.Background(), c.question, cites, 1, words, quiz); got != c.want {
				t.Errorf("answered %q, wanted %q", got, c.want)
			}
		})
	}
}

func TestReplyDeclines(t *testing.T) {
	words := Vocabulary([]Turn{{ID: "D1:1", Text: "Nothing to see."}})
	if got := Reply(context.Background(), "What did Evan eat?", nil, 0, words, quiz); !declined(got) {
		t.Errorf("answered %q with no evidence at all", got)
	}
	cites := []Cite{{Turn: Turn{ID: "D1:1", Text: "Nothing to see."}, Score: 1}}
	if got := Reply(context.Background(), "What did Evan eat?", cites, 0.1, words, quiz); !declined(got) {
		t.Errorf("answered %q from evidence it was 10%% sure of, under a floor of %v", got, quiz.Floor)
	}
}

// The answer is the part of the sentence the question did not already contain,
// and the informative part of what is left.
func TestReplyQuotesWhatWasAsked(t *testing.T) {
	turns := []Turn{
		{ID: "D1:1", Who: "Jolene", Date: day, Text: "I have two snakes, Susie and Seraphim."},
		{ID: "D1:2", Who: "Nate", Date: day, Text: "That is wonderful, snakes are such good company."},
		{ID: "D1:3", Who: "Nate", Date: day, Text: "I have a dog and two cats at home."},
	}
	vocab := Vocabulary(turns)
	cites := []Cite{{Turn: turns[0], Score: 1}}
	got := Reply(context.Background(), "What are the names of Jolene's snakes?", cites, 1, vocab, quiz)
	said := words(got)
	for _, name := range []string{"susie", "seraphim"} {
		if !contains(said, name) {
			t.Errorf("answered %q, which leaves out %s", got, name)
		}
	}
	if contains(said, "snake") || contains(said, "snakes") {
		t.Errorf("answered %q, which gives the question its own word back", got)
	}
}
