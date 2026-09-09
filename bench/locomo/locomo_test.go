package main

import (
	"testing"
	"time"
)

// The dataset, read back. LoCoMo keeps its sessions under numbered keys rather
// than in an array and dates them in prose, so the loader has real work to do
// and getting any of it wrong moves every number in the report.

func TestLoad(t *testing.T) {
	samples, err := Load("data/locomo10.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 10 {
		t.Fatalf("%d conversations, not 10", len(samples))
	}

	turns, asks := 0, 0
	kind := map[int]int{}
	for _, s := range samples {
		turns += len(s.Turns)
		asks += len(s.Asks)
		for _, a := range s.Asks {
			kind[a.Kind]++
		}
		if s.Who[0] == "" || s.Who[1] == "" || s.Who[0] == s.Who[1] {
			t.Errorf("%s: speakers %q and %q", s.ID, s.Who[0], s.Who[1])
		}
		for i, turn := range s.Turns {
			if turn.ID == "" || turn.Who == "" || turn.Text == "" {
				t.Fatalf("%s: turn %d is missing something: %+v", s.ID, i, turn)
			}
			if turn.Date.IsZero() {
				t.Fatalf("%s: turn %s has no date", s.ID, turn.ID)
			}
			if i > 0 && s.Turns[i-1].Session > turn.Session {
				t.Fatalf("%s: session %d follows %d", s.ID, turn.Session, s.Turns[i-1].Session)
			}
		}
		for _, a := range s.Asks {
			if a.Text == "" || a.Answer == "" {
				t.Errorf("%s: question %q answered %q", s.ID, a.Text, a.Answer)
			}
			if a.Kind < 1 || a.Kind > 5 {
				t.Errorf("%s: category %d", s.ID, a.Kind)
			}
		}
	}

	// The counts the paper reports, and what this file actually holds.
	if turns != 5882 {
		t.Errorf("%d turns, not 5882", turns)
	}
	if asks != 1986 {
		t.Errorf("%d questions, not 1986", asks)
	}
	for got, want := range map[int]int{1: 282, 2: 321, 3: 96, 4: 841, 5: 446} {
		if kind[got] != want {
			t.Errorf("category %d has %d questions, not %d", got, kind[got], want)
		}
	}
}

func TestDates(t *testing.T) {
	samples, err := Load("data/locomo10.json")
	if err != nil {
		t.Fatal(err)
	}
	first := samples[0].Turns[0]
	want := time.Date(2023, time.May, 8, 13, 56, 0, 0, time.UTC)
	if !first.Date.Equal(want) {
		t.Errorf("first session is dated %v, not %v", first.Date, want)
	}
	if got := Day(first.Date); got != "8 May, 2023" {
		t.Errorf("written as %q", got)
	}
	if got := first.Line(); got != `(8 May, 2023) Caroline said, "Hey Mel! Good to see you! How have you been?"` {
		t.Errorf("indexed as %q", got)
	}
}

// Evidence points at turns that exist, or the recall measure is meaningless.
func TestEvidenceResolves(t *testing.T) {
	samples, err := Load("data/locomo10.json")
	if err != nil {
		t.Fatal(err)
	}
	cited, lost := 0, 0
	for _, s := range samples {
		known := map[string]bool{}
		for _, turn := range s.Turns {
			known[turn.ID] = true
		}
		for _, a := range s.Asks {
			for _, e := range a.Evidence {
				cited++
				if !known[trim(e)] {
					lost++
				}
			}
		}
	}
	t.Logf("%d evidence turns cited, %d of them not in the conversation", cited, lost)
	if share := float64(lost) / float64(cited); share > 0.02 {
		t.Errorf("%.1f%% of evidence does not resolve to a turn", 100*share)
	}
}
