package extract

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/hanzoai/semantic"
)

// LLM is a pipeline stage too, once it has a model.
var _ semantic.Extractor = LLM{}

// say is a Model that answers with what it was given, in order, and keeps the
// prompts it was asked. It is the whole of what this package needs from a
// provider, which is the point of the interface being one method wide.
type say struct {
	reply []string
	err   error

	asked []string
	kinds []Kind
}

func (s *say) Complete(_ context.Context, prompt string, schema any) (string, error) {
	s.asked = append(s.asked, prompt)
	if k, ok := schema.(Kind); ok {
		s.kinds = append(s.kinds, k)
	}
	if s.err != nil {
		return "", s.err
	}
	if len(s.reply) == 0 {
		return "", nil
	}
	out := s.reply[0]
	if len(s.reply) > 1 {
		s.reply = s.reply[1:]
	}
	return out, nil
}

const (
	jobs = "Steve Jobs"
	// The reply the Python suite uses for the co-founder case: three
	// founders, only one of whom the entity pass found.
	founders = `{"relations":[
	  {"subject":"Steve Jobs","predicate":"founded_by","object":"Apple Inc.","confidence":0.95},
	  {"subject":"Steve Wozniak","predicate":"founded_by","object":"Apple Inc.","confidence":0.93},
	  {"subject":"Ronald Wayne","predicate":"founded_by","object":"Apple Inc.","confidence":0.91}]}`
)

var (
	apple    = Entity{Text: "Apple Inc.", Label: "ORG", End: 10, Score: 1}
	jobsEnt  = Entity{Text: jobs, Label: "PERSON", End: 10, Score: 1}
	appleTxt = "Apple Inc. was founded by Steve Jobs, Steve Wozniak, and Ronald Wayne."
)

// --- recovering JSON from a reply -----------------------------------------

func TestJSON(t *testing.T) {
	for _, c := range []struct {
		name  string
		reply string
		want  string
	}{
		{"plain", `{"a":1}`, `{"a":1}`},
		{"a labelled fence", "```json\n{\"a\":1}\n```", `{"a":1}`},
		{"a bare fence", "here you go\n```\n{\"a\":1}\n```\nhope that helps", `{"a":1}`},
		{"wrapped in prose", `Sure! {"a":1} — let me know.`, `{"a":1}`},
		{"a trailing comma", `{"a":[1,2,],}`, `{"a":[1,2]}`},
		{"truncated at a token limit", `{"a":[{"b":1},{"b":2}`, `{"a":[{"b":1},{"b":2}]}`},
		{
			// Closing by a count of each kind would write }] here and lose
			// the whole reply; what was opened last must be closed first.
			"truncated inside the last entity",
			`{"entities":[{"text":"Alice","label":"PERSON"},{"text":"Bob"`,
			`{"entities":[{"text":"Alice","label":"PERSON"},{"text":"Bob"}]}`,
		},
		{
			"truncated just after a comma",
			`{"entities":[{"text":"Alice"},`,
			`{"entities":[{"text":"Alice"}]}`,
		},
		{
			// Braces inside a string are text, not structure.
			"a brace in a value",
			`{"a":"} ] {","b":[1`,
			`{"a":"} ] {","b":[1]}`,
		},
		{"a bare list", `[{"a":1}]`, `[{"a":1}]`},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := JSON(c.reply)
			if err != nil {
				t.Fatalf("JSON(%q): %v", c.reply, err)
			}
			if !json.Valid(got) {
				t.Fatalf("JSON(%q) = %s, which does not parse", c.reply, got)
			}
			var a, b any
			_ = json.Unmarshal(got, &a)
			_ = json.Unmarshal([]byte(c.want), &b)
			if !reflect.DeepEqual(a, b) {
				t.Errorf("JSON(%q) = %s, want %s", c.reply, got, c.want)
			}
		})
	}
}

func TestJSONRejectsWhatIsNotThere(t *testing.T) {
	// A reply cut off in the middle of a string is not recoverable: closing
	// the quote would invent a value the model never sent.
	for _, reply := range []string{"", "   ", "I could not do that.", "{{{", `{"entities":[{"text":"Ali`} {
		if got, err := JSON(reply); !errors.Is(err, ErrParse) {
			t.Errorf("JSON(%q) = %s, %v; want ErrParse", reply, got, err)
		}
	}
}

// --- pulling a list out of whatever wrapper it arrived in ------------------

