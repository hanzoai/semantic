package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The walk, on a conversation small enough to know the right answer to.
//
// Three sessions, months apart, in which one person says where she camped
// three times and one thing about something else. The set the questions want
// is therefore split across the sessions, which is the shape the multi-hop
// questions have and the thing ranking turns by wording cannot get at.
//
// Each camping turn names one place and nothing else, so that what is being
// tested is whether the three are gathered and not whether a sentence with two
// content words in it can be told which of them was the answer.

func camping() Sample {
	when := func(month time.Month) time.Time {
		return time.Date(2023, month, 8, 13, 0, 0, 0, time.UTC)
	}
	return Sample{
		ID:  "camp",
		Who: [2]string{"Melanie", "Caroline"},
		Turns: []Turn{
			{ID: "D1:1", Session: 1, Date: when(time.January), Who: "Melanie",
				Text: "I camped at the beach."},
			{ID: "D1:2", Session: 1, Date: when(time.January), Who: "Caroline",
				Text: "That sounds cold, Melanie."},
			{ID: "D1:3", Session: 1, Date: when(time.January), Who: "Caroline",
				Text: "Melanie, you are braver than me."},
			{ID: "D2:1", Session: 2, Date: when(time.May), Who: "Melanie",
				Text: "I camped in the mountains."},
			{ID: "D2:2", Session: 2, Date: when(time.May), Who: "Melanie",
				Text: "I started pottery classes on Tuesdays."},
			{ID: "D3:1", Session: 3, Date: when(time.September), Who: "Melanie",
				Text: "I camped in the forest."},
		},
	}
}

func walker(t *testing.T) (*Path, *Words) {
	t.Helper()
	s := camping()
	words := Vocabulary(s.Turns)
	know, err := NewKnowledge(context.Background(), s, s.Turns, words)
	if err != nil {
		t.Fatal(err)
	}
	return NewPath(know), words
}

// A relation's values are one answer, however many sessions they were said
// over. This is the whole claim the arm makes.
func TestWalkGathersARelationAcrossSessions(t *testing.T) {
	p, _ := walker(t)
	cites, err := p.Recall(context.Background(), "Where has Melanie camped?", 5)
	if err != nil {
		t.Fatal(err)
	}
	said := strings.Join(ids(cites), " ")
	for _, want := range []string{"D1:1", "D2:1", "D3:1"} {
		if !contains(ids(cites), want) {
			t.Errorf("walking from Melanie missed %s; cited %s", want, said)
		}
	}
}

// What the reader then does with it. Gather states every value of the relation,
// which is what a question asking for a set is asking for; Reply states the
// best one or two, which is what a question with one answer wants. Both read
// the same evidence.
func TestGatherStatesTheWholeRelation(t *testing.T) {
	p, words := walker(t)
	ctx := context.Background()
	question := "Where has Melanie camped?"
	cites, err := p.Recall(ctx, question, 5)
	if err != nil {
		t.Fatal(err)
	}
	sure := Sure(words, question, cites)
	got := Gather(ctx, question, cites, sure, words, quiz)
	for _, want := range []string{"beach", "mountains", "forest"} {
		if !strings.Contains(got, want) {
			t.Errorf("gathered %q, which leaves out %s", got, want)
		}
	}
	if short := Reply(ctx, question, cites, sure, words, quiz); strings.Count(short, ",") >= 2 {
		t.Errorf("Reply stated %q, which is the whole set rather than the best of it", short)
	}
}

// A walk that reaches nothing has nothing to say. The graph is what it reads,
// so with the graph emptied it must return no evidence at all — the control
// the run reports under -blind, asserted here so a change that quietly stops
// reading the graph fails a test rather than passing unnoticed.
func TestWalkReadsTheGraph(t *testing.T) {
	s := camping()
	words := Vocabulary(s.Turns)
	know, err := NewKnowledge(context.Background(), s, s.Turns, words)
	if err != nil {
		t.Fatal(err)
	}
	know.Blind()
	cites, err := NewPath(know).Recall(context.Background(), "Where has Melanie camped?", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(cites) != 0 {
		t.Errorf("an emptied graph still answered with %v", ids(cites))
	}
}

// One relation said in two tenses is one relation. A conversation held over
// months says the same thing in whatever tense the month calls for, and a
// graph that files those apart has two relations with one value each.
func TestRelationFoldsTense(t *testing.T) {
	for _, c := range [][2]string{
		{"read", "reading"}, {"went", "went"}, {"not giving", "not gives"},
	} {
		if relation(c[0]) != relation(c[1]) {
			t.Errorf("%q and %q are %q and %q, which are two relations",
				c[0], c[1], relation(c[0]), relation(c[1]))
		}
	}
	if relation("eating") == relation("not eating") {
		t.Error("not eating is a tense of eating")
	}
}
