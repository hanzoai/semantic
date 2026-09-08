package store

import (
	"encoding/json"
	"strings"
)

// Op is how a condition compares a metadata field to a value.
type Op string

const (
	Eq  Op = "eq"  // field equals value
	Ne  Op = "ne"  // field differs from value
	Gt  Op = "gt"  // field is greater than value
	Ge  Op = "ge"  // field is greater than or equal to value
	Lt  Op = "lt"  // field is less than value
	Le  Op = "le"  // field is less than or equal to value
	In  Op = "in"  // field is one of the values in a list
	Has Op = "has" // field contains value: a substring, or a member of a list
)

// Cond is one comparison against one metadata field.
type Cond struct {
	Field string
	Op    Op
	Value any
}

// Filter is a conjunction: metadata passes when every condition holds. The
// zero value passes everything, so a query with no filter is not a special
// case.
//
// Conditions are added by the builder methods, each returning the extended
// filter, so a filter reads as the question it asks:
//
//	f := store.Filter{}.Eq("lang", "en").Ge("year", 2020)
type Filter []Cond

// Add appends one condition.
func (f Filter) Add(field string, op Op, v any) Filter {
	return append(f, Cond{Field: field, Op: op, Value: v})
}

// Eq requires the field to equal v.
func (f Filter) Eq(field string, v any) Filter { return f.Add(field, Eq, v) }

// Ne requires the field to be present and differ from v.
func (f Filter) Ne(field string, v any) Filter { return f.Add(field, Ne, v) }

// Gt requires the field to be greater than v.
func (f Filter) Gt(field string, v any) Filter { return f.Add(field, Gt, v) }

// Ge requires the field to be greater than or equal to v.
func (f Filter) Ge(field string, v any) Filter { return f.Add(field, Ge, v) }

// Lt requires the field to be less than v.
func (f Filter) Lt(field string, v any) Filter { return f.Add(field, Lt, v) }

// Le requires the field to be less than or equal to v.
func (f Filter) Le(field string, v any) Filter { return f.Add(field, Le, v) }

// In requires the field to equal one of the values in list.
func (f Filter) In(field string, list ...any) Filter { return f.Add(field, In, list) }

// Has requires the field to contain v: a substring of a string field, or a
// member of a list field.
func (f Filter) Has(field string, v any) Filter { return f.Add(field, Has, v) }

// Match reports whether metadata satisfies every condition. A field the
// metadata does not carry fails, whatever the operator — an absent field is
// not evidence of inequality — so Ne is "present and different", not "not
// equal or missing".
func (f Filter) Match(meta map[string]any) bool {
	for _, c := range f {
		got, ok := meta[c.Field]
		if !ok || !c.holds(got) {
			return false
		}
	}
	return true
}

func (c Cond) holds(got any) bool {
	switch c.Op {
	case Eq:
		return same(got, c.Value)
	case Ne:
		return !same(got, c.Value)
	case Gt, Ge, Lt, Le:
		d, ok := order(got, c.Value)
		if !ok {
			return false
		}
		switch c.Op {
		case Gt:
			return d > 0
		case Ge:
			return d >= 0
		case Lt:
			return d < 0
		}
		return d <= 0
	case In:
		for _, v := range list(c.Value) {
			if same(got, v) {
				return true
			}
		}
		return false
	case Has:
		if s, ok := got.(string); ok {
			want, ok := c.Value.(string)
			return ok && strings.Contains(s, want)
		}
		for _, v := range list(got) {
			if same(v, c.Value) {
				return true
			}
		}
		return false
	}
	return false
}

// same compares two metadata values. Numbers compare as numbers whatever
// their Go type, because a value that has been through JSON comes back as a
// float64 and is still the same value.
func same(a, b any) bool {
	if x, ok := num(a); ok {
		if y, ok := num(b); ok {
			return x == y
		}
		return false
	}
	if x, ok := a.(string); ok {
		y, ok := b.(string)
		return ok && x == y
	}
	if x, ok := a.(bool); ok {
		y, ok := b.(bool)
		return ok && x == y
	}
	xs, ys := list(a), list(b)
	if xs == nil || ys == nil || len(xs) != len(ys) {
		return a == nil && b == nil
	}
	for i := range xs {
		if !same(xs[i], ys[i]) {
			return false
		}
	}
	return true
}

// order compares two values, reporting -1, 0 or 1, and whether they are
// comparable at all. Numbers order numerically and strings lexically;
// anything else has no order.
func order(a, b any) (int, bool) {
	if x, ok := num(a); ok {
		y, ok := num(b)
		if !ok {
			return 0, false
		}
		switch {
		case x < y:
			return -1, true
		case x > y:
			return 1, true
		}
		return 0, true
	}
	if x, ok := a.(string); ok {
		y, ok := b.(string)
		if !ok {
			return 0, false
		}
		return strings.Compare(x, y), true
	}
	return 0, false
}

// num reads any Go numeric value, and a json.Number, as a float64.
func num(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// list reads a value as a sequence, so a []string written by a caller and
// the []any that comes back from JSON are the same list.
func list(v any) []any {
	switch xs := v.(type) {
	case []any:
		return xs
	case []string:
		out := make([]any, len(xs))
		for i, x := range xs {
			out[i] = x
		}
		return out
	case []int:
		out := make([]any, len(xs))
		for i, x := range xs {
			out[i] = x
		}
		return out
	case []float64:
		out := make([]any, len(xs))
		for i, x := range xs {
			out[i] = x
		}
		return out
	}
	return nil
}
