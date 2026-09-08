package parse

import (
	"context"

	"github.com/hanzoai/semantic"
)

// Text is the plain-text format. It normalizes line endings and leaves the
// content alone, recording no sections: prose has no boundaries the document
// declares, and inventing them here would compete with the splitter's own
// rule for dividing it.
type Text struct{}

// Parse normalizes the document's line endings.
func (Text) Parse(_ context.Context, d semantic.Doc) (semantic.Doc, error) {
	return with(d, lines(d.Text), map[string]any{"format": "text"}), nil
}