func TestItems(t *testing.T) {
	for _, c := range []struct {
		name string
		doc  string
		want []string
	}{
		{"under the key the prompt named", `{"relations":[{"subject":"a","object":"b"}]}`, []string{"a"}},
		{"under data", `{"data":[{"subject":"a","object":"b"}]}`, []string{"a"}},
		{"under results", `{"results":[{"subject":"a","object":"b"}]}`, []string{"a"}},
		{"a bare list", `[{"subject":"a","object":"b"}]`, []string{"a"}},
		{"one lone object", `{"subject":"a","object":"b"}`, []string{"a"}},
		{"nothing was found", `{"note":"no relations"}`, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := items[Link]([]byte(c.doc), string(Rels))
			if err != nil {
				t.Fatalf("items: %v", err)
			}
			var subjects []string
			for _, k := range got {
				subjects = append(subjects, k.Subject)
			}
			if !reflect.DeepEqual(subjects, c.want) {
				t.Errorf("items = %v, want %v", subjects, c.want)
			}
		})
	}
	if _, err := items[Link]([]byte("  "), string(Rels)); !errors.Is(err, ErrParse) {
		t.Error("an empty document was not reported")
	}
}

// --- the shapes providers actually send ------------------------------------

func TestEntityWire(t *testing.T) {
	for _, c := range []struct {
		name string
		doc  string
		want Entity
	}{
		{
			"as the prompt asks for it",
			`{"text":"Alice","label":"PERSON","confidence":0.95}`,
			Entity{Text: "Alice", Label: "PERSON", End: 5, Score: 0.95},
		},
		{
			"type for label, value for text",
			`{"value":"Alice","type":"PERSON"}`,
			Entity{Text: "Alice", Label: "PERSON", End: 5},
		},
		{
			"offsets under their long names",
			`{"text":"Alice","label":"PERSON","start_char":10,"end_char":15,"confidence":1}`,
			Entity{Text: "Alice", Label: "PERSON", Start: 10, End: 15, Score: 1},
		},
		{
			"a confidence in quotes",
			`{"text":"Alice","label":"PERSON","confidence":"0.5"}`,
			Entity{Text: "Alice", Label: "PERSON", End: 5, Score: 0.5},
		},
		{
			// A stated entity with no score has none, and no number is made up
			// for it: zero is unscored.
			"no confidence at all",
			`{"text":"  Alice  ","label":"PERSON"}`,
			Entity{Text: "Alice", Label: "PERSON", End: 5},
		},
		{
			"a confidence outside the range it was asked for",
			`{"text":"Alice","label":"PERSON","confidence":42}`,
			Entity{Text: "Alice", Label: "PERSON", End: 5, Score: 1},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			var got Entity
			if err := json.Unmarshal([]byte(c.doc), &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("entity = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestEntityRoundTrip(t *testing.T) {
	// What this package writes, it must read back.
	want := Entity{Text: "Alice", Label: "PERSON", Start: 3, End: 8, Score: 0.42}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Entity
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip = %+v, want %+v (via %s)", got, want, b)
	}
}

func TestLinkWire(t *testing.T) {
	for _, c := range []struct {
		name string
		doc  string
		want Link
	}{
		{
			"as the prompt asks for it",
			`{"subject":"Apple","predicate":"acquired","object":"Beats","confidence":0.97}`,
			Link{Subject: "Apple", Predicate: "acquired", Object: "Beats", Score: 0.97},
		},
		{
			"source and target for the endpoints, label for the predicate",
			`{"source":"Apple","label":"acquired","target":"Beats"}`,
			Link{Subject: "Apple", Predicate: "acquired", Object: "Beats"},
		},
		{
			"with the time it held",
			`{"subject":"Apple","predicate":"acquired","object":"Beats","confidence":0.97,
			  "valid_from":"2014-05-01","valid_until":null,
			  "temporal_confidence":0.9,"temporal_source_text":"May 2014"}`,
			Link{
				Subject: "Apple", Predicate: "acquired", Object: "Beats", Score: 0.97,
				From: "2014-05-01", When: 0.9, Cite: "May 2014",
			},
		},
		{
			"no time signal in the text",
			`{"subject":"Microsoft","predicate":"develops","object":"Windows",
			  "valid_from":null,"valid_until":null,"temporal_confidence":0.0,
			  "temporal_source_text":null}`,
			Link{Subject: "Microsoft", Predicate: "develops", Object: "Windows"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			var got Link
			if err := json.Unmarshal([]byte(c.doc), &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("link = %+v, want %+v", got, c.want)
			}
		})
	}
}

// --- entities --------------------------------------------------------------

func TestLLMEntities(t *testing.T) {
	m := &say{reply: []string{
		`{"entities":[{"text":"Steve Jobs","label":"PERSON","confidence":0.95},
		              {"text":"Apple","label":"ORG","confidence":0.9}]}`,
	}}
	got, err := LLM{Model: m}.Entities(context.Background(), "Steve Jobs founded Apple.")
	if err != nil {
		t.Fatalf("Entities: %v", err)
	}

	want := []Entity{
		{Text: jobs, Label: "PERSON", End: 10, Score: 0.95, Meta: map[string]any{"by": "llm"}},
		{Text: "Apple", Label: "ORG", End: 5, Score: 0.9, Meta: map[string]any{"by": "llm"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("entities = %+v, want %+v", got, want)
	}
	if len(m.kinds) != 1 || m.kinds[0] != Ents {
		t.Errorf("asked for %v, want %v", m.kinds, Ents)
	}
	if !strings.Contains(m.asked[0], "Steve Jobs founded Apple.") {
		t.Error("the prompt did not carry the text")
	}
	if !strings.Contains(m.asked[0], "PERSON (People, names, roles)") {
		t.Error("the prompt did not carry the default entity types")
	}
}

func TestLLMEntitiesPrefersTheTypesItIsGiven(t *testing.T) {
	m := &say{reply: []string{`{"entities":[]}`}}
	if _, err := (LLM{Model: m, Kinds: []string{"DRUG", "DISEASE"}}).
		Entities(context.Background(), "Aspirin treats headaches."); err != nil {
		t.Fatalf("Entities: %v", err)
	}
	if !strings.Contains(m.asked[0], "Preferred entity types: DRUG, DISEASE.") {
		t.Errorf("prompt does not name the caller's types:\n%s", m.asked[0])
	}
	if strings.Contains(m.asked[0], "PERSON (People, names, roles)") {
		t.Error("prompt still carries the default types as well")
	}
}

func TestLLMEntitiesFaults(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("upstream is down")

	for _, c := range []struct {
		name string
		llm  LLM
		text string
		is   error
		says string
	}{
		{"no text", LLM{Model: &say{}}, "  ", ErrInvalid, "empty text"},
		{"no model", LLM{}, "some text", nil, "no model"},
		{"the model failed", LLM{Model: &say{err: boom}}, "some text", boom, ""},
		{"the reply held no JSON", LLM{Model: &say{reply: []string{"I refuse."}}}, "some text", ErrParse, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := c.llm.Entities(ctx, c.text)
			if err == nil {
				t.Fatal("no error")
			}
			if c.is != nil && !errors.Is(err, c.is) {
				t.Errorf("err = %v, want it to wrap %v", err, c.is)
			}
			if c.says != "" && !strings.Contains(err.Error(), c.says) {
				t.Errorf("err = %v, want it to mention %q", err, c.says)
			}
		})
	}
}

func TestLLMEntitiesRefusedByTheSchema(t *testing.T) {
	// The schema is built from PascalCase concepts, so an extractor answering
	// in the NER convention leaves the vocabulary and the whole pass is
	// refused rather than half-kept.
	m := &say{reply: []string{`{"entities":[{"text":"Alice","label":"PERSON","confidence":1}]}`}}
	s := NewSchema(ont)
	_, err := LLM{Model: m, Schema: &s}.Entities(context.Background(), "Alice works at Acme.")
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
	if !strings.Contains(err.Error(), "PERSON") {
		t.Errorf("err = %v, want the offending label named", err)
	}
}

// --- relations -------------------------------------------------------------

func TestLLMRelations(t *testing.T) {
	m := &say{reply: []string{founders}}
	got, err := LLM{Model: m}.Relations(context.Background(), appleTxt, []Entity{apple, jobsEnt})
	if err != nil {
		t.Fatalf("Relations: %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("relations = %d, want all three co-founders", len(got))
	}
	for _, r := range got {
		if r.Predicate != "founded_by" || r.Score < 0.9 {
			t.Errorf("relation = %+v, want the stated predicate and confidence kept", r)
		}
		if r.Object.Text != "Apple Inc." || r.Object.Label != "ORG" {
			t.Errorf("object = %+v, want the entity already found", r.Object)
		}
		if r.Context != appleTxt {
			t.Error("relation carries no context, so nothing can check it later")
		}
	}

	// An endpoint the entity pass found keeps its label; one it missed is
	// kept as UNKNOWN rather than dropped, because a co-founder the entity
	// pass missed is still a co-founder.
	if got[0].Subject.Label != "PERSON" || got[0].Subject.Meta["loose"] == true {
		t.Errorf("Steve Jobs = %+v, want the known entity", got[0].Subject)
	}
	for _, r := range got[1:] {
		if r.Subject.Label != "UNKNOWN" || r.Subject.Meta["loose"] != true || r.Subject.Score != 0 {
			t.Errorf("%q = %+v, want an unscored UNKNOWN entity marked loose", r.Subject.Text, r.Subject)
		}
	}
}

func TestLLMRelationsWithNothingKnown(t *testing.T) {
	m := &say{reply: []string{founders}}
	got, err := LLM{Model: m}.Relations(context.Background(), appleTxt, []Entity{apple})
	if err != nil {
		t.Fatalf("Relations: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("relations = %d, want three", len(got))
	}
	for _, r := range got {
		if r.Subject.Label != "UNKNOWN" {
			t.Errorf("%q = %q, want UNKNOWN", r.Subject.Text, r.Subject.Label)
		}
		if r.Subject.End != len(r.Subject.Text) {
			t.Errorf("%q spans [%d,%d), want its own length", r.Subject.Text, r.Subject.Start, r.Subject.End)
		}
	}
}

func TestLLMRelationsSkipsTornAnswers(t *testing.T) {
	m := &say{reply: []string{`{"relations":[
	  {"subject":"","predicate":"founded_by","object":"Apple Inc."},
	  {"subject":"Steve Jobs","predicate":"founded_by","object":""},
	  {"subject":"Steve Jobs","predicate":"founded_by","object":"Apple Inc."}]}`}}
	got, err := LLM{Model: m}.Relations(context.Background(), appleTxt, []Entity{apple, jobsEnt})
	if err != nil {
		t.Fatalf("Relations: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("relations = %+v, want only the one with both endpoints", got)
	}
}

func TestLLMRelationsEmptyAnswer(t *testing.T) {
	m := &say{reply: []string{`{"relations":[]}`}}
	got, err := LLM{Model: m}.Relations(context.Background(), appleTxt, []Entity{apple})
	if err != nil || len(got) != 0 {
		t.Errorf("Relations = %+v, %v; want none and no error: the model said there was nothing", got, err)
	}
}

func TestLLMRelationsNeedSomethingToRelate(t *testing.T) {
	for _, c := range []struct {
		name  string
		text  string
		known []Entity
		says  string
	}{
		{"no text", "   ", []Entity{apple}, "empty text"},
		{"no entities", appleTxt, nil, "no entities to relate"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := LLM{Model: &say{}}.Relations(context.Background(), c.text, c.known)
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), c.says) {
				t.Errorf("err = %v, want ErrInvalid mentioning %q", err, c.says)
			}
		})
	}
}

func TestLLMRelationsPrompt(t *testing.T) {
	m := &say{reply: []string{`{"relations":[]}`}}
	if _, err := (LLM{Model: m, Preds: []string{"founded_by"}}).
		Relations(context.Background(), appleTxt, []Entity{apple, jobsEnt}); err != nil {
		t.Fatalf("Relations: %v", err)
	}
	for _, want := range []string{
		"Apple Inc. (ORG)",
		"Steve Jobs (PERSON)",
		"Preferred relation types: founded_by.",
		appleTxt,
	} {
		if !strings.Contains(m.asked[0], want) {
			t.Errorf("prompt does not carry %q:\n%s", want, m.asked[0])
		}
	}
	if len(m.kinds) != 1 || m.kinds[0] != Rels {
		t.Errorf("asked for %v, want %v", m.kinds, Rels)
	}
}

func TestLLMRelationsPromptRoster(t *testing.T) {
	// A prompt that spends its budget listing entities the chunk never names
	// buys nothing, so the roster is capped at the longest names.
	m := &say{reply: []string{`{"relations":[]}`}}
	if _, err := (LLM{Model: m, Cap: 1}).
		Relations(context.Background(), appleTxt, []Entity{apple, jobsEnt}); err != nil {
		t.Fatalf("Relations: %v", err)
	}
	if strings.Contains(m.asked[0], "Steve Jobs (PERSON)") {
		t.Errorf("roster exceeded the cap:\n%s", m.asked[0])
	}
	if !strings.Contains(m.asked[0], "Apple Inc. (ORG)") {
		t.Errorf("roster dropped the longest name:\n%s", m.asked[0])
	}
}

func TestLLMRelationsWhen(t *testing.T) {
	m := &say{reply: []string{`{"relations":[{"subject":"Apple Inc.","predicate":"acquired",
	  "object":"Beats","confidence":0.97,"valid_from":"2014-05-01","valid_until":null,
	  "temporal_confidence":0.9,"temporal_source_text":"May 2014"}]}`}}

	got, err := LLM{Model: m, When: true}.
		Relations(context.Background(), "Apple Inc. acquired Beats in May 2014.", []Entity{apple})
	if err != nil {
		t.Fatalf("Relations: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("relations = %+v, want one", got)
	}
	want := map[string]any{
		"by": "llm", "from": "2014-05-01", "until": "", "when": 0.9, "cite": "May 2014",
	}
	if !reflect.DeepEqual(got[0].Meta, want) {
		t.Errorf("Meta = %+v, want %+v", got[0].Meta, want)
	}
	if !strings.Contains(m.asked[0], "temporal_confidence") {
		t.Error("the temporal prompt was not used")
	}
}

func TestLLMRelationsWithoutWhenAsksNothingAboutIt(t *testing.T) {
	m := &say{reply: []string{`{"relations":[]}`}}
	if _, err := (LLM{Model: m}).Relations(context.Background(), appleTxt, []Entity{apple}); err != nil {
		t.Fatalf("Relations: %v", err)
	}
	if strings.Contains(m.asked[0], "temporal_confidence") {
		t.Error("the temporal prompt was used for a caller that did not ask for time")
	}
}

// --- triples ---------------------------------------------------------------

func TestLLMTriples(t *testing.T) {
	c := semantic.Chunk{DocID: "doc-1", Index: 3, Text: "Apple was founded by Steve Jobs."}
	m := &say{reply: []string{`{"triplets":[
	  {"subject":"Apple","predicate":"founded_by","object":"Steve Jobs","confidence":0.95},
	  {"subject":"Apple","object":"Cupertino"},
	  {"subject":"","predicate":"x","object":"y"}]}`}}

	got, err := LLM{Model: m}.Triples(context.Background(), c)
	if err != nil {
		t.Fatalf("Triples: %v", err)
	}
	want := []semantic.Triple{
		{Subject: "Apple", Predicate: "founded_by", Object: jobs, From: c, Score: 0.95},
		{Subject: "Apple", Predicate: "related_to", Object: "Cupertino", From: c},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("triples = %+v, want %+v", got, want)
	}
	if len(m.kinds) != 1 || m.kinds[0] != Tris {
		t.Errorf("asked for %v, want %v", m.kinds, Tris)
	}
}

func TestLLMTriplesNeedsText(t *testing.T) {
	_, err := LLM{Model: &say{}}.Triples(context.Background(), semantic.Chunk{})
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("err = %v, want ErrInvalid", err)
	}
}

// --- both passes -----------------------------------------------------------

func TestLLMFind(t *testing.T) {
	m := &say{reply: []string{
		`{"entities":[{"text":"Steve Jobs","label":"PERSON","confidence":0.95},
		              {"text":"Apple Inc.","label":"ORG","confidence":0.95}]}`,
		founders,
	}}
	got, err := LLM{Model: m}.Find(context.Background(), appleTxt)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got.Entities) != 2 || len(got.Relations) != 3 {
		t.Errorf("Find = %d entities, %d relations; want 2 and 3", len(got.Entities), len(got.Relations))
	}
	if len(m.asked) != 2 {
		t.Errorf("asked %d times, want two: entities, then the relations between them", len(m.asked))
	}
}

func TestLLMFindStopsWhenThereAreNoEntities(t *testing.T) {
	m := &say{reply: []string{`{"entities":[]}`}}
	got, err := LLM{Model: m}.Find(context.Background(), "Nothing here.")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got.Entities) != 0 || len(got.Relations) != 0 {
		t.Errorf("Find = %+v, want nothing", got)
	}
	if len(m.asked) != 1 {
		t.Errorf("asked %d times, want one: there is nothing to relate", len(m.asked))
	}
}

func TestLLMExtract(t *testing.T) {
	c := semantic.Chunk{DocID: "doc-1", Index: 1, Text: appleTxt}
	m := &say{reply: []string{
		`{"entities":[{"text":"Apple Inc.","label":"ORG","confidence":0.95}]}`,
		`{"relations":[{"subject":"Steve Jobs","predicate":"founded_by","object":"Apple Inc.","confidence":0.95}]}`,
	}}
	got, err := LLM{Model: m}.Extract(context.Background(), c)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	want := []semantic.Triple{{
		Subject: jobs, Predicate: "founded_by", Object: "Apple Inc.", From: c, Score: 0.95,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("triples = %+v, want %+v", got, want)
	}
}

func TestLoose(t *testing.T) {
	// The endpoints no entity accounts for are what a graph builder must
	// promote to nodes of their own, or the edge dangles.
	tri := semantic.Triple{Subject: "Steve Wozniak", Predicate: "founded_by", Object: "Apple Inc."}
	if got := Loose(tri, []Entity{apple, jobsEnt}, 0); !reflect.DeepEqual(got, []string{"Steve Wozniak"}) {
		t.Errorf("Loose = %v, want [Steve Wozniak]", got)
	}
	known := []Entity{apple, jobsEnt, {Text: "Steve Wozniak", Label: "PERSON", End: 13, Score: 1}}
	if got := Loose(tri, known, 0); got != nil {
		t.Errorf("Loose = %v, want none: both endpoints are accounted for", got)
	}
}

func TestKindSchema(t *testing.T) {
	// The JSON Schema an adapter may hand its provider must be the schema of
	// the reply that Kind's prompt demands.
	for _, k := range []Kind{Ents, Rels, Tris} {
		s := k.JSON()
		if !json.Valid([]byte(s)) {
			t.Errorf("%s: schema is not valid JSON: %s", k, s)
		}
		if !strings.Contains(s, string(k)) {
			t.Errorf("%s: schema does not name the key the reply must use: %s", k, s)
		}
	}
	if got := Kind("nonsense").JSON(); got != "" {
		t.Errorf("Kind(nonsense).JSON() = %q, want empty", got)
	}
}

func TestLLMTriplesPrompt(t *testing.T) {
	m := &say{reply: []string{`{"triplets":[]}`}}
	c := semantic.Chunk{Text: "Aspirin is a drug."}

	if _, err := (LLM{Model: m}).Triples(context.Background(), c); err != nil {
		t.Fatalf("Triples: %v", err)
	}
	if !strings.Contains(m.asked[0], "Common predicates include: is_a") {
		t.Errorf("prompt does not carry the default predicates:\n%s", m.asked[0])
	}

	m = &say{reply: []string{`{"triplets":[]}`}}
	if _, err := (LLM{Model: m, Preds: []string{"is_a", "treats"}}).Triples(context.Background(), c); err != nil {
		t.Fatalf("Triples: %v", err)
	}
	if !strings.Contains(m.asked[0], "Preferred triplet predicates: is_a, treats.") {
		t.Errorf("prompt does not name the caller's predicates:\n%s", m.asked[0])
	}
}

func TestLLMFindKeepsWhatItGot(t *testing.T) {
	// The entities are worth returning even when the relation pass fails:
	// the caller can retry the second half or settle for the first.
	m := &say{reply: []string{
		`{"entities":[{"text":"Apple Inc.","label":"ORG","confidence":0.95}]}`,
		"I refuse.",
	}}
	got, err := LLM{Model: m}.Find(context.Background(), appleTxt)
	if !errors.Is(err, ErrParse) {
		t.Fatalf("err = %v, want ErrParse", err)
	}
	if len(got.Entities) != 1 || got.Relations != nil {
		t.Errorf("Find = %+v, want the entities it did get", got)
	}
}

func TestItemsRejectsAnItemItCannotRead(t *testing.T) {
	if _, err := items[Link]([]byte(`{"subject":42}`), string(Rels)); !errors.Is(err, ErrParse) {
		t.Errorf("err = %v, want ErrParse", err)
	}
	if _, err := items[Link]([]byte(`{"relations":"not a list"}`), string(Rels)); err != nil {
		t.Errorf("err = %v, want the wrong-shaped key ignored rather than fatal", err)
	}
	if _, err := items[Entity]([]byte(`[1,2,3]`), string(Ents)); !errors.Is(err, ErrParse) {
		t.Errorf("err = %v, want ErrParse", err)
	}
}
