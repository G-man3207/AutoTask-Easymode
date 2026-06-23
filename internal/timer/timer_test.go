package timer

import (
	"path/filepath"
	"testing"
	"time"
)

var base = time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)

func TestStartPausesOthersByDefault(t *testing.T) {
	st := &State{}
	a := st.Start("a", 1, "A", base, false)
	b := st.Start("b", 2, "B", base, false)
	if a.Running() {
		t.Error("a should be paused after starting b")
	}
	if !b.Running() {
		t.Error("b should be running")
	}
}

func TestStartKeepOthersRunning(t *testing.T) {
	st := &State{}
	a := st.Start("a", 1, "A", base, true)
	b := st.Start("b", 2, "B", base, true)
	if !a.Running() || !b.Running() {
		t.Error("both should run with keepOthers")
	}
	if st.Active() != nil {
		t.Error("Active should be nil when more than one runs")
	}
}

func TestElapsedAndHours(t *testing.T) {
	st := &State{}
	s := st.Start("a", 1, "A", base, false)
	now := base.Add(90 * time.Minute)
	if got := s.Elapsed(now); got != 90*time.Minute {
		t.Errorf("elapsed = %v", got)
	}
	if got := s.Hours(now); got != 1.5 {
		t.Errorf("hours = %v want 1.5", got)
	}
}

func TestPauseResumeBanksTime(t *testing.T) {
	st := &State{}
	s := st.Start("a", 1, "A", base, false)
	s.Pause(base.Add(30 * time.Minute))
	if s.Running() {
		t.Error("should be paused")
	}
	resumeAt := base.Add(60 * time.Minute)
	s.Resume(resumeAt)
	if got := s.Hours(resumeAt.Add(15 * time.Minute)); got != 0.75 {
		t.Errorf("hours = %v want 0.75 (30m banked + 15m)", got)
	}
}

func TestSwitchPausesOthers(t *testing.T) {
	st := &State{}
	a := st.Start("a", 1, "A", base, true)
	b := st.Start("b", 2, "B", base, true)
	st.Switch(a, base.Add(time.Hour))
	if !a.Running() {
		t.Error("a should run after switch")
	}
	if b.Running() {
		t.Error("b should be paused after switch")
	}
}

func TestNextIDReusesFreedSlot(t *testing.T) {
	st := &State{}
	s1 := st.Start("a", 1, "A", base, true)
	s2 := st.Start("b", 2, "B", base, true)
	if s1.ID != "s1" || s2.ID != "s2" {
		t.Fatalf("ids = %q %q", s1.ID, s2.ID)
	}
	st.Remove("s1")
	if s3 := st.Start("c", 3, "C", base, true); s3.ID != "s1" {
		t.Errorf("expected reuse of s1, got %q", s3.ID)
	}
}

func TestFindActiveRemove(t *testing.T) {
	st := &State{}
	s := st.Start("a", 1, "A", base, false)
	if st.Find(s.ID) != s {
		t.Error("Find should return the session")
	}
	if st.Active() != s {
		t.Error("Active should return the single running session")
	}
	st.Remove(s.ID)
	if st.Find(s.ID) != nil {
		t.Error("session should be gone")
	}
}

func TestAddNoteAndJoined(t *testing.T) {
	s := &Session{}
	s.AddNote(" first ")
	s.AddNote("")
	s.AddNote("second")
	if len(s.Notes) != 2 {
		t.Fatalf("notes = %v", s.Notes)
	}
	if s.JoinedNotes() != "first\nsecond" {
		t.Errorf("joined = %q", s.JoinedNotes())
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s := st.Start("a", 1, "A", base, false)
	s.AddNote("hello")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Sessions) != 1 {
		t.Fatalf("sessions = %d", len(got.Sessions))
	}
	if got.Sessions[0].Label != "a" || got.Sessions[0].Notes[0] != "hello" {
		t.Errorf("bad round trip: %+v", got.Sessions[0])
	}
}

func TestSortedOrder(t *testing.T) {
	st := &State{}
	st.Start("a", 1, "A", base, true)
	st.Start("b", 2, "B", base, true)
	st.Remove("s1")
	st.Start("c", 3, "C", base, true) // reuses s1
	sorted := st.Sorted()
	if sorted[0].ID != "s1" || sorted[1].ID != "s2" {
		t.Errorf("order = %q,%q", sorted[0].ID, sorted[1].ID)
	}
}
