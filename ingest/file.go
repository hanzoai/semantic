package ingest

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/hanzoai/semantic"
)

// File reads one file. How many documents it yields depends on what the file
// holds: text is one document, a JSON array or a CSV is one per record.
type File struct {
	// Max is the largest file this reader will read, in bytes. Zero means
	// no limit.
	Max int64
	// Decode turns the bytes into documents. Nil picks a decoder from the
	// file's extension: json, jsonl, csv, tsv, html, otherwise plain text.
	Decode Decoder
}

// Ingest reads the file at ref. A "file://" prefix is accepted and stripped.
func (f File) Ingest(ctx context.Context, ref string) ([]semantic.Doc, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p := local(ref)
	if p == "" {
		return nil, ErrEmpty
	}
	fi, err := os.Stat(p)
	if err != nil {
		return nil, fmt.Errorf("ingest: %w", err)
	}
	if fi.IsDir() {
		return nil, fmt.Errorf("ingest: %s is a directory", p)
	}
	if f.Max > 0 && fi.Size() > f.Max {
		return nil, fmt.Errorf("ingest: %s is %d bytes: %w", p, fi.Size(), ErrSize)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("ingest: %w", err)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	o := Origin{
		Ref:  abs,
		Size: int64(len(b)),
		Mod:  fi.ModTime(),
		Hash: sum(b),
		Type: kind(p, b),
		Mime: media(p),
		At:   time.Now(),
	}
	return decode(f.Decode, o).Decode(b, o)
}

// Dir walks a directory tree and reads the files in it.
//
// Documents read before an error are still returned alongside it, so one
// unreadable file does not discard a walk of ten thousand.
type Dir struct {
	// Match keeps only paths matching one of these patterns. A pattern is
	// tried against the file's name and against its path relative to the
	// root, with path.Match syntax: "*.md", "docs/*.json". Empty keeps
	// everything.
	Match []string
	// Skip drops paths matching one of these patterns, applied after Match.
	Skip []string
	// Depth limits how deep the walk goes: 1 is the directory itself, 2 its
	// subdirectories, and 0 — the zero value — is unlimited.
	Depth int
	// Min and Max bound file size in bytes. Max zero means no upper bound.
	Min, Max int64
	// Hidden includes files and directories whose name begins with a dot.
	Hidden bool
	// Decode turns each file's bytes into documents, as in File.
	Decode Decoder
}

// Ingest walks the tree rooted at ref.
func (d Dir) Ingest(ctx context.Context, ref string) ([]semantic.Doc, error) {
	root := local(ref)
	if root == "" {
		return nil, ErrEmpty
	}
	fi, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("ingest: %w", err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("ingest: %s is not a directory", root)
	}
	read := File{Max: d.Max, Decode: d.Decode}

	var out []semantic.Doc
	var bad []error
	err = filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			bad = append(bad, err)
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			rel = p
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if !d.Hidden && strings.HasPrefix(e.Name(), ".") {
			if e.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		depth := strings.Count(rel, "/") + 1
		if e.IsDir() {
			if d.Depth > 0 && depth >= d.Depth {
				return fs.SkipDir
			}
			return nil
		}
		if d.Depth > 0 && depth > d.Depth {
			return nil
		}
		if !d.keep(e.Name(), rel) {
			return nil
		}
		info, err := e.Info()
		if err != nil {
			bad = append(bad, err)
			return nil
		}
		if info.Size() < d.Min || (d.Max > 0 && info.Size() > d.Max) {
			return nil
		}
		docs, err := read.Ingest(ctx, p)
		if err != nil {
			bad = append(bad, err)
			return nil
		}
		out = append(out, docs...)
		return nil
	})
	if err != nil {
		bad = append(bad, err)
	}
	return out, errors.Join(bad...)
}

// keep reports whether a file passes the Match and Skip patterns.
func (d Dir) keep(name, rel string) bool {
	if len(d.Match) > 0 && !match(d.Match, name, rel) {
		return false
	}
	return !match(d.Skip, name, rel)
}

// match reports whether one of the patterns matches the name or the relative
// path. A malformed pattern matches nothing.
func match(pats []string, name, rel string) bool {
	for _, p := range pats {
		if ok, err := path.Match(p, name); err == nil && ok {
			return true
		}
		if ok, err := path.Match(p, rel); err == nil && ok {
			return true
		}
	}
	return false
}

// Glob reads every file matching a shell pattern, in sorted order.
type Glob struct {
	// Max is the largest file this reader will read, in bytes, as in File.
	Max int64
	// Decode turns each file's bytes into documents, as in File.
	Decode Decoder
}

// Ingest reads the files matching the pattern ref. As with Dir, documents
// read before an error are returned alongside it.
func (g Glob) Ingest(ctx context.Context, ref string) ([]semantic.Doc, error) {
	pat := local(ref)
	if pat == "" {
		return nil, ErrEmpty
	}
	hits, err := filepath.Glob(pat)
	if err != nil {
		return nil, fmt.Errorf("ingest: pattern %q: %w", pat, err)
	}
	read := File{Max: g.Max, Decode: g.Decode}
	var out []semantic.Doc
	var bad []error
	for _, p := range hits {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		fi, err := os.Stat(p)
		if err != nil {
			bad = append(bad, err)
			continue
		}
		if fi.IsDir() {
			continue
		}
		docs, err := read.Ingest(ctx, p)
		if err != nil {
			bad = append(bad, err)
			continue
		}
		out = append(out, docs...)
	}
	return out, errors.Join(bad...)
}

// local turns a reference into a filesystem path, accepting the "file://"
// form as well as a plain path.
func local(ref string) string {
	ref = strings.TrimSpace(ref)
	if rest, ok := strings.CutPrefix(ref, "file://"); ok {
		if host, p, found := strings.Cut(rest, "/"); found && (host == "" || host == "localhost") {
			return "/" + p
		}
		return rest
	}
	return strings.TrimPrefix(ref, "file:")
}
