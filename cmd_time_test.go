package main

import (
	"autotask-easymode/internal/atapi"
	"strings"
	"testing"
	"time"
)

func TestParseClock(t *testing.T) {
	cases := []struct {
		in      string
		h, m    int
		wantErr bool
	}{
		{"11", 11, 0, false},
		{"9", 9, 0, false},
		{"13:30", 13, 30, false},
		{"00:00", 0, 0, false},
		{"23:59", 23, 59, false},
		{" 8 ", 8, 0, false},
		{"24", 0, 0, true},
		{"11:60", 0, 0, true},
		{"-1", 0, 0, true},
		{"", 0, 0, true},
		{"ab", 0, 0, true},
	}
	for _, c := range cases {
		h, m, err := parseClock(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseClock(%q): expected error", c.in)
			}
			continue
		}
		if err != nil || h != c.h || m != c.m {
			t.Errorf("parseClock(%q) = %d,%d,%v; want %d,%d,nil", c.in, h, m, err, c.h, c.m)
		}
	}
}

func TestParseWindows(t *testing.T) {
	day := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)

	// Shared default note across both windows.
	ws, err := parseWindows("11-12,13-15", day, "shared")
	if err != nil {
		t.Fatalf("shared: %v", err)
	}
	if len(ws) != 2 {
		t.Fatalf("want 2 windows, got %d", len(ws))
	}
	if ws[0].hours != 1 || ws[1].hours != 2 {
		t.Errorf("hours = %v, %v; want 1, 2", ws[0].hours, ws[1].hours)
	}
	if ws[0].note != "shared" || ws[1].note != "shared" {
		t.Errorf("notes = %q, %q; want shared", ws[0].note, ws[1].note)
	}
	if ws[0].start.Hour() != 11 || ws[1].end.Hour() != 15 {
		t.Errorf("unexpected window bounds: %v", ws)
	}

	// Per-window notes override the default.
	ws, err = parseWindows("11-12=fixed X,13:00-15:30=did Y", day, "shared")
	if err != nil {
		t.Fatalf("per-window: %v", err)
	}
	if ws[0].note != "fixed X" || ws[1].note != "did Y" {
		t.Errorf("notes = %q, %q", ws[0].note, ws[1].note)
	}
	if ws[1].hours != 2.5 {
		t.Errorf("hours = %v; want 2.5", ws[1].hours)
	}

	// A window with no note and no default is rejected (Autotask requires one).
	if _, err := parseWindows("11-12", day, ""); err == nil {
		t.Error("expected error for window with no note")
	}
	// Malformed range and non-positive range are rejected.
	if _, err := parseWindows("11", day, "n"); err == nil {
		t.Error("expected error for missing '-'")
	}
	if _, err := parseWindows("12-11", day, "n"); err == nil {
		t.Error("expected error for end <= start")
	}
	if _, err := parseWindows("", day, "n"); err == nil {
		t.Error("expected error for empty spec")
	}
}

func TestTimeAddDryRun(t *testing.T) {
	app := newTestApp(t, &fakeClient{})
	app.cfg.ResourceID = 55

	res, err := app.cmdTimeAdd([]string{"--ticket", "100", "--date", "2026-06-15", "--windows", "11-12,13-15", "--note", "n", "--dry-run"})
	if err != nil {
		t.Fatal(err)
	}
	d := dataMap(t, res)
	if d["totalHours"] != 3.0 {
		t.Errorf("totalHours = %v, want 3", d["totalHours"])
	}
	entriesAny, ok := d["entries"].([]any)
	if !ok || len(entriesAny) != 2 {
		t.Fatalf("entries = %v", d["entries"])
	}
	entries := make([]map[string]any, len(entriesAny))
	for i, e := range entriesAny {
		entries[i], _ = e.(map[string]any)
	}
	// Windows carry zoned (UTC) start/end so Autotask displays them correctly.
	if entries[0]["startDateTime"] != "2026-06-15T11:00:00Z" || entries[0]["endDateTime"] != "2026-06-15T12:00:00Z" {
		t.Errorf("window 1 = %v..%v", entries[0]["startDateTime"], entries[0]["endDateTime"])
	}
	if entries[1]["startDateTime"] != "2026-06-15T13:00:00Z" || entries[1]["endDateTime"] != "2026-06-15T15:00:00Z" {
		t.Errorf("window 2 = %v..%v", entries[1]["startDateTime"], entries[1]["endDateTime"])
	}
	if asInt64(entries[0]["ticketID"]) != 100 {
		t.Errorf("ticketID = %v, want 100", entries[0]["ticketID"])
	}
}

