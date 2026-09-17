package agent

import (
	"reflect"
	"testing"
	"time"
)

func TestScopeCovers(t *testing.T) {
	held := Scope{Agent: "ada", Session: "s1", Task: "t1"}
	for _, c := range []struct {
		name   string
		filter Scope
		want   bool
	}{
		{"the empty filter asks nothing", Scope{}, true},
		{"the same agent", Scope{Agent: "ada"}, true},
		{"another agent", Scope{Agent: "bob"}, false},
		{"agent and session", Scope{Agent: "ada", Session: "s1"}, true},
		{"right agent, wrong session", Scope{Agent: "ada", Session: "s2"}, false},
		{"right session, wrong task", Scope{Session: "s1", Task: "t2"}, false},
	} {
		if got := c.filter.Covers(held); got != c.want {
			t.Errorf("%s: Covers = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestWindowIsBoundedByCount(t *testing.T) {
	m := &Memory{Window: 3}
	for _, id := range []string{"a", "b", "c", "d"} {
		m.Add(Note{ID: id, Text: id, At: at(1)})
	}
	if got := noteIDs(m.Recent()); !reflect.DeepEqual(got, []string{"b", "c", "d"}) {
		t.Errorf("the window holds %v, want the last three", got)
	}
	if m.Count() != 4 {
		t.Errorf("%d notes kept, want 4: leaving the window is not forgetting", m.Count())
	}
	if _, ok := m.Get("a"); !ok {
		t.Error("the note that left the window is gone; it should still be readable")
	}
}

func TestWindowIsBoundedBySize(t *testing.T) {
	// Four characters to a token, so forty characters is ten tokens.
	m := &Memory{Window: 10, Budget: 20}
	long := func(n int) string {
		s := ""
		for range n {
			s += "x"
		}
		return s
	}
	m.Add(Note{ID: "a", Text: long(40), At: at(1)})
	m.Add(Note{ID: "b", Text: long(40), At: at(1)})
	if got := noteIDs(m.Recent()); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("the window holds %v, want both: twenty tokens is the budget", got)
	}
	m.Add(Note{ID: "c", Text: long(40), At: at(1)})
	if got := noteIDs(m.Recent()); !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Errorf("the window holds %v, want the last two: the third pushed the first out", got)
	}
}

func TestHoldForgetsWhatIsOldEnough(t *testing.T) {
	m := &Memory{Hold: 48 * time.Hour}
	m.Add(Note{ID: "old", Text: "old", At: at(1)})
	m.Add(Note{ID: "recent", Text: "recent", At: at(3)})
	if m.Count() != 2 {
		t.Fatalf("%d notes kept, want both: two days is the hold", m.Count())
	}
	m.Add(Note{ID: "now", Text: "now", At: at(5)})
	if _, ok := m.Get("old"); ok {
		t.Error("a note four days past the hold is still kept")
	}
	if _, ok := m.Get("recent"); !ok {
		t.Error("a note inside the hold was forgotten")
	}
}

func TestAddFillsWhatIsMissing(t *testing.T) {
	m := &Memory{}
	id := m.Add(Note{Text: "something"})
	if id == "" {
		t.Fatal("Add returned no id")
	}
	n, _ := m.Get(id)
	if n.At.IsZero() {
		t.Error("the note has no time")
	}
	if other := m.Add(Note{Text: "something else"}); other == id {
		t.Error("two notes were given the same id")
	}
	if m.Add(Note{ID: id, Text: "replaced"}); m.Count() != 2 {
		t.Errorf("%d notes kept after rewriting one, want 2", m.Count())
	}
	if n, _ := m.Get(id); n.Text != "replaced" {
		t.Errorf("the note reads %q, want the rewrite", n.Text)
	}
}

func TestForgetAndAbout(t *testing.T) {
	m := &Memory{}
	m.Add(Note{ID: "a", Text: "loan for ada", Of: []string{"ada"}, At: at(1)})
	m.Add(Note{ID: "b", Text: "loan for bob", Of: []string{"bob"}, At: at(2)})
	m.Add(Note{ID: "c", Text: "ada and bob", Of: []string{"ada", "bob"}, At: at(3)})

	if got := noteIDs(m.About("ada")); !reflect.DeepEqual(got, []string{"a", "c"}) {
		t.Errorf("notes about ada are %v, want a and c", got)
	}
	if !m.Forget("a") {
		t.Fatal("Forget reported nothing to forget")
	}
	if m.Forget("a") {
		t.Error("forgetting twice reported a second forgetting")
	}
	if got := noteIDs(m.About("ada")); !reflect.DeepEqual(got, []string{"c"}) {
		t.Errorf("notes about ada are %v, want only c", got)
	}
	if got := noteIDs(m.Recent()); !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Errorf("the window holds %v, want b and c", got)
	}
}

