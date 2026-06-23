package main

import "testing"

func TestDefOr(t *testing.T) {
	if defOr(0, 5) != 5 {
		t.Error("zero should yield fallback")
	}
	if defOr(3, 5) != 3 {
		t.Error("nonzero should yield value")
	}
}

func TestRound2(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{1.234, 1.23},
		{1.236, 1.24},
		{10, 10},
		{0.1, 0.1},
	}
	for _, c := range cases {
		if got := round2(c.in); got != c.want {
			t.Errorf("round2(%v) = %v want %v", c.in, got, c.want)
		}
	}
}

func TestAsInt64(t *testing.T) {
	if asInt64(float64(42)) != 42 {
		t.Error("float64")
	}
	if asInt64("13") != 13 {
		t.Error("string")
	}
	if asInt64(int64(5)) != 5 {
		t.Error("int64")
	}
	if asInt64(7) != 7 {
		t.Error("int")
	}
	if asInt64(nil) != 0 || asInt64(true) != 0 {
		t.Error("unknown/nil should be 0")
	}
}

func TestAsFloat(t *testing.T) {
	if asFloat(1.5) != 1.5 {
		t.Error("float64")
	}
	if asFloat("2.5") != 2.5 {
		t.Error("string")
	}
	if asFloat(3) != 3 {
		t.Error("int")
	}
	if asFloat(nil) != 0 {
		t.Error("nil")
	}
}

func TestAsString(t *testing.T) {
	if asString(nil) != "" {
		t.Error("nil")
	}
	if asString("x") != "x" {
		t.Error("string")
	}
	if asString(42) != "42" {
		t.Error("int -> string")
	}
}

func TestFirstArg(t *testing.T) {
	if firstArg(nil) != "" {
		t.Error("nil")
	}
	if firstArg([]string{"a", "b"}) != "a" {
		t.Error("first element")
	}
}

func TestRedact(t *testing.T) {
	if redact("") != "" {
		t.Error("empty stays empty")
	}
	if redact("  ") != "" {
		t.Error("blank stays empty")
	}
	if redact("secret") != "***set***" {
		t.Error("nonempty is masked")
	}
}

func TestSetInt(t *testing.T) {
	var n int
	if err := setInt(&n, "12"); err != nil || n != 12 {
		t.Errorf("valid: err=%v n=%d", err, n)
	}
	if err := setInt(&n, "nope"); err == nil {
		t.Error("invalid should error")
	}
}

func TestPositiveOr(t *testing.T) {
	if positiveOr(0, 3) != 3 {
		t.Error("zero -> fallback")
	}
	if positiveOr(2, 3) != 2 {
		t.Error("positive -> value")
	}
	if positiveOr(-1, 3) != 3 {
		t.Error("negative -> fallback")
	}
}

func TestJoinNotes(t *testing.T) {
	if joinNotes([]string{"a"}, "b") != "a\nb" {
		t.Error("append extra")
	}
	if joinNotes([]string{"a"}, "   ") != "a" {
		t.Error("blank extra ignored")
	}
	if joinNotes(nil, "x") != "x" {
		t.Error("nil notes")
	}
}

func TestDateRange(t *testing.T) {
	from, to, err := dateRange("2026-01-01", "2026-01-31")
	if err != nil {
		t.Fatal(err)
	}
	if from != "2026-01-01T00:00:00" || to != "2026-01-31T23:59:59" {
		t.Errorf("range = %q %q", from, to)
	}
	if _, _, err := dateRange("bad", ""); err == nil {
		t.Error("bad from should error")
	}
	if _, _, err := dateRange("", "bad"); err == nil {
		t.Error("bad to should error")
	}
	if f, tt, err := dateRange("", ""); err != nil || f != "" || tt != "" {
		t.Error("empty range should be allowed")
	}
}

func TestParseSearch(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantQuery string
		wantLimit int
		wantComp  string
		wantErr   bool
	}{
		{"plain multiword", []string{"Acme", "Care"}, "Acme Care", defaultSearchLimit, "", false},
		{"limit after query (the live bug)", []string{"Acme", "Care", "--limit", "5"}, "Acme Care", 5, "", false},
		{"limit before query", []string{"--limit", "5", "Acme"}, "Acme", 5, "", false},
		{"limit inline", []string{"--limit=7", "Acme"}, "Acme", 7, "", false},
		{"company flag", []string{"--company", "acme", "Migration"}, "Migration", defaultSearchLimit, "acme", false},
		{"bad limit", []string{"--limit", "x", "q"}, "", 0, "", true},
		{"missing value", []string{"q", "--limit"}, "", 0, "", true},
		{"unknown flag", []string{"--bogus", "q"}, "", 0, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSearch(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.query != tt.wantQuery || got.limit != tt.wantLimit || got.company != tt.wantComp {
				t.Errorf("got %+v", got)
			}
		})
	}
}

func TestParseID(t *testing.T) {
	if id, err := parseID("42"); err != nil || id != 42 {
		t.Errorf("valid: err=%v id=%d", err, id)
	}
	if _, err := parseID(""); err == nil {
		t.Error("empty should error")
	}
	if _, err := parseID("x"); err == nil {
		t.Error("nonnumeric should error")
	}
}