func TestTimeAddRejectsContactFromOtherCompany(t *testing.T) {
	fc := &fakeClient{items: map[int64]map[string]any{
		300: {"id": float64(300), "companyID": float64(999), "isActive": 1},
	}}
	app := newTestApp(t, fc)
	app.cfg.ResourceID = 55

	_, err := app.cmdTimeAdd([]string{
		"--company", "0",
		"--title", "Website",
		"--desc", "Work on website",
		"--windows", "11-12",
		"--note", "Reviewed publish issue",
		"--contact", "300",
	})
	if err == nil || !strings.Contains(err.Error(), "belongs to company 999") {
		t.Fatalf("err = %v", err)
	}
	if len(fc.creates) != 0 {
		t.Fatalf("must not create ticket/time with cross-company contact: %+v", fc.creates)
	}
}

func TestTimeAddUsesConfiguredWorkTimezone(t *testing.T) {
	t.Setenv(envTimeZone, "Europe/Stockholm")
	app := newTestApp(t, &fakeClient{})
	app.cfg.ResourceID = 55
	app.now = func() time.Time {
		return time.Date(2026, 6, 25, 22, 30, 0, 0, time.UTC)
	}

	res, err := app.cmdTimeAdd([]string{"--ticket", "100", "--windows", "08-09", "--note", "n", "--dry-run"})
	if err != nil {
		t.Fatal(err)
	}
	d := dataMap(t, res)
	if d["date"] != "2026-06-26" {
		t.Fatalf("date = %v, want Stockholm today 2026-06-26", d["date"])
	}
	entriesAny, ok := d["entries"].([]any)
	if !ok || len(entriesAny) != 1 {
		t.Fatalf("entries = %v", d["entries"])
	}
	entry, _ := entriesAny[0].(map[string]any)
	if entry["startDateTime"] != "2026-06-26T06:00:00Z" || entry["endDateTime"] != "2026-06-26T07:00:00Z" {
		t.Fatalf("window = %v..%v, want 08-09 Europe/Stockholm as 06-07Z", entry["startDateTime"], entry["endDateTime"])
	}
}

func TestTimeAddCreatesEntriesAndCloses(t *testing.T) {
	fc := &fakeClient{}
	app := newTestApp(t, fc)
	app.cfg.ResourceID = 55
	app.cfg.Defaults.QueueID = 8

	res, err := app.cmdTimeAdd([]string{"--company", "0", "--date", "2026-06-15", "--windows", "11-12=A,13-15=B", "--desc", "project work", "--close"})
	if err != nil {
		t.Fatal(err)
	}

	var ticketCreates, timeCreates int
	for _, c := range fc.creates {
		switch c.entity {
		case "Tickets":
			ticketCreates++
		case "TimeEntries":
			timeCreates++
		}
	}
	if ticketCreates != 1 {
		t.Errorf("ticket creates = %d, want 1", ticketCreates)
	}
	if timeCreates != 2 {
		t.Errorf("time entry creates = %d, want 2", timeCreates)
	}
	// Both entries must point at the created ticket id.
	for _, c := range fc.creates {
		if c.entity == "TimeEntries" && asInt64(c.fields["ticketID"]) == 0 {
			t.Error("time entry not linked to created ticket")
		}
	}
	if len(fc.updates) != 1 {
		t.Errorf("expected 1 close update, got %d", len(fc.updates))
	}
	d := dataMap(t, res)
	if d["closed"] != true {
		t.Error("closed should be true")
	}
	ids, ok := d["timeEntryIds"].([]any)
	if !ok || len(ids) != 2 {
		t.Errorf("timeEntryIds = %v", d["timeEntryIds"])
	}
}

func TestTimeAddResumesPartialWriteFromJournal(t *testing.T) {
	fc := &fakeClient{failAt: map[string]int{atapi.EntityTimeEntries: 2}}
	app := newTestApp(t, fc)
	app.cfg.ResourceID = 55
	app.cfg.Defaults.QueueID = 8
	args := []string{"--company", "0", "--date", "2026-06-15", "--windows", "11-12=A,13-15=B", "--desc", "project work", "--close"}

	if _, err := app.cmdTimeAdd(args); err == nil {
		t.Fatal("expected first attempt to fail on second time entry")
	}
	fc.failAt = nil

	res, err := app.cmdTimeAdd(args)
	if err != nil {
		t.Fatal(err)
	}
	var ticketCreates, timeCreates int
	for _, c := range fc.creates {
		switch c.entity {
		case atapi.EntityTickets:
			ticketCreates++
		case atapi.EntityTimeEntries:
			timeCreates++
		}
	}
	if ticketCreates != 1 {
		t.Fatalf("ticket creates = %d want 1; creates=%+v", ticketCreates, fc.creates)
	}
	if timeCreates != 2 {
		t.Fatalf("time entry creates = %d want 2; creates=%+v", timeCreates, fc.creates)
	}
	if len(fc.updates) != 1 {
		t.Fatalf("close updates = %d want 1", len(fc.updates))
	}
	if dataMap(t, res)["closed"] != true {
		t.Fatal("retry result should be closed")
	}
}