func TestRecallRanksTheWindowHigher(t *testing.T) {
	m := &Memory{Window: 1}
	m.Add(Note{ID: "full", Text: "python language", At: at(1)})
	m.Add(Note{ID: "half", Text: "python only", At: at(2)}) // the window now holds this

	got := m.Recall("python language", 0, Scope{})
	if len(got) != 2 {
		t.Fatalf("recalled %v, want both notes", found(got))
	}
	if want := []string{"full", "half"}; !reflect.DeepEqual(found(got), want) {
		t.Errorf("recalled %v, want %v: being recent does not beat answering the question", found(got), want)
	}
	almost(t, "the score of the full match", got[0].Score, 1)
	almost(t, "the score of the half match in the window", got[1].Score, 0.6)
	if got[0].Fresh || !got[1].Fresh {
		t.Errorf("freshness is %v and %v, want only the note in the window marked", got[0].Fresh, got[1].Fresh)
	}

	m.Add(Note{ID: "other", Text: "python alone", At: at(3)}) // half leaves the window
	got = m.Recall("python language", 0, Scope{})
	if want := []string{"full", "other", "half"}; !reflect.DeepEqual(found(got), want) {
		t.Errorf("recalled %v, want %v: of two equal answers the recent one leads", found(got), want)
	}
	almost(t, "the score of the half match once it has left the window", got[2].Score, 0.5)
}

func TestRecallNarrowsToScope(t *testing.T) {
	m := &Memory{}
	m.Add(Note{ID: "mine", Text: "loan approved", Scope: Scope{Agent: "ada"}, At: at(1)})
	m.Add(Note{ID: "theirs", Text: "loan approved", Scope: Scope{Agent: "bob"}, At: at(2)})

	got := m.Recall("loan", 0, Scope{Agent: "ada"})
	if len(got) != 1 || got[0].Note.ID != "mine" {
		t.Errorf("recalled %v, want only ada's note", found(got))
	}
	if n := len(m.Recall("loan", 0, Scope{})); n != 2 {
		t.Errorf("the empty scope recalled %d notes, want both", n)
	}
	if n := len(m.All(Scope{Agent: "bob"})); n != 1 {
		t.Errorf("All for bob gave %d notes, want 1", n)
	}
	if got := m.Recall("   ", 0, Scope{}); got != nil {
		t.Errorf("an empty question recalled %v", found(got))
	}
}

func TestRecallCountIsACap(t *testing.T) {
	m := &Memory{}
	for _, id := range []string{"a", "b", "c"} {
		m.Add(Note{ID: id, Text: "loan " + id, At: at(1)})
	}
	if n := len(m.Recall("loan", 2, Scope{})); n != 2 {
		t.Errorf("asked for 2, got %d", n)
	}
	if n := len(m.Recall("loan", 0, Scope{})); n != 3 {
		t.Errorf("asked for all, got %d", n)
	}
}

func TestTokens(t *testing.T) {
	for _, c := range []struct {
		in   string
		want int
	}{{"", 0}, {"abc", 0}, {"abcd", 1}, {"abcdefgh", 2}} {
		if got := tokens(c.in); got != c.want {
			t.Errorf("tokens(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func noteIDs(ns []Note) []string {
	var out []string
	for _, n := range ns {
		out = append(out, n.ID)
	}
	return out
}

func found(fs []Found) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Note.ID)
	}
	return out
}
