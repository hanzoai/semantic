package ingest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/hanzoai/semantic"
)

// Registry resolves a reference to the reader that can read it, and is
// itself a semantic.Ingester, so a pipeline can take the registry and accept
// whatever the caller points it at.
//
// It is also the seam for sources this package does not implement. A driver
// for object storage, a wiki, a queue or a database registers itself under
// its scheme and is reached by the same call:
//
//	ingest.Default.Set("s3", myBucketReader)
//	docs, err := ingest.Default.Ingest(ctx, "s3://bucket/key")
//
// A file extension works as a key too, so a reader for a binary format takes
// over the paths that name it:
//
//	ingest.Default.Set("parquet", myParquetReader)
//
// The zero Registry is empty and usable; Default holds the readers here.
type Registry struct {
	mu sync.RWMutex
	by map[string]semantic.Ingester
}

// Default resolves the schemes this package implements: file, dir, glob,
// http, https and stdin.
var Default = &Registry{by: map[string]semantic.Ingester{
	"file":  File{},
	"dir":   Dir{},
	"glob":  Glob{},
	"http":  Web{},
	"https": Web{},
	"stdin": Reader{},
}}

// Set registers in under scheme, replacing whatever was there.
func (r *Registry) Set(scheme string, in semantic.Ingester) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.by == nil {
		r.by = map[string]semantic.Ingester{}
	}
	r.by[strings.ToLower(scheme)] = in
}

// Get returns the reader registered under scheme.
func (r *Registry) Get(scheme string) (semantic.Ingester, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	in, ok := r.by[strings.ToLower(scheme)]
	return in, ok
}

// Drop removes the reader registered under scheme.
func (r *Registry) Drop(scheme string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.by, strings.ToLower(scheme))
}

// Schemes lists what is registered, in order.
func (r *Registry) Schemes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.by))
	for s := range r.by {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// Ingest reads whatever ref points at. A URL goes to its scheme's reader; a
// local path goes to a reader registered for its extension if there is one,
// otherwise to the reader for its shape — glob, dir or file.
func (r *Registry) Ingest(ctx context.Context, ref string) ([]semantic.Doc, error) {
	if strings.TrimSpace(ref) == "" {
		return nil, ErrEmpty
	}
	key := Scheme(ref)
	if key == "file" || key == "dir" || key == "glob" {
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(ref), "."))
		if in, ok := r.Get(ext); ok && ext != "" {
			return in.Ingest(ctx, ref)
		}
	}
	in, ok := r.Get(key)
	if !ok {
		return nil, fmt.Errorf("%w %q: %s", ErrScheme, key, ref)
	}
	return in.Ingest(ctx, ref)
}

// Scheme names the kind of source a reference points at.
//
// A URL yields its own scheme. "-" is standard input. A local path yields
// "glob" when it contains pattern characters, "dir" when it names a
// directory that exists, and "file" otherwise — so, like the Python it is
// ported from, this looks at the filesystem to tell a directory from a file.
func Scheme(ref string) string {
	ref = strings.TrimSpace(ref)
	switch {
	case ref == "":
		return ""
	case ref == "-":
		return "stdin"
	}
	if s, rest, ok := strings.Cut(ref, "://"); ok && rest != "" && word(s) {
		return strings.ToLower(s)
	}
	if strings.ContainsAny(ref, "*?[") {
		return "glob"
	}
	if fi, err := os.Stat(local(ref)); err == nil && fi.IsDir() {
		return "dir"
	}
	return "file"
}

// word reports whether s looks like a URL scheme rather than the head of a
// path that happens to contain "://".
func word(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case i > 0 && (c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'):
		default:
			return false
		}
	}
	return true
}
