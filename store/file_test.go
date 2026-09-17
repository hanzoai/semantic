package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// write fills a store with one of everything, so a reopen has something of
// each kind to get wrong.
func write(t *testing.T, s *File) {
	t.Helper()
	ctx := t.Context()
	for _, it := range corpus {
		if err := s.Put(ctx, it.ID, it.Vec, it.Meta); err != nil {
			t.Fatalf("Put %s: %v", it.ID, err)
		}
	}
	if err := s.Node(ctx, "ada", map[string]any{"name": "Ada", "born": 1815}); err != nil {
		t.Fatal(err)
	}
	if err := s.Edge(ctx, "ada", "charles", "knows"); err != nil {
		t.Fatal(err)
	}
	if err := s.Assert(ctx, "ada", "wrote", "notes"); err != nil {
		t.Fatal(err)
	}
}

// check asks a store everything write's data can answer.
func check(t *testing.T, s *File) {
	t.Helper()
	ctx := t.Context()
	want := Stat{Vectors: 4, Nodes: 2, Edges: 1, Facts: 1}
	if got := s.Stat(); got != want {
		t.Errorf("holds %+v, want %+v", got, want)
	}
	near, err := s.Near(ctx, []float32{1, 0}, 2)
	if err != nil {
		t.Fatal(err)
	}
	equal(t, ids(near), []string{"east", "tilt"})

	if _, meta, ok := s.Get("east"); !ok || meta["lang"] != "en" {
		t.Errorf("east came back as %v %v", meta, ok)
	}
	p, ok := s.Props("ada")
	if !ok {
		t.Fatal("the node is gone")
	}
	// JSON has one number type, so 1815 comes back as a float. It is the
	// same number, and a filter has to agree.
	if !(Filter{}.Eq("born", 1815)).Match(p) {
		t.Errorf("properties are %v, want born 1815", p)
	}
	out, err := s.Out(ctx, "ada")
	if err != nil {
		t.Fatal(err)
	}
	equal(t, out, []string{"charles"})

	facts, err := s.Match(ctx, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	equal(t, facts, [][3]string{{"ada", "wrote", "notes"}})
}

func open(t *testing.T, path string) *File {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestFileSurvivesReopening(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb")
	s := open(t, path)
	write(t, s)
	check(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	check(t, open(t, path))
}

func TestFileRecordsRemovals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb")
	s := open(t, path)
	ctx := t.Context()
	write(t, s)
	if err := s.Drop(ctx, "east"); err != nil {
		t.Fatal(err)
	}
	if err := s.Unlink(ctx, "ada", "charles", "knows"); err != nil {
		t.Fatal(err)
	}
	if err := s.Retract(ctx, "ada", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Node(ctx, "ada", map[string]any{"born": 1816}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	back := open(t, path)
	want := Stat{Vectors: 3, Nodes: 2, Edges: 0, Facts: 0}
	if got := back.Stat(); got != want {
		t.Errorf("holds %+v, want %+v", got, want)
	}
	if _, _, ok := back.Get("east"); ok {
		t.Error("a dropped vector came back")
	}
	p, _ := back.Props("ada")
	if !(Filter{}.Eq("born", 1816).Eq("name", "Ada")).Match(p) {
		t.Errorf("properties are %v, want the later born and the earlier name", p)
	}
}

func TestFileCut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb")
	s := open(t, path)
	write(t, s)
	if err := s.Cut(t.Context(), "ada"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	back := open(t, path)
	if _, ok := back.Props("ada"); ok {
		t.Error("a cut node came back")
	}
	if got := back.Edges("", "", ""); len(got) != 0 {
		t.Errorf("the cut node's edges came back: %v", got)
	}
}

func TestFileRank(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb")
	s := open(t, path)
	write(t, s)
	if err := s.Rank(Euclid); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	back := open(t, path)
	if back.Metric() != Euclid {
		t.Errorf("reopened store ranks by %q, want %q", back.Metric(), Euclid)
	}
	got, err := back.Near(t.Context(), []float32{1, 0}, 1)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, got[0].Score, 0)
}

func TestFileDiscardsAnUnfinishedWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb")
	s := open(t, path)
	write(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	whole, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		tail string
	}{
		{"a line that never ended", `{"op":"put","id":"half`},
		{"a line that ended but says nothing", "\n"},
		{"bytes from nowhere", "\x00\x01\x02"},
	} {
		t.Run(c.name, func(t *testing.T) {
			torn := filepath.Join(t.TempDir(), "kb")
			if err := os.WriteFile(torn, append(append([]byte{}, whole...), c.tail...), 0o644); err != nil {
				t.Fatal(err)
			}
			back := open(t, torn)
			check(t, back)

			// The tail is gone, so the next write lands on a clean file.
			if err := back.Assert(t.Context(), "charles", "wrote", "engine"); err != nil {
				t.Fatal(err)
			}
			if err := back.Close(); err != nil {
				t.Fatal(err)
			}
			again := open(t, torn)
			if got := again.Stat().Facts; got != 2 {
				t.Errorf("after writing over the torn tail: %d facts, want 2", got)
			}
		})
	}
}

func TestFileStopsAtACorruptRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb")
	s := open(t, path)
	write(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// A whole line that is not a record ends the replay: what came before
	// it is trusted, what came after it cannot be.
	whole, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	broken := append(append([]byte{}, whole...), []byte("nonsense\n"+string(whole))...)
	if err := os.WriteFile(path, broken, 0o644); err != nil {
		t.Fatal(err)
	}
	check(t, open(t, path))
}

func TestFileRefusesAnUnreadableRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb")
	if err := os.WriteFile(path, []byte(`{"op":"sideways"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil || !strings.Contains(err.Error(), "sideways") {
		t.Errorf("opening a file with an unknown record gave %v, want it named", err)
	}
}

func TestFileCompacts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb")
	s := open(t, path)
	ctx := t.Context()
	write(t, s)
	// Churn: the log holds far more than the state it describes.
	for i := range 50 {
		if err := s.Put(ctx, "churn", []float32{float32(i), 0}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Drop(ctx, "churn"); err != nil {
		t.Fatal(err)
	}

	long, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Compact(); err != nil {
		t.Fatal(err)
	}
	short, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if short.Size() >= long.Size() {
		t.Errorf("compacting grew the file: %d then %d", long.Size(), short.Size())
	}
	if lines := count(t, path); lines != 1 {
		t.Errorf("a compacted file has %d lines, want one snapshot", lines)
	}

	// The snapshot has to say everything the log did, before and after a
	// reopen, and the file has to be writable again afterwards.
	check(t, s)
	if err := s.Assert(ctx, "charles", "wrote", "engine"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	back := open(t, path)
	if got := back.Stat().Facts; got != 2 {
		t.Errorf("after compacting and writing: %d facts, want 2", got)
	}
	if _, err := os.Stat(path + ".new"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("compaction left %s behind", path+".new")
	}
}

func TestFileCompactsAnEmptyStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb")
	s := open(t, path)
	if err := s.Compact(); err != nil {
		t.Fatal(err)
	}
	if got := s.Stat(); got != (Stat{}) {
		t.Errorf("an empty store compacted to %+v", got)
	}
	if err := s.Put(t.Context(), "a", []float32{1}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if got := open(t, path).Stat().Vectors; got != 1 {
		t.Errorf("holds %d vectors after compacting an empty store, want 1", got)
	}
}

func TestFileRefusesToWriteWhenClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(t.Context(), "a", []float32{1}, nil); !errors.Is(err, os.ErrClosed) {
		t.Errorf("writing to a closed store gave %v, want os.ErrClosed", err)
	}
	if err := s.Compact(); !errors.Is(err, os.ErrClosed) {
		t.Errorf("compacting a closed store gave %v, want os.ErrClosed", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("closing twice: %v", err)
	}
}

func TestFileChecksBeforeItWrites(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "kb"))
	ctx := t.Context()
	for _, c := range []struct {
		name string
		call func() error
		want error
	}{
		{"Put", func() error { return s.Put(ctx, "", []float32{1}, nil) }, ErrID},
		{"Node", func() error { return s.Node(ctx, "", nil) }, ErrID},
		{"Edge", func() error { return s.Edge(ctx, "a", "", "x") }, ErrID},
		{"Cut", func() error { return s.Cut(ctx, "") }, ErrID},
		{"Assert", func() error { return s.Assert(ctx, "a", "", "c") }, ErrTerm},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := c.call(); !errors.Is(err, c.want) {
				t.Errorf("gave %v, want %v", err, c.want)
			}
		})
	}
	if n := count(t, s.Name()); n != 0 {
		t.Errorf("a refused write left %d records on disk", n)
	}
}

func TestOpenRefusesNoPath(t *testing.T) {
	if _, err := Open(""); !errors.Is(err, os.ErrInvalid) {
		t.Errorf("Open(\"\") gave %v, want os.ErrInvalid", err)
	}
}

func count(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(b), "\n")
}
