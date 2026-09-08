package split

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/hanzoai/semantic"
)

// Options are the settings a splitter can be built from by name, for callers
// whose choice of splitter arrives as configuration rather than as code. Each
// splitter reads the fields that apply to it and ignores the rest.
type Options struct {
	Size       int              // chunk size, in the splitter's own unit
	Overlap    int              // units repeated from the end of the previous chunk
	Max        int              // units per chunk, where a splitter counts them
	Separators []string         // separator hierarchy for recursive
	Threshold  float64          // similarity below which semantic cuts
	Embed      Embed            // vectors for semantic
	Count      func(string) int // token count for tokens
}

// Build makes one splitter from a set of options.
type Build func(Options) (semantic.Splitter, error)

var (
	mu    sync.RWMutex
	built = map[string]Build{
		"chars": func(o Options) (semantic.Splitter, error) { return Chars{Size: o.Size, Overlap: o.Overlap}, nil },
		"words": func(o Options) (semantic.Splitter, error) { return Words{Size: o.Size, Overlap: o.Overlap}, nil },
		"tokens": func(o Options) (semantic.Splitter, error) {
			return Tokens{Size: o.Size, Overlap: o.Overlap, Count: o.Count}, nil
		},
		"sentences": func(o Options) (semantic.Splitter, error) {
			return Sentences{Size: o.Size, Max: o.Max, Overlap: o.Overlap}, nil
		},
		"paragraphs": func(o Options) (semantic.Splitter, error) { return Paragraphs{Size: o.Size, Overlap: o.Overlap}, nil },
		"recursive": func(o Options) (semantic.Splitter, error) {
			return Recursive{Size: o.Size, Overlap: o.Overlap, Separators: o.Separators}, nil
		},
		"markdown": func(o Options) (semantic.Splitter, error) { return Markdown{Size: o.Size}, nil },
		"code":     func(o Options) (semantic.Splitter, error) { return Code{Size: o.Size}, nil },
		"semantic": func(o Options) (semantic.Splitter, error) {
			if o.Embed == nil {
				return nil, errors.New("split: semantic needs Options.Embed")
			}
			return Semantic{Embed: o.Embed, Threshold: o.Threshold, Size: o.Size}, nil
		},
	}
)

// New returns the splitter registered under name.
func New(name string, o Options) (semantic.Splitter, error) {
	mu.RLock()
	build, ok := built[name]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("split: no splitter named %q", name)
	}
	return build(o)
}

// Names lists every splitter New can build, in order.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	names := make([]string, 0, len(built))
	for name := range built {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Register adds a splitter to the ones New can build, replacing any of the
// same name. It is how a caller reaches its own splitter from configuration.
func Register(name string, build Build) {
	mu.Lock()
	defer mu.Unlock()
	built[name] = build
}
