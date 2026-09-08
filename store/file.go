package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// File keeps a store in one file on disk. It answers every question Mem
// does, from memory, and owes its durability to a log: each write is
// appended as one JSON record and flushed before the call returns, so
// reopening the file replays exactly what was acknowledged.
//
// A process that dies mid-write leaves a partial last line. A record counts
// only if it is terminated and decodes, so the partial line is discarded on
// the next open and the file is truncated back to the last whole record —
// the write that never returned never happened.
//
// The log grows with every write. Compact folds it into a single snapshot
// record, written beside the file and renamed over it, so a crash during
// compaction leaves the old log intact.
type File struct {
	mu   sync.Mutex
	mem  Mem
	f    *os.File
	path string
	// end is the offset of the last whole record, which is where the next
	// one is appended.
	end int64
}

// File answers all three questions, like the store it keeps.
var (
	_ Vector = (*File)(nil)
	_ Graph  = (*File)(nil)
	_ Triple = (*File)(nil)
)

// Open opens the store at path, creating it if it is not there, and replays
// what was written.
func Open(path string) (*File, error) {
	if path == "" {
		return nil, fmt.Errorf("store: open: %w", os.ErrInvalid)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	s := &File{f: f, path: path}
	if err := s.replay(); err != nil {
		f.Close()
		return nil, err
	}
	return s, nil
}

// Close flushes and closes the file.
func (s *File) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return nil
	}
	err := s.f.Close()
	s.f = nil
	if err != nil {
		return fmt.Errorf("store: close %s: %w", s.path, err)
	}
	return nil
}

// Name is the path the store is kept at, as os.File reports its own.
func (s *File) Name() string { return s.path }

// log is one entry in the write-ahead log. One shape carries every kind of
// write, because a second shape would mean a second way to read the file.
type log struct {
	Op    string         `json:"op"`
	ID    string         `json:"id,omitempty"`
	IDs   []string       `json:"ids,omitempty"`
	Vec   []float32      `json:"vec,omitempty"`
	Meta  map[string]any `json:"meta,omitempty"`
	From  string         `json:"from,omitempty"`
	To    string         `json:"to,omitempty"`
	Label string         `json:"label,omitempty"`
	S     string         `json:"s,omitempty"`
	P     string         `json:"p,omitempty"`
	O     string         `json:"o,omitempty"`
	Snap  *Snap          `json:"snap,omitempty"`
}

// replay rebuilds the store from the log and truncates a partial tail.
func (s *File) replay() error {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return fmt.Errorf("store: read %s: %w", s.path, err)
	}
	var end int64
	for rest := b; len(rest) > 0; {
		i := bytes.IndexByte(rest, '\n')
		if i < 0 {
			break // a write that did not finish
		}
		line, next := rest[:i], rest[i+1:]
		var e log
		if err := json.Unmarshal(line, &e); err != nil {
			break // a record that did not survive
		}
		if err := s.apply(e); err != nil {
			return fmt.Errorf("store: replay %s: %w", s.path, err)
		}
		end += int64(i) + 1
		rest = next
	}
	if end != int64(len(b)) {
		if err := s.f.Truncate(end); err != nil {
			return fmt.Errorf("store: truncate %s: %w", s.path, err)
		}
	}
	s.end = end
	return nil
}

// apply replays one record into memory. Replay is not a write, so it takes
// no context of its own.
func (s *File) apply(e log) error {
	ctx := context.Background()
	switch e.Op {
	case "snap":
		if e.Snap == nil {
			return nil
		}
		return s.mem.Load(ctx, *e.Snap)
	case "rank":
		s.mem.Rank(Metric(e.Label))
		return nil
	case "put":
		return s.mem.Put(ctx, e.ID, e.Vec, e.Meta)
	case "drop":
		return s.mem.Drop(ctx, e.IDs...)
	case "node":
		return s.mem.Node(ctx, e.ID, e.Meta)
	case "edge":
		return s.mem.Edge(ctx, e.From, e.To, e.Label)
	case "cut":
		return s.mem.Cut(ctx, e.ID)
	case "unlink":
		return s.mem.Unlink(ctx, e.From, e.To, e.Label)
	case "assert":
		return s.mem.Assert(ctx, e.S, e.P, e.O)
	case "retract":
		return s.mem.Retract(ctx, e.S, e.P, e.O)
	}
	return fmt.Errorf("unknown record %q", e.Op)
}

// write appends one record and flushes it, then applies it. The order is
// what makes the file the record of truth: a write that is not on disk is
// not in memory either.
func (s *File) write(e log) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return fmt.Errorf("store: write %s: %w", s.path, os.ErrClosed)
	}
	b, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("store: encode %s: %w", e.Op, err)
	}
	b = append(b, '\n')
	n, err := s.f.WriteAt(b, s.end)
	if err != nil {
		s.f.Truncate(s.end)
		return fmt.Errorf("store: append %s: %w", s.path, err)
	}
	if err := s.f.Sync(); err != nil {
		s.f.Truncate(s.end)
		return fmt.Errorf("store: sync %s: %w", s.path, err)
	}
	s.end += int64(n)
	return s.apply(e)
}

