package main

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"testing"
)

// The metric is only worth reporting if it is the same metric. These tests
// hold it against the Python it was ported from: testdata/stems.tsv is NLTK's
// answer for every word the corpus contains, testdata/normalise.tsv is the
// official normalize_answer over every question, answer and turn, and
// testdata/grade.tsv is eval_question_answering's score for a spread of
// answers of every category.
//
// All three are written by ref/golden.py, which imports the official code
// unmodified. Two of them quote the dataset, which is CC BY-NC and so is not
// kept in this repository; `make golden` writes them and those two tests skip
// until it has. The stem table is a word list and is here.

func TestStem(t *testing.T) {
	for word, want := range pairs(t, "testdata/stems.tsv") {
		if got := stem(word); got != want {
			t.Errorf("stem(%q) = %q, NLTK says %q", word, got, want)
		}
	}
}

func TestNormalise(t *testing.T) {
	for text, want := range pairs(t, "testdata/normalise.tsv") {
		if got := normalise(text); got != want {
			t.Errorf("normalise(%q) = %q, the scorer says %q", text, got, want)
		}
	}
}

func TestGrade(t *testing.T) {
	f, err := open(t, "testdata/grade.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	lines := bufio.NewScanner(f)
	lines.Buffer(make([]byte, 0, 1<<20), 1<<20)
	n := 0
	for lines.Scan() {
		field := strings.Split(lines.Text(), "\t")
		if len(field) != 4 {
			t.Fatalf("grade.tsv: %d fields in %q", len(field), lines.Text())
		}
		kind := int(field[0][0] - '0')
		want := parse(t, field[3])
		if got := grade(kind, field[1], field[2]); !near(got, want) {
			t.Errorf("grade(%d, %q, %q) = %.6f, the scorer says %.6f",
				kind, field[1], field[2], got, want)
		}
		n++
	}
	if err := lines.Err(); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("grade.tsv is empty")
	}
	t.Logf("%d scores agree with the official scorer", n)
}

// open reads a golden file, or skips the test when it has not been written.
// The two that quote the dataset are not in the repository, since the dataset
// is not ours to redistribute.
func open(t *testing.T, path string) (*os.File, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		t.Skipf("%s has not been written; run `make golden` in bench/locomo", path)
	}
	return f, err
}

// near allows for the last bit of a float written out and read back.
func near(a, b float64) bool {
	d := a - b
	return d < 1e-9 && d > -1e-9
}

func parse(t *testing.T, s string) float64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return f
}

// pairs reads a two-column file of input and expected output.
func pairs(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := open(t, path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	out := map[string]string{}
	lines := bufio.NewScanner(f)
	lines.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for lines.Scan() {
		before, after, ok := strings.Cut(lines.Text(), "\t")
		if !ok {
			t.Fatalf("%s: no tab in %q", path, lines.Text())
		}
		out[unescape(before)] = unescape(after)
	}
	if err := lines.Err(); err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatalf("%s is empty", path)
	}
	t.Logf("%s: %d cases", path, len(out))
	return out
}

// unescape puts back the tabs and newlines the golden file escaped so that one
// case stays on one line.
func unescape(s string) string {
	return strings.NewReplacer(`\t`, "\t", `\n`, "\n", `\\`, `\`).Replace(s)
}
