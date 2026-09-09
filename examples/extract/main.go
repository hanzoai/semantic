// Command extract turns text into entities, the relations between them, and
// the triples those relations assert. Two extractors fill the same stage:
// Rules needs nothing but the text, and LLM asks a model and does the work
// around the call.
//
// Two checks gate either extractor's output, and they are orthogonal: a Schema
// asks whether the extraction is in the vocabulary, a Floor asks whether it is
// sure enough and structurally sound. A caller that wants both runs both.
//
// The model at the end is a stub that returns exactly what providers actually
// return — a JSON document wrapped in a markdown fence, and one cut off at a
// token limit — so the recovery this package does around a real call is what
// the example exercises. No network, no key.
//
//	go run ./examples/extract
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/hanzoai/semantic"
	"github.com/hanzoai/semantic/extract"
)

const text = "Ada Lovelace works for Babbage Engines. " +
	"Babbage Engines was founded by Charles Babbage in 1843 and is headquartered in Montréal. " +
	"Revenue reached $1,200,000, up 40% on the year."

func main() {
	ctx := context.Background()

	shapes()
	names()
	relations()
	near()
	gates()
	matching()
	model(ctx)
}

// shapes is the zero Rules: built-in patterns over the shape of the text, no
// gazetteer, no configuration. It finds what a regular expression can find and
// says how sure it is.
func shapes() {
	fmt.Println("rules, patterns only")
	for _, e := range (extract.Rules{}).Entities(text) {
		fmt.Printf("  %-9s %-18q %.2f  bytes %d..%d\n", e.Label, e.Text, e.Score, e.Start, e.End)
	}
}

// names adds a gazetteer: surface forms the caller already knows. A gazetteer
// hit outranks a pattern hit over the same span, because the caller stated it
// and the pattern only guessed — which is how Montréal becomes a LOC rather
// than nothing and Babbage Engines an ORG rather than a PERSON.
func names() {
	fmt.Println("\nrules, with a gazetteer")
	for _, e := range gazetteer().Entities(text) {
		fmt.Printf("  %-9s %-18q %.2f\n", e.Label, e.Text, e.Score)
	}
}

// relations reads a verb phrase between two entities the caller already found.
// Requiring both endpoints to be known entities is what keeps a pattern from
// matching a long run of prose that merely ends in the right verb.
func relations() {
	r := gazetteer()
	set := r.Find(text)
	fmt.Printf("\nrelations  %d entities, %d relations\n", len(set.Entities), len(set.Relations))
	for _, rel := range set.Relations {
		fmt.Printf("  %-16s %-12s %-16s %.2f  %q\n",
			rel.Subject.Text, rel.Predicate, rel.Object.Text, rel.Score, clip(rel.Context))
	}

	// One of those is wrong: the located_in family includes a bare
	// "X in Y", which fires on "founded by Charles Babbage in 1843". A rule
	// set that finds everything finds some things that are not there, which
	// is what the schema check below is for.

	// Flattened to pipeline triples, each attributed to the chunk it came
	// from, which is what makes the claim answerable later.
	c := semantic.Chunk{DocID: "notes", Index: 0, Text: text}
	fmt.Println("  as triples:")
	for _, t := range set.Triples(c) {
		fmt.Printf("    %s %s %s (chunk %d of %s)\n", t.Subject, t.Predicate, t.Object, t.From.Index, t.From.DocID)
	}
}

// near is what is left when no pattern fits: two things named a few words
// apart are usually about each other. On its own that is all it claims — the
// predicate is related_to. Given the predicates a caller is looking for, the
// words between the two are matched against them and the best match names the
// relation.
func near() {
	known := gazetteer().Entities(text)
	rels := (extract.Gap{Max: 40}).Relations(text, known)
	fmt.Printf("\ngap  %d pairs within 40 bytes of each other, the first four:\n", len(rels))
	for _, rel := range rels[:4] {
		fmt.Printf("  %-16s %-12s %-16s %.2f\n", rel.Subject.Text, rel.Predicate, rel.Object.Text, rel.Score)
	}

	// Handing it the linking verbs at a Min of 1 is how an attribute is read:
	// the sentence states what it states, and nothing where it used a verb
	// the caller did not name. Proximity is all this measures — it does not
	// stop at a sentence boundary — so it is given one sentence here.
	const line = "Babbage Engines is a corporation."
	attrs := []extract.Entity{
		{Text: "Babbage Engines", Label: "ORG", Start: 0, End: 15, Score: 1},
		{Text: "corporation", Label: "TYPE", Start: 21, End: 32, Score: 1},
	}
	fmt.Printf("  Gap{Preds: []string{\"is\", \"has\"}, Min: 1} over %q:\n", line)
	for _, rel := range (extract.Gap{Preds: []string{"is", "has"}, Min: 1}).Relations(line, attrs) {
		fmt.Printf("    %-16s %-6s %-14s %.2f\n", rel.Subject.Text, rel.Predicate, rel.Object.Text, rel.Score)
	}
}