// Compact rewrites the file as one snapshot, discarding a log that has
// grown longer than the state it describes. The snapshot is written beside
// the file and renamed over it, so an interrupted compaction leaves the
// store as it was.
func (s *File) Compact() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return fmt.Errorf("store: compact %s: %w", s.path, os.ErrClosed)
	}
	snap := s.mem.Dump()
	b, err := json.Marshal(log{Op: "snap", Snap: &snap})
	if err != nil {
		return fmt.Errorf("store: encode snapshot: %w", err)
	}
	b = append(b, '\n')

	tmp := s.path + ".new"
	f, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("store: compact %s: %w", s.path, err)
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("store: compact %s: %w", s.path, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("store: compact %s: %w", s.path, err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("store: compact %s: %w", s.path, err)
	}
	if dir, err := os.Open(filepath.Dir(s.path)); err == nil {
		dir.Sync()
		dir.Close()
	}
	s.f.Close()
	s.f = f
	s.end = int64(len(b))
	return nil
}

// Rank sets how nearness is measured and records the choice, so a store
// reopened later ranks the way it was told to.
func (s *File) Rank(v Metric) error { return s.write(log{Op: "rank", Label: string(v)}) }

// Metric reports how nearness is measured. See Mem.Metric.
func (s *File) Metric() Metric { return s.mem.Metric() }

// Put writes a vector under an id. See Mem.Put.
func (s *File) Put(ctx context.Context, id string, v []float32, meta map[string]any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if id == "" {
		return ErrID
	}
	return s.write(log{Op: "put", ID: id, Vec: v, Meta: meta})
}

// Drop removes vectors. See Mem.Drop.
func (s *File) Drop(ctx context.Context, ids ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	return s.write(log{Op: "drop", IDs: ids})
}

// Node writes a node and its properties. See Mem.Node.
func (s *File) Node(ctx context.Context, id string, props map[string]any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if id == "" {
		return ErrID
	}
	return s.write(log{Op: "node", ID: id, Meta: props})
}

// Edge links two nodes under a label. See Mem.Edge.
func (s *File) Edge(ctx context.Context, from, to, label string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if from == "" || to == "" {
		return ErrID
	}
	return s.write(log{Op: "edge", From: from, To: to, Label: label})
}

// Cut removes a node and every edge that touched it. See Mem.Cut.
func (s *File) Cut(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if id == "" {
		return ErrID
	}
	return s.write(log{Op: "cut", ID: id})
}

// Unlink removes the edges that match a pattern. See Mem.Unlink.
func (s *File) Unlink(ctx context.Context, from, to, label string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.write(log{Op: "unlink", From: from, To: to, Label: label})
}

// Assert records one triple. See Mem.Assert.
func (s *File) Assert(ctx context.Context, sub, pred, obj string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if sub == "" || pred == "" || obj == "" {
		return ErrTerm
	}
	return s.write(log{Op: "assert", S: sub, P: pred, O: obj})
}

// Retract removes the triples that match a pattern. See Mem.Retract.
func (s *File) Retract(ctx context.Context, sub, pred, obj string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.write(log{Op: "retract", S: sub, P: pred, O: obj})
}

// Reads go straight to memory: the file is how the store survives, not how
// it is queried.

// Near returns the k nearest vectors. See Mem.Near.
func (s *File) Near(ctx context.Context, v []float32, k int) ([]Match, error) {
	return s.mem.Near(ctx, v, k)
}

// Search returns the matches a query admits. See Mem.Search.
func (s *File) Search(ctx context.Context, q Query) ([]Match, error) { return s.mem.Search(ctx, q) }

// Get returns a vector and its metadata. See Mem.Get.
func (s *File) Get(id string) ([]float32, map[string]any, bool) { return s.mem.Get(id) }

// Scan pages through the stored vectors. See Mem.Scan.
func (s *File) Scan(offset, limit int) []Match { return s.mem.Scan(offset, limit) }

// Props returns a node's properties. See Mem.Props.
func (s *File) Props(id string) (map[string]any, bool) { return s.mem.Props(id) }

// Nodes lists every node. See Mem.Nodes.
func (s *File) Nodes() []string { return s.mem.Nodes() }

// Out lists the nodes this one points at. See Mem.Out.
func (s *File) Out(ctx context.Context, id string) ([]string, error) { return s.mem.Out(ctx, id) }

// In lists the nodes that point at this one. See Mem.In.
func (s *File) In(ctx context.Context, id string) ([]string, error) { return s.mem.In(ctx, id) }

// Edges lists the links that match a pattern. See Mem.Edges.
func (s *File) Edges(from, to, label string) []Link { return s.mem.Edges(from, to, label) }

// Path returns a shortest path between two nodes. See Mem.Path.
func (s *File) Path(ctx context.Context, from, to string) ([]string, error) {
	return s.mem.Path(ctx, from, to)
}

// Reach lists the nodes within hops steps of one. See Mem.Reach.
func (s *File) Reach(ctx context.Context, id string, hops int) ([]string, error) {
	return s.mem.Reach(ctx, id, hops)
}

// Degree counts the edges into and out of a node. See Mem.Degree.
func (s *File) Degree(id string) (in, out int) { return s.mem.Degree(id) }

// Components groups the nodes into connected components. See Mem.Components.
func (s *File) Components() [][]string { return s.mem.Components() }

// Match returns the triples that fit a pattern. See Mem.Match.
func (s *File) Match(ctx context.Context, sub, pred, obj string) ([][3]string, error) {
	return s.mem.Match(ctx, sub, pred, obj)
}

// Stat counts what the store holds. See Mem.Stat.
func (s *File) Stat() Stat { return s.mem.Stat() }

// Dump returns everything the store holds. See Mem.Dump.
func (s *File) Dump() Snap { return s.mem.Dump() }
