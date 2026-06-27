// Package timer manages local, offline work sessions. A technician rarely logs
// time to the second; they work in chunks and switch between customers. A
// session accrues elapsed time that becomes a *suggested* number of hours; the
// human (or the AI driving the CLI) confirms or overrides it before anything is
// written to Autotask.
//
// Multiple sessions can be open at once; only those with a running segment
// accrue time, so you can pause one customer and switch to another.
package timer

import (
	"autotask-easymode/internal/atomicfile"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// NoCompany is the Session.CompanyID value meaning no company was set (the
// session was attached to an existing ticket instead). It is negative because 0
// is a valid company id (the owner org).
const NoCompany = -1

// Session is one unit of work tied to (eventually) one Autotask ticket.
type Session struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	CompanyID int       `json:"companyId"`       // NoCompany (-1) when unset; 0 is a real company
	TicketID  int64     `json:"ticketId"`        // 0 until a ticket is created/attached
	Title     string    `json:"title"`           // ticket title
	Notes     []string  `json:"notes,omitempty"` // accumulated work notes
	CreatedAt time.Time `json:"createdAt"`
	StartedAt time.Time `json:"startedAt"`      // start of current running segment; zero when paused
	Accrued   float64   `json:"accruedSeconds"` // time banked from previous segments
}

// Running reports whether the session is currently accruing time.
func (s *Session) Running() bool { return !s.StartedAt.IsZero() }

// Elapsed returns total worked time as of now.
func (s *Session) Elapsed(now time.Time) time.Duration {
	secs := s.Accrued
	if s.Running() {
		secs += now.Sub(s.StartedAt).Seconds()
	}
	return time.Duration(secs * float64(time.Second))
}

// Hours returns elapsed time in decimal hours, rounded to 2 decimals.
func (s *Session) Hours(now time.Time) float64 {
	return round2(s.Elapsed(now).Hours())
}

// State is the full set of local sessions, persisted as JSON.
type State struct {
	Sessions []*Session `json:"sessions"`
	path     string
}

// Load reads the state file, returning empty state if none exists.
func Load(path string) (*State, error) {
	st := &State{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, st); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	st.path = path
	return st, nil
}

// Save writes the state file with restrictive permissions.
func (st *State) Save() error {
	return atomicfile.WriteJSON(st.path, st, 0o600)
}

// Find returns the session with the given id, or nil.
func (st *State) Find(id string) *Session {
	for _, s := range st.Sessions {
		if s.ID == id {
			return s
		}
	}
	return nil
}

// Active returns the single running session, or nil if none/ambiguous-free.
// If exactly one session is running it is returned; otherwise nil.
func (st *State) Active() *Session {
	var active *Session
	for _, s := range st.Sessions {
		if s.Running() {
			if active != nil {
				return nil // more than one running; caller must disambiguate
			}
			active = s
		}
	}
	return active
}

// Start creates a new running session and returns it. By default any other
// running sessions are paused so only one timer runs at a time; pass
// keepOthersRunning to allow concurrent timers.
func (st *State) Start(label string, companyID int, title string, now time.Time, keepOthersRunning bool) *Session {
	if !keepOthersRunning {
		st.pauseAll(now)
	}
	s := &Session{
		ID:        st.nextID(),
		Label:     label,
		CompanyID: companyID,
		Title:     title,
		CreatedAt: now,
		StartedAt: now,
	}
	st.Sessions = append(st.Sessions, s)
	return s
}

// Pause banks the current segment of a running session.
func (s *Session) Pause(now time.Time) {
	if s.Running() {
		s.Accrued += now.Sub(s.StartedAt).Seconds()
		s.StartedAt = time.Time{}
	}
}

// Resume starts a new running segment.
func (s *Session) Resume(now time.Time) {
	if !s.Running() {
		s.StartedAt = now
	}
}

// Switch makes s the only running session.
func (st *State) Switch(s *Session, now time.Time) {
	st.pauseAll(now)
	s.Resume(now)
}

// Remove deletes a session from state.
func (st *State) Remove(id string) {
	out := st.Sessions[:0]
	for _, s := range st.Sessions {
		if s.ID != id {
			out = append(out, s)
		}
	}
	st.Sessions = out
}

// AddNote appends a non-empty work note to a session.
func (s *Session) AddNote(note string) {
	note = strings.TrimSpace(note)
	if note != "" {
		s.Notes = append(s.Notes, note)
	}
}

// JoinedNotes returns all notes as a single newline-separated string.
func (s *Session) JoinedNotes() string {
	return strings.Join(s.Notes, "\n")
}

func (st *State) pauseAll(now time.Time) {
	for _, s := range st.Sessions {
		s.Pause(now)
	}
}

// nextID returns the smallest free "sN" id.
func (st *State) nextID() string {
	used := map[int]bool{}
	for _, s := range st.Sessions {
		if strings.HasPrefix(s.ID, "s") {
			if n, err := strconv.Atoi(s.ID[1:]); err == nil {
				used[n] = true
			}
		}
	}
	for n := 1; ; n++ {
		if !used[n] {
			return "s" + strconv.Itoa(n)
		}
	}
}

// Sorted returns sessions ordered by id for stable display.
func (st *State) Sorted() []*Session {
	out := append([]*Session(nil), st.Sessions...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func round2(f float64) float64 {
	return float64(int64(f*100+0.5)) / 100
}