// gates are the two checks. They ask different questions of the same
// extraction, so running one is not running the other.
func gates() {
	fmt.Println("\nchecks")

	// A schema built from a domain ontology. Names match exactly: a
	// generator that writes classes in PascalCase expects labels in
	// PascalCase, so this schema allows Person and rejects PERSON.
	schema := extract.NewSchema(extract.Ontology{
		Classes: []extract.Class{{Name: "Person"}, {Name: "Organization"}, {Name: "Place"}},
		Properties: []extract.Property{
			{Name: "worksFor", Domain: extract.Names{"Person"}, Range: extract.Names{"Organization"}},
			{Name: "foundedBy", Domain: extract.Names{"Organization"}, Range: extract.Names{"Person"}},
		},
	})
	fmt.Printf("  schema concepts %v, predicates worksFor/foundedBy\n", schema.Concepts)

	person := extract.Entity{Text: "Ada Lovelace", Label: "Person", Score: 0.9}
	org := extract.Entity{Text: "Babbage Engines", Label: "Organization", Score: 0.9}
	place := extract.Entity{Text: "Montréal", Label: "City", Score: 0.4}

	set := extract.Set{
		Entities: []extract.Entity{person, org, place},
		Relations: []extract.Relation{
			{Subject: person, Predicate: "worksFor", Object: org, Score: 0.9},
			{Subject: org, Predicate: "listedOn", Object: place, Score: 0.5},
		},
	}

	rep := schema.Check(set)
	fmt.Printf("  schema  ok=%v score=%.2f unknown=%v\n", rep.OK, rep.Score, rep.Unknown)
	for _, e := range rep.Errs {
		fmt.Printf("          %s\n", e)
	}
	kept := schema.Keep(set)
	fmt.Printf("  keep    %d of %d entities, %d of %d relations\n",
		len(kept.Entities), len(set.Entities), len(kept.Relations), len(set.Relations))

	// The other axis. A Floor knows nothing about the vocabulary; it asks
	// how much of the extraction clears a confidence bar and whether it is
	// structurally sound.
	floor := extract.Floor(0.6)
	rep = floor.Check(set)
	fmt.Printf("  floor   ok=%v score=%.2f counts=%v\n", rep.OK, rep.Score, rep.Counts)
	kept = floor.Keep(set)
	fmt.Printf("  keep    %d entities, %d relations clear 0.60\n", len(kept.Entities), len(kept.Relations))

	// A failed report carries an error that wraps ErrInvalid, so a caller
	// can tell a rejected extraction from a transport failure.
	broken := extract.Set{Entities: []extract.Entity{{Text: "", Label: "Person"}}}
	if err := floor.Check(broken).Err(); errors.Is(err, extract.ErrInvalid) {
		fmt.Printf("  err     %s\n", strings.ReplaceAll(err.Error(), "\n", "; "))
	}
}

