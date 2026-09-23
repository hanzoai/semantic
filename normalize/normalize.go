// Package normalize is the cleanup between parsing a document and splitting
// it: the same sentence written with a non-breaking space, a curly apostrophe
// or a decomposed accent should reach the extractor as one string, not three.
//
// Every transform is a [Step] — a function from string to string — and steps
// compose into a [Chain], so a caller assembles the pipeline the corpus needs
// instead of arguing with an options struct. [Text] and [Clean] are the two
// chains worth having by default.
//
// The package also reads the values that end up as graph nodes: dates
// ([Date]), numbers ([Number]), quantities ([Quantity]) and sums of money
// ([Money]). Each is a [Kind]: it reads text under an [Anchor] — what the
// source says about how to read it — into a typed value, and writes that value
// in one canonical form. A reading returns an error rather than a string,
// because a date that failed to parse is not a date, and text with two
// readings is refused with [ErrAmbiguous] rather than read one way.
package normalize

// Step is one text transform. Every exported function of the form
// func(string) string in this package is a Step and can be used as one.
type Step func(string) string

// Chain applies steps in order. The zero Chain returns its input unchanged.
type Chain []Step

// Run applies every step in the chain to s.
func (c Chain) Run(s string) string {
	for _, step := range c {
		s = step(s)
	}
	return s
}

var (
	prose = Chain{NFC, Lines, Control, Quotes, Space}
	plain = Chain{Tags, NFC, Control, Quotes, Flat}
)

// Text is the default cleanup for prose that keeps its shape: compose
// characters, settle line endings, drop control codes, fold typographic
// punctuation to ASCII and regularize spacing. Paragraph breaks survive.
func Text(s string) string { return prose.Run(s) }

// Clean is the default cleanup for text headed into a single field or a
// similarity key: everything Text does, plus HTML removal, and the line
// structure flattened to single spaces.
func Clean(s string) string { return plain.Run(s) }