func TestTimeAddCreateTicketIncludesIssueTypes(t *testing.T) {
	app := newTestApp(t, &fakeClient{})
	app.cfg.ResourceID = 55
	app.cfg.Defaults.QueueID = 8

	res, err := app.cmdTimeAdd([]string{
		"--company", "0",
		"--title", "Website",
		"--desc", "Work on website",
		"--windows", "11-12",
		"--note", "Reviewed publish issue",
		"--issue-type", "10",
		"--sub-issue-type", "200",
		"--contact", "300",
		"--dry-run",
	})
	if err != nil {
		t.Fatal(err)
	}
	createTicket, _ := dataMap(t, res)["createTicket"].(map[string]any)
	if asInt64(createTicket["issueType"]) != 10 || asInt64(createTicket["subIssueType"]) != 200 || asInt64(createTicket["contactID"]) != 300 {
		t.Fatalf("createTicket = %+v", createTicket)
	}
	data, ok := res.data.(TimeAddDryRun)
	if !ok {
		t.Fatalf("data = %T", res.data)
	}
	if warningsContain(data.Warnings, "classified") {
		t.Fatalf("did not expect classification warning, got %v", data.Warnings)
	}
	if warningsContain(data.Warnings, "contactID") {
		t.Fatalf("did not expect contact warning, got %v", data.Warnings)
	}
}

func TestTimeAddCreateTicketWarnsWithoutIssueTypes(t *testing.T) {
	app := newTestApp(t, &fakeClient{})
	app.cfg.ResourceID = 55

	res, err := app.cmdTimeAdd([]string{
		"--company", "0",
		"--title", "Website",
		"--desc", "Work on website",
		"--windows", "11-12",
		"--note", "Reviewed publish issue",
		"--dry-run",
	})
	if err != nil {
		t.Fatal(err)
	}
	data, ok := res.data.(TimeAddDryRun)
	if !ok {
		t.Fatalf("data = %T", res.data)
	}
	if !warningsContain(data.Warnings, "most new tickets should be classified") {
		t.Fatalf("expected missing-classification warning, got %v", data.Warnings)
	}
}

func TestTimeAddRejectsIssueTypesWithExistingTicket(t *testing.T) {
	app := newTestApp(t, &fakeClient{})
	app.cfg.ResourceID = 55
	cases := [][]string{
		{
			"--ticket", "100",
			"--windows", "11-12",
			"--note", "Reviewed publish issue",
			"--issue-type", "10",
			"--dry-run",
		},
		{
			"--ticket", "100",
			"--windows", "11-12",
			"--note", "Reviewed publish issue",
			"--contact", "300",
			"--dry-run",
		},
	}
	for _, args := range cases {
		if _, err := app.cmdTimeAdd(args); err == nil {
			t.Fatalf("expected ticket-field flags with existing --ticket to fail: %v", args)
		}
	}
}

func TestTimeAddValidation(t *testing.T) {
	app := newTestApp(t, &fakeClient{})

	// No resource id configured.
	if _, err := app.cmdTimeAdd([]string{"--ticket", "1", "--windows", "11-12", "--note", "n"}); err == nil {
		t.Error("expected error without a resource id")
	}

	app.cfg.ResourceID = 55
	// Missing windows.
	if _, err := app.cmdTimeAdd([]string{"--ticket", "1", "--note", "n"}); err == nil {
		t.Error("expected error for missing --windows")
	}
	// Neither ticket nor company.
	if _, err := app.cmdTimeAdd([]string{"--windows", "11-12", "--note", "n"}); err == nil {
		t.Error("expected error without --ticket or --company")
	}
	// Bad window.
	if _, err := app.cmdTimeAdd([]string{"--ticket", "1", "--windows", "nope", "--note", "n"}); err == nil {
		t.Error("expected error for bad window")
	}
	// Bad date.
	if _, err := app.cmdTimeAdd([]string{"--ticket", "1", "--date", "monday", "--windows", "11-12", "--note", "n"}); err == nil {
		t.Error("expected error for bad date")
	}
	// Creating a ticket (--company) without a description is rejected: the new
	// ticket would be customer-facing with a blank description.
	if _, err := app.cmdTimeAdd([]string{"--company", "0", "--windows", "11-12", "--note", "n"}); err == nil {
		t.Error("expected error creating a ticket without --desc")
	}
	// Logging against an existing --ticket needs no description (the ticket
	// already has one), so --desc must NOT be required on that path.
	if _, err := app.cmdTimeAdd([]string{"--ticket", "1", "--windows", "11-12", "--note", "n", "--dry-run"}); err != nil {
		t.Errorf("existing-ticket path should not require --desc: %v", err)
	}
}