// matching is the binding layer: an endpoint a model named in its own words,
// resolved back to an entity already found. The cheap tests run first and stop
// as soon as one is decisive.
func matching() {
	known := gazetteer().Entities(text)
	fmt.Println("\nmatching")
	for _, said := range []string{"Ada Lovelace", "ada lovelace", "Lovelace", "Babbage Engines Inc", "Grace Hopper"} {
		if e, ok := extract.Bind(said, known, 0.8); ok {
			fmt.Printf("  %-20q → %-18q %s\n", said, e.Text, e.Label)
		} else {
			fmt.Printf("  %-20q → no match at 0.80 (best %.2f)\n", said, extract.Similar(said, texts(known)))
		}
	}
	fmt.Printf("  Ratio(%q, %q) = %.2f\n", "Montréal", "Montreal", extract.Ratio("Montréal", "Montreal"))

	// A prompt that spends its budget listing entities the chunk never names
	// buys nothing, so a roster is trimmed to what the text plausibly
	// mentions before it goes into one.
	fmt.Printf("  Narrow(%d known, max 2) → %v\n", len(known), texts(extract.Narrow(text, known, 2)))
}

// model is the LLM path with the network taken out. What this package supplies
// around a call is the prompt, the recovery of JSON from whatever the model
// wrapped it in, the field aliases every provider spells differently, and the
// binding of a named endpoint back to an entity already found.
func model(ctx context.Context) {
	fmt.Println("\nmodel")

	// Two replies of the kind providers actually send. The first is fenced;
	// the second was cut off at a token limit mid-object, and it spells
	// label as "type" and confidence as a quoted number.
	l := extract.LLM{When: true, Model: replies{
		extract.Ents: "Here is the JSON you asked for:\n```json\n" +
			`{"entities":[{"text":"Ada Lovelace","label":"Person","confidence":0.94},` +
			`{"text":"Babbage Engines","type":"Organization","confidence":"0.88"}]}` +
			"\n```",
		extract.Rels: `{"relations":[{"subject":"Ada Lovelace","predicate":"worksFor",` +
			`"object":"Babbage Engines","confidence":0.91,"valid_from":"1843",` +
			`"temporal_confidence":0.8,"temporal_source_text":"joined in 1843"},` +
			`{"source":"Charles Babbage","predicate":"founded",`,
	}}

	ents, err := l.Entities(ctx, text)
	if err != nil {
		log.Fatal(err)
	}
	for _, e := range ents {
		fmt.Printf("  entity   %-18q %-14s %.2f\n", e.Text, e.Label, e.Score)
	}

	// The second relation was cut off mid-object at a token limit. What
	// survives is kept; the temporal fields the model volunteered are read
	// whether or not they were asked for.
	rels, err := l.Relations(ctx, text, ents)
	if err != nil {
		log.Fatal(err)
	}
	for _, r := range rels {
		fmt.Printf("  relation %-16s %-10s %-18s %.2f\n", r.Subject.Text, r.Predicate, r.Object.Text, r.Score)
		fmt.Printf("           held from %v, temporal confidence %v, read from %q\n",
			r.Meta["from"], r.Meta["when"], r.Meta["cite"])
	}

	// The recovery on its own, so it is clear what was done to the reply.
	for _, reply := range []string{
		"```json\n{\"entities\":[{\"text\":\"Ada\",\"label\":\"Person\"},{\"text\":\"Charles",
		"the model refused to answer",
	} {
		doc, err := extract.JSON(reply)
		switch {
		case err == nil:
			fmt.Printf("  JSON  %-58q → %s\n", reply, doc)
		case errors.Is(err, extract.ErrParse):
			fmt.Printf("  JSON  %-58q → %v\n", reply, extract.ErrParse)
		}
	}
}

// replies is a Model that answers from a script. A real adapter sends the
// prompt to a provider; the schema argument is the Kind being asked for, which
// an adapter may turn into whatever structured-output format its API wants.
type replies map[extract.Kind]string

func (r replies) Complete(_ context.Context, _ string, schema any) (string, error) {
	k, _ := schema.(extract.Kind)
	reply, ok := r[k]
	if !ok {
		return "", fmt.Errorf("no scripted reply for %v", k)
	}
	return reply, nil
}

// gazetteer is the reader's prior knowledge: names it will recognise, with
// what each one is.
func gazetteer() extract.Rules {
	return extract.Rules{Names: map[string]string{
		"Ada Lovelace":    "PERSON",
		"Charles Babbage": "PERSON",
		"Babbage Engines": "ORG",
		"Montréal":        "LOC",
	}}
}

func texts(es []extract.Entity) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Text
	}
	return out
}

func clip(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) > 46 {
		return string([]rune(s)[:46]) + "…"
	}
	return s
}
