package agent

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Scope is whose knowledge this is and what it was for. An empty field asks
// nothing, so the zero Scope, read as a filter, admits everything.
type Scope struct {
	Agent   string
	Session string
	Task    string
}

// Covers reports whether s, read as a filter, admits o.
func (s Scope) Covers(o Scope) bool {
	return (s.Agent == "" || s.Agent == o.Agent) &&
		(s.Session == "" || s.Session == o.Session) &&
		(s.Task == "" || s.Task == o.Task)
}

// Note is one thing an agent was told or observed.
type Note struct {
	ID    string
	Text  string
	At    time.Time
	Scope Scope
	Of    []string // the entities the note is about
	Props map[string]any
}

// Memory is what an agent has been told. Everything kept is searchable; the
// most recent is also held in a window, which is the part that fits in a
// prompt. The window is bounded twice — by count and by size — because either
// alone lets the other run away: ten long notes overflow a prompt, and a size
// budget alone can hold a hundred fragments no one wanted.
//
// Its zero value is an empty memory with a window of ten notes and about two
// thousand tokens, and it is safe for concurrent use.
type Memory struct {
	mu sync.RWMutex

	// Window is how many recent notes to hold. Zero means ten.
	Window int
	// Budget is roughly how many tokens the window may hold. Zero means two
	// thousand. Size is counted the cheap way, four characters to a token,
	// which is close enough to keep a prompt inside its limit.
	Budget int
	// Hold is how long a note is kept. Zero keeps everything.
	Hold time.Duration

	notes  map[string]*Note
	order  []string
	window []string
}

func (m *Memory) most() int {
	if m.Window < 1 {
		return 10
	}
	return m.Window
}

func (m *Memory) budget() int {
	if m.Budget < 1 {
		return 2000
	}
	return m.Budget
}

// tokens is the cheap size of a piece of text: four characters to a token.
func tokens(s string) int { return len(s) / 4 }

// Add keeps a note and puts it at the head of the window, dropping whatever
// no longer fits and whatever is older than Hold. It fills in an id and a
// time when the caller left them empty, and returns the id.
func (m *Memory) Add(n Note) string {
	if n.ID == "" {
		n.ID = mint("note")
	}
	if n.At.IsZero() {
		n.At = time.Now().UTC()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.notes == nil {
		m.notes = map[string]*Note{}
	}
	if _, had := m.notes[n.ID]; !had {
		m.order = append(m.order, n.ID)
	}
	m.notes[n.ID] = &n
	m.window = append(without(m.window, n.ID), n.ID)
	m.trim()
	m.expire(n.At)
	return n.ID
}

// trim drops the oldest of the window until it fits both bounds. The caller
// holds the lock.
func (m *Memory) trim() {
	for len(m.window) > m.most() {
		m.window = m.window[1:]
	}
	size := 0
	for _, id := range m.window {
		size += tokens(m.notes[id].Text)
	}
	for size > m.budget() && len(m.window) > 0 {
		size -= tokens(m.notes[m.window[0]].Text)
		m.window = m.window[1:]
	}
}

// expire forgets notes older than Hold, measured from the moment now. The
// caller holds the lock.
func (m *Memory) expire(now time.Time) {
	if m.Hold <= 0 {
		return
	}
	cut := now.Add(-m.Hold)
	for _, id := range append([]string{}, m.order...) {
		if m.notes[id].At.Before(cut) {
			m.forget(id)
		}
	}
}

// Get reads one note.
func (m *Memory) Get(id string) (Note, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n, ok := m.notes[id]
	if !ok {
		return Note{}, false
	}
	return *n, true
}

// Forget drops one note, from the window and from what is kept.
func (m *Memory) Forget(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.notes[id]; !ok {
		return false
	}
	m.forget(id)
	return true
}

func (m *Memory) forget(id string) {
	delete(m.notes, id)
	m.order = without(m.order, id)
	m.window = without(m.window, id)
}

// All lists every note kept, oldest first, narrowed to a scope.
func (m *Memory) All(s Scope) []Note {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Note, 0, len(m.order))
	for _, id := range m.order {
		if n := m.notes[id]; n != nil && s.Covers(n.Scope) {
			out = append(out, *n)
		}
	}
	return out
}

// Recent is the window, oldest first: what an agent still has in mind.
func (m *Memory) Recent() []Note {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Note, 0, len(m.window))
	for _, id := range m.window {
		out = append(out, *m.notes[id])
	}
	return out
}

// About lists the notes that name an entity, oldest first.
func (m *Memory) About(entity string) []Note {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []Note
	for _, id := range m.order {
		for _, e := range m.notes[id].Of {
			if e == entity {
				out = append(out, *m.notes[id])
				break
			}
		}
	}
	return out
}

// Count is how many notes are kept.
func (m *Memory) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.notes)
}

// Found is a note a query matched, how well, and whether it was still in the
// window.
type Found struct {
	Note  Note
	Score float64
	Fresh bool
}

// Recall finds the notes that answer a query, best first, at most n of them
// (all of them when n is not positive). A note still in the window scores a
// little higher than the same words recalled from further back, because what
// was just said is usually what the question is about.
func (m *Memory) Recall(q string, n int, s Scope) []Found {
	terms := words(q)
	if len(terms) == 0 {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	fresh := set(m.window)
	var out []Found
	for _, id := range m.order {
		note := m.notes[id]
		if note == nil || !s.Covers(note.Scope) {
			continue
		}
		have := set(words(note.Text))
		found := 0
		for _, t := range terms {
			if have[t] {
				found++
			}
		}
		if found == 0 {
			continue
		}
		score := float64(found) / float64(len(terms))
		if fresh[id] {
			score += 0.1
			if score > 1 {
				score = 1
			}
		}
		out = append(out, Found{Note: *note, Score: score, Fresh: fresh[id]})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// mint makes an identifier that will not collide with another.
func mint(kind string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s-%d", kind, time.Now().UnixNano())
	}
	return kind + "-" + hex.EncodeToString(b[:])
}
