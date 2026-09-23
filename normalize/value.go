package normalize

import (
	"errors"
	"fmt"
	"time"
)

// Errors the typed readers report, wrapped with the kind, the text and the
// reason. Test for them with errors.Is.
var (
	// ErrAmbiguous means the text has more than one reading and the Anchor
	// does not say which: a day and a month in an order it does not give, a
	// time with no zone, a separator that may group digits or mark a
	// decimal, a currency sign several currencies use, a relative date with
	// no date to count from.
	ErrAmbiguous = errors.New("ambiguous")
	// ErrSyntax means the text is not a value of the kind at all.
	ErrSyntax = errors.New("not readable")
)

// refuse is the error for text a kind will not read.
func refuse(kind, s string, err error, why string) error {
	return fmt.Errorf("normalize: %s %q: %w: %s", kind, s, err, why)
}

// Anchor is what a source says about how to read the values in it: the
// context a date or a number needs and the text alone does not carry. The
// zero Anchor assumes nothing, and anything whose reading depends on it is
// refused rather than read one way by default.
type Anchor struct {
	// At is the source's own date — when the letter was written, the page
	// published, the message sent — and relative dates such as "yesterday"
	// are counted from it, on its calendar. It is never the time of reading:
	// a document says "yesterday" about the day before it was written, and
	// nothing here calls time.Now. Zero refuses a relative date.
	At time.Time
	// Zone places a time written without an offset. Nil refuses one.
	Zone *time.Location
	// Order is the order of day and month in an all-numeric date. Zero
	// refuses one that does not begin with its year.
	Order Order
	// Point is the decimal mark, '.' or ','; the other is then a digit group
	// separator. Zero reads a mark only where it can be one thing, and
	// refuses "1,234" and "1.234", which can be either.
	Point rune
}

// Order is the order of the day and the month in an all-numeric date.
type Order int

// The two orders in use. A date that begins with a four-digit year is read
// year, month, day whatever the Order.
const (
	DMY Order = iota + 1 // 31/12/2023
	MDY                  // 12/31/2023
)

// Kind is one kind of value: how to read it from text under an Anchor, and how
// to write it in its one canonical form.
//
// Canon writes text that Parse reads back under Anchor{Point: '.'} to the same
// value — ISO 8601 dates with their offsets, numbers with a point and no
// grouping — so Canon∘Parse settles every spelling of a value on one string,
// and applying it again changes nothing.
type Kind[T any] struct {
	Name  string
	Parse func(Anchor, string) (T, error)
	Canon func(T) string
}

// Datatype is a Kind seen without its Go type, for code that holds several
// kinds side by side — a schema whose properties each name one — and needs
// only the canonical text.
type Datatype interface {
	// String is the kind's name.
	String() string
	// Norm reads s under a and writes it canonically.
	Norm(a Anchor, s string) (string, error)
}

// String returns the kind's name.
func (k Kind[T]) String() string { return k.Name }

// Norm reads s under a and writes the value in its canonical form.
func (k Kind[T]) Norm(a Anchor, s string) (string, error) {
	v, err := k.Parse(a, s)
	if err != nil {
		return "", err
	}
	return k.Canon(v), nil
}

// Every kind this package defines is a Datatype.
var (
	_ Datatype = Date
	_ Datatype = Number
	_ Datatype = Quantity
	_ Datatype = Money
)
