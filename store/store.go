// Package store holds the three ways a graph is kept: by similarity, by
// edge, and by assertion. They are separate interfaces because they answer
// separate questions, and a deployment may back them with one engine or three.
package store

import "context"

// Match is a neighbour and how near it is.
type Match struct {
	ID    string
	Score float64
	Meta  map[string]any
}

// Vector answers "what is like this".
type Vector interface {
	Put(ctx context.Context, id string, v []float32, meta map[string]any) error
	Near(ctx context.Context, v []float32, k int) ([]Match, error)
}

// Graph answers "what is connected to this".
type Graph interface {
	Node(ctx context.Context, id string, props map[string]any) error
	Edge(ctx context.Context, from, to, label string) error
	Out(ctx context.Context, id string) ([]string, error)
}

// Triple answers "what was asserted".
type Triple interface {
	Assert(ctx context.Context, s, p, o string) error
	Match(ctx context.Context, s, p, o string) ([][3]string, error)
}
