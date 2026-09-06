// Package decision is the layer the graph exists for: what an agent knew,
// what it chose, under which rule, and what followed. It is named for the
// act rather than the data because the data is already the graph.
package decision

import (
	"context"
	"time"
)

// Record is one decision an agent made and the evidence it stood on.
type Record struct {
	ID      string
	Agent   string
	Choice  string
	Because []string // Triple or Entity ids that supported the choice
	Rule    string   // the Policy that allowed it
	At      time.Time
}

// Policy says whether a choice is allowed. It answers before the act,
// so a refusal is a decision with a reason rather than an error.
type Policy interface {
	Allow(ctx context.Context, agent, choice string) (bool, string)
}

// Recorder keeps decisions and can read them back by agent.
type Recorder interface {
	Record(ctx context.Context, r Record) error
	By(ctx context.Context, agent string) ([]Record, error)
}

// Cause walks the chain: which earlier decisions this one rests on.
type Cause interface {
	Chain(ctx context.Context, id string) ([]Record, error)
}
