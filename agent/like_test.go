package agent

import (
	"reflect"
	"testing"
)

func TestWords(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
	}{
		{"Mortgage Approval", []string{"mortgage", "approval"}},
		{"risk  risk\trisk", []string{"risk"}},
		{"   ", nil},
		{"", nil},
	} {
		if got := words(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("words(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// alike is the port of _calculate_decision_content_similarity, including the
// rule that the bigram fallback may not touch ordinary multi-word queries.
func TestAlike(t *testing.T) {
	for _, c := range []struct {
		name string
		q    string
		text string
		want float64
	}{
		{
			name: "words shared over words held between them",
			q:    "mortgage approval",
			text: "mortgage approval for a buyer",
			want: 2.0 / 5.0,
		},
		{
			name: "nothing in common",
			q:    "mortgage approval",
			text: "weather forecast",
			want: 0,
		},
		{
			name: "a multi-word query never falls back to bigrams",
			q:    "mortgage approval",
			text: "mortgageapproval",
			want: 0,
		},
		{
			name: "a single token may fall back, and here it helps",
			q:    "mortgage",
			text: "mortgage rates",
			want: 7.0 / 12.0,
		},
		{
			name: "a two-character query has too few bigrams to count",
			q:    "ab",
			text: "abacus abandon able",
			want: 0,
		},
		{
			name: "a script without word spaces is compared by bigrams",
			q:    "融資審査",
			text: "融資審査の記録",
			want: 3.0 / 6.0,
		},
		{
			name: "nothing to compare",
			q:    "",
			text: "anything",
			want: 0,
		},
	} {
		if got := alike(c.q, c.text); got != c.want {
			t.Errorf("%s: alike(%q, %q) = %v, want %v", c.name, c.q, c.text, got, c.want)
		}
	}
}

func TestSpaced(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"plain english", true},
		{"", true},
		{"融資", false},
		{"ひらがな", false},
		{"カタカナ", false},
		{"한글", false},
		{"mixed 融資 text", false},
	} {
		if got := spaced(c.in); got != c.want {
			t.Errorf("spaced(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestLikeByWordsAndByShape(t *testing.T) {
	var g Graph
	g.Add(Node{ID: "a", Kind: "case", Text: "mortgage approval for a buyer"})
	g.Add(Node{ID: "b", Kind: "case", Text: "mortgage approval for a second buyer"})
	g.Add(Node{ID: "c", Kind: "case", Text: "weather forecast"})
	g.Add(Node{ID: "d", Kind: "other", Text: "mortgage approval for a buyer"})
	// a and b each hold two edges; c holds none.
	for _, l := range []Link{
		{From: "a", To: "x", Label: About}, {From: "a", To: "y", Label: About},
		{From: "b", To: "x", Label: About}, {From: "b", To: "z", Label: About},
	} {
		g.Join(Edge{Link: l})
	}

	// Words do not care what kind of thing a node is, so d — same text,
	// different kind — is the closest by words and the furthest by shape.
	got := g.Like("a", false, 2)
	if want := []string{"d", "b"}; !reflect.DeepEqual(hits(got), want) {
		t.Fatalf("by words the closest are %v, want %v", hits(got), want)
	}
	if got[0].Score != 1 {
		t.Errorf("the identical text scored %v, want 1", got[0].Score)
	}
	almost(t, "the score for b", got[1].Score, 5.0/6.0)

	shape := g.Like("a", true, 0)
	by := map[string]float64{}
	for _, h := range shape {
		by[h.Node.ID] = h.Score
	}
	if by["b"] != 1 {
		t.Errorf("b sits like a in the graph but scored %v, want 1", by["b"])
	}
	if by["c"] != 0 {
		t.Errorf("c holds no edges at all but scored %v, want 0", by["c"])
	}
	if by["d"] != 0 {
		t.Errorf("d is another kind of thing but scored %v; kinds are not comparable", by["d"])
	}
	if got := g.Like("nobody", false, 0); got != nil {
		t.Errorf("asking about a node that is not there gave %v", hits(got))
	}
}

func hits(hs []Hit) []string {
	var out []string
	for _, h := range hs {
		out = append(out, h.Node.ID)
	}
	return out
}
