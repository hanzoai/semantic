package extract

import (
	"encoding/json"
	"strconv"
	"strings"
)

// What a model actually sends, as opposed to what this package works with.
// Providers spell the same field several ways — a label is a "type", a
// subject a "source", a confidence a quoted number — so the reply is decoded
// into one permissive form and the aliases are resolved in one place. Entity
// and Link stay clean of it.
type wire struct {
	Text  string `json:"text"`
	Value string `json:"value"`
	Span  string `json:"span"`
	Label string `json:"label"`
	Type  string `json:"type"`

	Start     int `json:"start"`
	StartChar int `json:"start_char"`
	End       int `json:"end"`
	EndChar   int `json:"end_char"`

	Subject   string `json:"subject"`
	Source    string `json:"source"`
	Object    string `json:"object"`
	Target    string `json:"target"`
	Predicate string `json:"predicate"`

	Confidence number `json:"confidence"`
	Score      number `json:"score"`

	From  string `json:"valid_from"`
	Until string `json:"valid_until"`
	When  number `json:"temporal_confidence"`
	Cite  string `json:"temporal_source_text"`

	Meta     map[string]any `json:"meta"`
	Metadata map[string]any `json:"metadata"`
}

// number is a confidence as a provider sent it: a JSON number, a number in
// quotes, or null. Anything else reads as absent rather than as an error,
// because a model that answered the question badly still answered it.
type number struct {
	Value float64
	Set   bool
}

// UnmarshalJSON reads a number written as a number, as a string, or as null.
func (n *number) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch t := v.(type) {
	case float64:
		n.Value, n.Set = clamp(t), true
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(t), 64); err == nil {
			n.Value, n.Set = clamp(f), true
		}
	}
	return nil
}

// stated is the confidence a model reply carries under either spelling, and
// zero when it carries none: an item the model did not score is unscored, not
// given a number the model never sent.
func (w wire) stated() float64 {
	if w.Confidence.Set {
		return w.Confidence.Value
	}
	return w.Score.Value
}

// UnmarshalJSON reads an entity as a model returned it. Offsets are optional —
// the prompt asks for text and label, not spans — and an entity that comes
// back without them spans its own text, so End-Start is its length even when
// the position is unknown.
func (e *Entity) UnmarshalJSON(b []byte) error {
	var w wire
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	text := strings.TrimSpace(first(w.Text, w.Value, w.Span))
	start := first(w.Start, w.StartChar)
	end := first(w.End, w.EndChar)
	if end == 0 {
		end = start + len(text)
	}
	meta := w.Meta
	if meta == nil {
		meta = w.Metadata
	}
	*e = Entity{
		Text:  text,
		Label: first(w.Label, w.Type),
		Start: start,
		End:   end,
		Score: w.stated(),
		Meta:  meta,
	}
	return nil
}

// UnmarshalJSON reads a relation as a model stated it, endpoints still named
// by text. The temporal fields are read whether or not they were asked for;
// a model that volunteers them is not punished for it.
func (k *Link) UnmarshalJSON(b []byte) error {
	var w wire
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	*k = Link{
		Subject:   strings.TrimSpace(first(w.Subject, w.Source)),
		Predicate: strings.TrimSpace(first(w.Predicate, w.Label)),
		Object:    strings.TrimSpace(first(w.Object, w.Target)),
		Score:     w.stated(),
		From:      w.From,
		Until:     w.Until,
		When:      w.When.Value,
		Cite:      w.Cite,
	}
	return nil
}

// first returns the first argument that is not the zero value, which is how
// one field is read from the several names providers give it.
func first[T comparable](vs ...T) T {
	var zero T
	for _, v := range vs {
		if v != zero {
			return v
		}
	}
	return zero
}
