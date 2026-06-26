package main

import (
	"autotask-easymode/internal/atapi"
	"autotask-easymode/internal/timer"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCompanyAliasAndResolve(t *testing.T) {
	app := newTestApp(t, nil)
	res, err := app.cmdCompanyAlias([]string{"Acme Care", "42"})
	if err != nil {
		t.Fatal(err)
	}
	if res.action != "company.alias" {
		t.Errorf("action = %s", res.action)
	}
	got, err := app.cfg.ResolveCompany("acme care")
	if err != nil || got != 42 {
		t.Errorf("resolve = %d, %v", got, err)
	}
}

func TestCompanyAliasBadID(t *testing.T) {
	app := newTestApp(t, nil)
	if _, err := app.cmdCompanyAlias([]string{"x", "notanumber"}); err == nil {
		t.Fatal("expected error for non-numeric id")
	}
}

func TestCompanySearch(t *testing.T) {
	fc := &fakeClient{companies: []map[string]any{
		{"id": float64(7), "companyName": "Acme Care", "isActive": true},
	}}
	app := newTestApp(t, fc)
	res, err := app.cmdCompanySearch([]string{"Acme"})
	if err != nil {
		t.Fatal(err)
	}
	data := dataMap(t, res)
	if asInt64(data["count"]) != 1 {
		t.Errorf("count = %v", data["count"])
	}
}

func TestContactSearch(t *testing.T) {
	fc := &fakeClient{contacts: []map[string]any{
		{
			"id": float64(9), "companyID": float64(7),
			"firstName": "Anna", "lastName": "Andersson",
			"emailAddress": "anna@example.com", "isActive": 1,
			"phone": "08-123", "mobilePhone": "070-123",
		},
	}}
	app := newTestApp(t, fc)
	res, err := app.cmdContactSearch([]string{"--company", "7", "Anna"})
	if err != nil {
		t.Fatal(err)
	}
	data := dataMap(t, res)
	if asInt64(data["count"]) != 1 {
		t.Fatalf("count = %v", data["count"])
	}
	contacts, _ := data["contacts"].([]any)
	got, _ := contacts[0].(map[string]any)
	if got["email"] != "anna@example.com" {
		t.Fatalf("contact = %+v", got)
	}
	if !strings.Contains(asString(data["guidance"]), "contact_create") {
		t.Fatalf("guidance = %v", data["guidance"])
	}
}

func TestContactCreateDryRun(t *testing.T) {
	app := newTestApp(t, nil)
	res, err := app.cmdContactCreate([]string{
		"--company", "7",
		"--first-name", "Anna",
		"--last-name", "Andersson",
		"--email", "anna@example.com",
		"--dry-run",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.dryRun {
		t.Fatal("expected dryRun")
	}
	fields, _ := dataMap(t, res)["fields"].(map[string]any)
	if asInt64(fields["companyID"]) != 7 || fields["firstName"] != "Anna" || fields["emailAddress"] != "anna@example.com" || asInt64(fields["isActive"]) != 1 {
		t.Fatalf("fields = %+v", fields)
	}
}

func TestContactCreateValidation(t *testing.T) {
	app := newTestApp(t, nil)
	if _, err := app.cmdContactCreate([]string{"--company", "7", "--first-name", "Anna", "--last-name", "Andersson", "--email", "bad", "--dry-run"}); err == nil {
		t.Fatal("expected bad email to fail")
	}
	if _, err := app.cmdContactSearch([]string{"Anna"}); err == nil {
		t.Fatal("expected contact search without company to fail")
	}
}

func TestTicketCreateDryRun(t *testing.T) {
	app := newTestApp(t, nil) // dry-run needs no client
	res, err := app.cmdTicketCreate([]string{"--company", "123", "--title", "x", "--desc", "what it's about", "--dry-run"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.dryRun {
		t.Error("expected dryRun result")
	}
	data, ok := res.data.(TicketCreateDryRun)
	if !ok {
		t.Fatalf("data = %T", res.data)
	}
	if !warningsContain(data.Warnings, "most new tickets should be classified") {
		t.Fatalf("expected missing-classification warning, got %v", data.Warnings)
	}
}

func TestTicketIssueTypesGroupsSubIssues(t *testing.T) {
	fc := &fakeClient{fields: []atapi.Field{
		{Name: "issueType", PicklistValues: []atapi.PicklistValue{
			{Value: "10", Label: "Computer", IsActive: true},
			{Value: "11", Label: "Network", IsActive: false},
		}},
		{Name: "subIssueType", PicklistValues: []atapi.PicklistValue{
			{Value: "200", Label: "Laptop", ParentValue: "10", IsActive: true},
			{Value: "201", Label: "Inactive", ParentValue: "10", IsActive: false},
			{Value: "202", Label: "Orphan", ParentValue: "11", IsActive: true},
		}},
	}}
	app := newTestApp(t, fc)

	res, err := app.cmdTicketIssueTypes(nil)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := res.data.(TicketIssueTypesResult)
	if !ok {
		t.Fatalf("data = %T", res.data)
	}
	if got.Count != 1 || got.SubIssueCount != 1 {
		t.Fatalf("counts = %d/%d, want 1/1", got.Count, got.SubIssueCount)
	}
	if got.IssueTypes[0].ID != 10 || got.IssueTypes[0].Label != "Computer" {
		t.Fatalf("issue = %+v", got.IssueTypes[0])
	}
	subs := got.IssueTypes[0].SubIssueTypes
	if len(subs) != 1 || subs[0].ID != 200 || subs[0].IssueTypeID != 10 {
		t.Fatalf("subIssueTypes = %+v", subs)
	}
	if !strings.Contains(got.Guidance, "ask the user") {
		t.Errorf("guidance should tell agents to ask when ambiguous: %q", got.Guidance)
	}
}

func TestTicketCreateDryRunIncludesIssueTypes(t *testing.T) {
	app := newTestApp(t, nil)
	app.cfg.Defaults.QueueID = 8
	res, err := app.cmdTicketCreate([]string{
		"--company", "123",
		"--title", "x",
		"--desc", "what it's about",
		"--issue-type", "10",
		"--sub-issue-type", "200",
		"--contact", "300",
		"--dry-run",
	})
	if err != nil {
		t.Fatal(err)
	}
	fields, _ := dataMap(t, res)["fields"].(map[string]any)
	if asInt64(fields["issueType"]) != 10 || asInt64(fields["subIssueType"]) != 200 || asInt64(fields["contactID"]) != 300 {
		t.Fatalf("fields = %+v", fields)
	}
	data, ok := res.data.(TicketCreateDryRun)
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

func TestTicketCreateRejectsSubIssueWithoutIssueType(t *testing.T) {
	app := newTestApp(t, nil)
	if _, err := app.cmdTicketCreate([]string{
		"--company", "123",
		"--title", "x",
		"--desc", "what it's about",
		"--sub-issue-type", "200",
		"--dry-run",
	}); err == nil {
		t.Fatal("expected --sub-issue-type without --issue-type to fail")
	}
}

func TestTicketCreateReal(t *testing.T) {
	fc := &fakeClient{}
	app := newTestApp(t, fc)
	app.cfg.Defaults.QueueID = 8
	res, err := app.cmdTicketCreate([]string{"--company", "123", "--title", "hello", "--desc", "fixed the thing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(fc.creates) != 1 || fc.creates[0].entity != "Tickets" {
		t.Fatalf("creates = %+v", fc.creates)
	}
	if fc.creates[0].fields["queueID"] != 8 {
		t.Errorf("queueID = %v", fc.creates[0].fields["queueID"])
	}
	if asInt64(dataMap(t, res)["ticketId"]) == 0 {
		t.Error("expected a ticketId")
	}
}

func TestTicketFieldsAssignsResource(t *testing.T) {
	app := newTestApp(t, nil)
	app.cfg.ResourceID = 1001
	app.cfg.Defaults.RoleID = 1002
	fields, _ := app.ticketFields(0, "x", "")
	if asInt64(fields["assignedResourceID"]) != 1001 {
		t.Errorf("assignedResourceID = %v", fields["assignedResourceID"])
	}
	if asInt64(fields["assignedResourceRoleID"]) != 1002 {
		t.Errorf("assignedResourceRoleID = %v", fields["assignedResourceRoleID"])
	}
}

func TestTicketFieldsNoAssignWithoutRole(t *testing.T) {
	app := newTestApp(t, nil)
	app.cfg.ResourceID = 1001 // role left unset
	fields, _ := app.ticketFields(0, "x", "")
	if _, ok := fields["assignedResourceID"]; ok {
		t.Error("must not assign a resource without a role (Autotask requires the role)")
	}
}

func TestTicketCreateMissingTitle(t *testing.T) {
	app := newTestApp(t, nil)
	if _, err := app.cmdTicketCreate([]string{"--company", "123"}); err == nil {
		t.Fatal("expected error for missing title")
	}
}

func TestTicketCreateMissingDesc(t *testing.T) {
	app := newTestApp(t, nil)
	// A title but no description must be rejected before any write: an empty
	// ticket description is customer-facing and looks unprofessional.
	if _, err := app.cmdTicketCreate([]string{"--company", "123", "--title", "x"}); err == nil {
		t.Fatal("expected error for missing --desc")
	}
	// Whitespace-only is still blank.
	if _, err := app.cmdTicketCreate([]string{"--company", "123", "--title", "x", "--desc", "   "}); err == nil {
		t.Fatal("expected error for whitespace-only --desc")
	}
}

func TestTicketCloseReal(t *testing.T) {
	fc := &fakeClient{}
	app := newTestApp(t, fc)
	app.cfg.Defaults.TicketStatusComplete = 5
	if _, err := app.cmdTicketClose([]string{"4242"}); err != nil {
		t.Fatal(err)
	}
	if len(fc.updates) != 1 || asInt64(fc.updates[0]["id"]) != 4242 || fc.updates[0]["status"] != 5 {
		t.Errorf("updates = %+v", fc.updates)
	}
}

func TestTimerStartDryRunDoesNotMutate(t *testing.T) {
	app := newTestApp(t, nil)
	res, err := app.cmdTimerStart([]string{"--company", "123", "--title", "map", "--dry-run"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.dryRun {
		t.Error("expected dryRun")
	}
	if len(app.state.Sessions) != 0 {
		t.Error("dry-run must not create a session")
	}
}

func TestTimerStartCreatesTicketAndSession(t *testing.T) {
	fc := &fakeClient{}
	app := newTestApp(t, fc)
	app.cfg.Defaults.QueueID = 8
	if _, err := app.cmdTimerStart([]string{"--company", "123", "--title", "map first day"}); err != nil {
		t.Fatal(err)
	}
	if len(fc.creates) != 1 || fc.creates[0].entity != "Tickets" {
		t.Fatalf("expected 1 ticket create, got %+v", fc.creates)
	}
	if len(app.state.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(app.state.Sessions))
	}
	if app.state.Sessions[0].TicketID == 0 {
		t.Error("session should carry the created ticket id")
	}
}

func TestTimerStartNoTicket(t *testing.T) {
	app := newTestApp(t, &fakeClient{})
	if _, err := app.cmdTimerStart([]string{"--company", "123", "--title", "x", "--no-ticket"}); err != nil {
		t.Fatal(err)
	}
	if app.state.Sessions[0].TicketID != 0 {
		t.Error("expected no ticket with --no-ticket")
	}
}

func TestTimerStartAcceptsCompanyZero(t *testing.T) {
	app := newTestApp(t, &fakeClient{})
	// 0 is a valid Autotask company (the owner org) — it must not be treated as
	// "no company given".
	if _, err := app.cmdTimerStart([]string{"--company", "0", "--title", "x", "--no-ticket"}); err != nil {
		t.Fatalf("company 0 should be accepted: %v", err)
	}
	if len(app.state.Sessions) != 1 || app.state.Sessions[0].CompanyID != 0 {
		t.Errorf("expected one session on company 0, got %+v", app.state.Sessions)
	}
}

func TestReportAcceptsCompanyZero(t *testing.T) {
	fc := &fakeClient{tickets: []map[string]any{{"id": float64(5), "title": "T"}}}
	app := newTestApp(t, fc)
	if _, err := app.cmdReport([]string{"--company", "0"}); err != nil {
		t.Fatalf("report --company 0 should work: %v", err)
	}
}

func TestTimeEntryFieldsIncludesWindow(t *testing.T) {
	app := newTestApp(t, nil) // now is fixed to testNow
	f := app.timeEntryFields(55, 100, 2, "x", testNow)
	// Service tickets require a start/stop window matching the hours, sent with an
	// explicit zone so Autotask records the right instant.
	if f["endDateTime"] != testNow.UTC().Format(dateTimeZoned) {
		t.Errorf("end = %v, want %v", f["endDateTime"], testNow.UTC().Format(dateTimeZoned))
	}
	if f["startDateTime"] != testNow.Add(-2*time.Hour).UTC().Format(dateTimeZoned) {
		t.Errorf("start = %v", f["startDateTime"])
	}
}

func TestWorkedAnchor(t *testing.T) {
	app := newTestApp(t, nil) // now is fixed to testNow

	// No date falls back to the current time (log-as-you-go).
	got, err := app.workedAnchor("")
	if err != nil {
		t.Fatalf("empty date: %v", err)
	}
	if !got.Equal(testNow) {
		t.Errorf("empty date anchor = %v, want %v", got, testNow)
	}

	// A given date anchors the window at the end of that business day.
	got, err = app.workedAnchor("2026-06-15")
	if err != nil {
		t.Fatalf("valid date: %v", err)
	}
	want := time.Date(2026, 6, 15, workdayEndHour, 0, 0, 0, testNow.Location())
	if !got.Equal(want) {
		t.Errorf("dated anchor = %v, want %v", got, want)
	}

	// Garbage is rejected with a hinted error rather than silently logged today.
	if _, err := app.workedAnchor("monday"); err == nil {
		t.Error("expected error for invalid --date")
	}
}

func TestWorkedAnchorUsesConfiguredWorkTimezone(t *testing.T) {
	t.Setenv(envTimeZone, "Europe/Stockholm")
	app := newTestApp(t, nil)
	app.now = func() time.Time {
		return time.Date(2026, 6, 25, 22, 30, 0, 0, time.UTC)
	}

	got, err := app.workedAnchor("")
	if err != nil {
		t.Fatalf("empty date: %v", err)
	}
	if got.Location().String() != "Europe/Stockholm" {
		t.Fatalf("location = %v, want Europe/Stockholm", got.Location())
	}
	if y, m, d := got.Date(); y != 2026 || m != 6 || d != 26 {
		t.Fatalf("local date = %04d-%02d-%02d, want 2026-06-26", y, m, d)
	}

	got, err = app.workedAnchor("2026-06-26")
	if err != nil {
		t.Fatalf("dated anchor: %v", err)
	}
	want := time.Date(2026, 6, 26, workdayEndHour, 0, 0, 0, got.Location())
	if !got.Equal(want) {
		t.Fatalf("dated anchor = %v, want %v", got, want)
	}
}

func TestTimerStopBackdates(t *testing.T) {
	fc := &fakeClient{}
	app := newTestApp(t, fc)
	app.cfg.ResourceID = 55
	s := app.state.Start("a", 0, "A", testNow, false)
	s.TicketID = 4242

	if _, err := app.cmdTimerStop([]string{"s1", "--hours", "3", "--date", "2026-06-15", "--note", "x"}); err != nil {
		t.Fatal(err)
	}

	var te *createCall
	for i := range fc.creates {
		if fc.creates[i].entity == "TimeEntries" {
			te = &fc.creates[i]
		}
	}
	if te == nil {
		t.Fatal("no time entry created")
	}
	wantEnd := time.Date(2026, 6, 15, workdayEndHour, 0, 0, 0, testNow.Location()).UTC().Format(dateTimeZoned)
	if te.fields["endDateTime"] != wantEnd {
		t.Errorf("end = %v, want %v", te.fields["endDateTime"], wantEnd)
	}
	wantStart := time.Date(2026, 6, 15, workdayEndHour-3, 0, 0, 0, testNow.Location()).UTC().Format(dateTimeZoned)
	if te.fields["startDateTime"] != wantStart {
		t.Errorf("start = %v, want %v", te.fields["startDateTime"], wantStart)
	}
	if !strings.HasPrefix(asString(te.fields["dateWorked"]), "2026-06-15") {
		t.Errorf("dateWorked = %v, want 2026-06-15", te.fields["dateWorked"])
	}
}

func TestTimerStopRejectsBadDate(t *testing.T) {
	app := newTestApp(t, &fakeClient{})
	app.cfg.ResourceID = 55
	s := app.state.Start("a", 0, "A", testNow, false)
	s.TicketID = 4242
	if _, err := app.cmdTimerStop([]string{"s1", "--hours", "3", "--date", "nope"}); err == nil {
		t.Fatal("expected error for invalid --date")
	}
}

func TestTimerStopRequiresResource(t *testing.T) {
	app := newTestApp(t, &fakeClient{})
	app.state.Start("a", 123, "A", testNow, false)
	if _, err := app.cmdTimerStop([]string{"s1", "--hours", "2"}); err == nil {
		t.Fatal("expected error without a configured resource id")
	}
}

func TestTimerStopLogsTimeAndCloses(t *testing.T) {
	fc := &fakeClient{}
	app := newTestApp(t, fc)
	app.cfg.ResourceID = 55
	app.cfg.Defaults.QueueID = 8
	s := app.state.Start("a", 123, "A", testNow, false)
	s.TicketID = 4242
	s.AddNote("did the thing")

	res, err := app.cmdTimerStop([]string{"s1", "--hours", "2.5", "--close", "--note", "wrapped up"})
	if err != nil {
		t.Fatal(err)
	}

	var te *createCall
	for i := range fc.creates {
		if fc.creates[i].entity == "TimeEntries" {
			te = &fc.creates[i]
		}
	}
	if te == nil {
		t.Fatal("no time entry created")
	}
	if asFloat(te.fields["hoursWorked"]) != 2.5 {
		t.Errorf("hours = %v", te.fields["hoursWorked"])
	}
	if asInt64(te.fields["ticketID"]) != 4242 {
		t.Errorf("ticketID = %v", te.fields["ticketID"])
	}
	if asInt64(te.fields["resourceID"]) != 55 {
		t.Errorf("resourceID = %v", te.fields["resourceID"])
	}
	notes := asString(te.fields["summaryNotes"])
	if !strings.Contains(notes, "did the thing") || !strings.Contains(notes, "wrapped up") {
		t.Errorf("notes = %q", notes)
	}
	if len(fc.updates) != 1 {
		t.Errorf("expected 1 close update, got %d", len(fc.updates))
	}
	if len(app.state.Sessions) != 0 {
		t.Error("session should be removed after stop")
	}
	if dataMap(t, res)["closed"] != true {
		t.Error("closed should be true")
	}
}

func TestTimerStopCreatesTicketWhenMissing(t *testing.T) {
	fc := &fakeClient{}
	app := newTestApp(t, fc)
	app.cfg.ResourceID = 55
	app.cfg.Defaults.QueueID = 8
	app.state.Start("a", 123, "A", testNow, false) // no ticket attached
	if _, err := app.cmdTimerStop([]string{"s1", "--hours", "1", "--note", "x"}); err != nil {
		t.Fatal(err)
	}
	var ticketCreated, teCreated bool
	for _, c := range fc.creates {
		switch c.entity {
		case "Tickets":
			ticketCreated = true
		case "TimeEntries":
			teCreated = true
		}
	}
	if !ticketCreated || !teCreated {
		t.Errorf("expected ticket+time entry creation; creates=%+v", fc.creates)
	}
}

func TestTimerStopCreatesTicketForCompanyZero(t *testing.T) {
	fc := &fakeClient{}
	app := newTestApp(t, fc)
	app.cfg.ResourceID = 55
	app.cfg.Defaults.QueueID = 8
	// 0 is the owner org, a valid company — a no-ticket session on it must still
	// create a ticket on stop, not be rejected as "no company".
	app.state.Start("a", 0, "A", testNow, false)
	if _, err := app.cmdTimerStop([]string{"s1", "--hours", "1", "--note", "x"}); err != nil {
		t.Fatalf("company 0 should create a ticket: %v", err)
	}
	var ticketCreated bool
	for _, c := range fc.creates {
		if c.entity == "Tickets" {
			ticketCreated = true
		}
	}
	if !ticketCreated {
		t.Errorf("expected a ticket to be created for company 0; creates=%+v", fc.creates)
	}
}

func TestTimerStopNoCompanyNoTicketErrors(t *testing.T) {
	app := newTestApp(t, &fakeClient{})
	app.cfg.ResourceID = 55
	app.state.Start("a", timer.NoCompany, "A", testNow, false)
	if _, err := app.cmdTimerStop([]string{"s1", "--hours", "1", "--note", "x"}); err == nil {
		t.Fatal("expected error when session has neither ticket nor company")
	}
}

func TestTimerStartTicketHasNoCompany(t *testing.T) {
	app := newTestApp(t, &fakeClient{})
	// Attaching to an existing ticket without --company leaves the session with
	// NoCompany, not 0 (which would be the owner org).
	if _, err := app.cmdTimerStart([]string{"--ticket", "999"}); err != nil {
		t.Fatal(err)
	}
	s := app.state.Sessions[0]
	if s.CompanyID != timer.NoCompany {
		t.Errorf("CompanyID = %d, want NoCompany (%d)", s.CompanyID, timer.NoCompany)
	}
	if s.TicketID != 999 {
		t.Errorf("TicketID = %d, want 999", s.TicketID)
	}
}

func TestTimerStopRequiresNote(t *testing.T) {
	app := newTestApp(t, &fakeClient{})
	app.cfg.ResourceID = 55
	s := app.state.Start("a", 123, "A", testNow, false)
	s.TicketID = 1
	// No session notes and no --note: Autotask would 500 on a blank summaryNotes,
	// so atem must reject it locally with a hint first.
	if _, err := app.cmdTimerStop([]string{"s1", "--hours", "1"}); err == nil {
		t.Fatal("expected error when the time entry would have a blank note")
	}
	// With a note it succeeds.
	if _, err := app.cmdTimerStop([]string{"s1", "--hours", "1", "--note", "did the thing"}); err != nil {
		t.Fatalf("note provided, should succeed: %v", err)
	}
}

func TestTicketSearchScopesCompanyZero(t *testing.T) {
	fc := &fakeClient{}
	app := newTestApp(t, fc)
	// --company 0 must scope to company 0, not search across all companies.
	if _, err := app.cmdTicketSearch([]string{"--company", "0", "x"}); err != nil {
		t.Fatal(err)
	}
	if fc.searchCompany != 0 {
		t.Errorf("company 0 search scoped to %d, want 0", fc.searchCompany)
	}
	// No --company searches all companies.
	if _, err := app.cmdTicketSearch([]string{"y"}); err != nil {
		t.Fatal(err)
	}
	if fc.searchCompany != atapi.AllCompanies {
		t.Errorf("unscoped search = %d, want AllCompanies (%d)", fc.searchCompany, atapi.AllCompanies)
	}
}

func TestTimerStopDryRunDoesNotMutate(t *testing.T) {
	app := newTestApp(t, &fakeClient{})
	app.cfg.ResourceID = 55
	s := app.state.Start("a", 123, "A", testNow, false)
	s.TicketID = 1
	res, err := app.cmdTimerStop([]string{"s1", "--hours", "1", "--note", "x", "--dry-run"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.dryRun {
		t.Error("expected dryRun")
	}
	if len(app.state.Sessions) != 1 {
		t.Error("dry-run must not remove the session")
	}
}

func TestTimerStatusPauseResumeSwitchNote(t *testing.T) {
	app := newTestApp(t, nil)
	app.state.Start("a", 1, "A", testNow, true)
	app.state.Start("b", 2, "B", testNow, true)

	if _, err := app.cmdTimerStatus(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := app.cmdTimerPause([]string{"s1"}); err != nil {
		t.Fatal(err)
	}
	if app.state.Find("s1").Running() {
		t.Error("s1 should be paused")
	}
	if _, err := app.cmdTimerResume([]string{"s1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.cmdTimerSwitch([]string{"s2"}); err != nil {
		t.Fatal(err)
	}
	if app.state.Find("s1").Running() {
		t.Error("s1 should pause after switching to s2")
	}
	if _, err := app.cmdTimerNote([]string{"s2", "hello", "world"}); err != nil {
		t.Fatal(err)
	}
	if got := app.state.Find("s2").JoinedNotes(); got != "hello world" {
		t.Errorf("note = %q", got)
	}
}

func TestTimerSwitchNeedsArg(t *testing.T) {
	app := newTestApp(t, nil)
	if _, err := app.cmdTimerSwitch(nil); err == nil {
		t.Fatal("switch without session id should error")
	}
}

func TestReportAggregatesAndRendersMarkdown(t *testing.T) {
	fc := &fakeClient{
		tickets: []map[string]any{
			{"id": float64(10), "title": "Setup"},
			{"id": float64(11), "title": "Migration"},
		},
		entries: []map[string]any{
			{"ticketID": float64(10), "hoursWorked": float64(2), "dateWorked": "2026-06-01T09:00:00", "summaryNotes": "a"},
			{"ticketID": float64(10), "hoursWorked": 1.5, "dateWorked": "2026-06-02T09:00:00", "summaryNotes": "b"},
			{"ticketID": float64(11), "hoursWorked": float64(3), "dateWorked": "2026-06-03T09:00:00", "summaryNotes": "c"},
		},
	}
	app := newTestApp(t, fc)
	res, err := app.cmdReport([]string{"--company", "123", "--format", "md"})
	if err != nil {
		t.Fatal(err)
	}
	data := dataMap(t, res)
	if asFloat(data["totalHours"]) != 6.5 {
		t.Errorf("total = %v", data["totalHours"])
	}
	if asInt64(data["ticketCount"]) != 2 {
		t.Errorf("ticketCount = %v", data["ticketCount"])
	}
	md, _ := data["markdown"].(string)
	if !strings.Contains(md, "Tidsrapport") {
		t.Errorf("markdown missing header: %s", md)
	}
}

func TestReportMatchAccountWide(t *testing.T) {
	fc := &fakeClient{
		tickets: []map[string]any{{"id": float64(900), "title": "Migration planning"}},
		entries: []map[string]any{
			{"ticketID": float64(900), "hoursWorked": float64(4), "dateWorked": "2026-06-01T09:00:00", "summaryNotes": "x"},
		},
	}
	app := newTestApp(t, fc)
	res, err := app.cmdReport([]string{"--match", "Migration", "--format", "md"})
	if err != nil {
		t.Fatal(err)
	}
	data := dataMap(t, res)
	if data["match"] != "Migration" {
		t.Errorf("match = %v", data["match"])
	}
	if asFloat(data["totalHours"]) != 4 {
		t.Errorf("total = %v", data["totalHours"])
	}
	if md, _ := data["markdown"].(string); !strings.Contains(md, "Migration") {
		t.Errorf("markdown missing scope: %s", md)
	}
}

func TestReportOutWritesFile(t *testing.T) {
	fc := &fakeClient{
		tickets: []map[string]any{{"id": float64(900), "title": "Migration"}},
		entries: []map[string]any{
			{"ticketID": float64(900), "hoursWorked": float64(2), "dateWorked": "2026-06-01T09:00:00", "summaryNotes": "x"},
		},
	}
	app := newTestApp(t, fc)
	path := filepath.Join(t.TempDir(), "report.md")
	res, err := app.cmdReport([]string{"--match", "Migration", "--format", "md", "--out", path})
	if err != nil {
		t.Fatal(err)
	}
	if dataMap(t, res)["writtenTo"] != path {
		t.Errorf("writtenTo = %v", dataMap(t, res)["writtenTo"])
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "Tidsrapport") {
		t.Errorf("unexpected file content: %s", b)
	}
}

func TestFlagEntry(t *testing.T) {
	app := newTestApp(t, nil) // defaults: thin = > 5 h & < 80 chars; large = >= 12 h
	if app.flagEntry(8, "Excelark uppladdat.") != "thin" {
		t.Error("8h + short note should be thin")
	}
	if app.flagEntry(5, "kort") != "" {
		t.Error("exactly 5h should not flag (needs > 5)")
	}
	if app.flagEntry(8, strings.Repeat("x", 100)) != "" {
		t.Error("8h + long note should not flag")
	}
	if app.flagEntry(20, strings.Repeat("x", 500)) != "large" {
		t.Error("20h should flag large regardless of note")
	}
	if app.flagEntry(12, strings.Repeat("x", 500)) != "large" {
		t.Error("12h should flag large (>= threshold)")
	}
	app.cfg.Defaults.FlagHoursOver = 10
	if app.flagEntry(8, "kort") != "" {
		t.Error("with thin-threshold raised to 10h, 8h should not flag")
	}
}

func TestReportFlagsThinAndLargeEntries(t *testing.T) {
	fc := &fakeClient{
		tickets: []map[string]any{{"id": float64(900), "title": "Migration"}},
		entries: []map[string]any{
			{"ticketID": float64(900), "hoursWorked": float64(8), "dateWorked": "2026-02-02T00:00:00Z", "summaryNotes": "Excelark uppladdat."},                    // thin
			{"ticketID": float64(900), "hoursWorked": float64(8), "dateWorked": "2026-02-03T00:00:00Z", "summaryNotes": strings.Repeat("detaljerat arbete ", 10)}, // ok
			{"ticketID": float64(900), "hoursWorked": float64(2), "dateWorked": "2026-02-04T00:00:00Z", "summaryNotes": "kort"},                                   // ok
			{"ticketID": float64(900), "hoursWorked": float64(20), "dateWorked": "2026-02-05T00:00:00Z", "summaryNotes": strings.Repeat("val beskrivet ", 20)},    // large
		},
	}
	app := newTestApp(t, fc)
	res, err := app.cmdReport([]string{"--match", "Migration"})
	if err != nil {
		t.Fatal(err)
	}
	flagged, ok := dataMap(t, res)["flagged"].([]any)
	if !ok {
		t.Fatalf("flagged missing or wrong type")
	}
	if len(flagged) != 2 {
		t.Fatalf("expected 2 flagged entries, got %d", len(flagged))
	}
	reasons := map[string]bool{}
	for _, fa := range flagged {
		f, _ := fa.(map[string]any)
		reasons[asString(f["reason"])] = true
	}
	if !reasons["thin"] || !reasons["large"] {
		t.Errorf("expected both thin and large reasons, got %+v", flagged)
	}
}

func TestReportEmpty(t *testing.T) {
	app := newTestApp(t, &fakeClient{})
	res, err := app.cmdReport([]string{"--company", "123"})
	if err != nil {
		t.Fatal(err)
	}
	if asInt64(dataMap(t, res)["ticketCount"]) != 0 {
		t.Error("expected zero tickets")
	}
}

func TestReportBadDate(t *testing.T) {
	app := newTestApp(t, &fakeClient{})
	if _, err := app.cmdReport([]string{"--company", "123", "--from", "2026/01/01"}); err == nil {
		t.Fatal("expected invalid date error")
	}
}

func TestConfigSetAndShow(t *testing.T) {
	app := newTestApp(t, nil)
	if _, err := app.cmdConfigSet([]string{"queueId", "9"}); err != nil {
		t.Fatal(err)
	}
	if app.cfg.Defaults.QueueID != 9 {
		t.Errorf("queueID = %d", app.cfg.Defaults.QueueID)
	}
	if _, err := app.cmdConfigSet([]string{"unknownKey", "1"}); err == nil {
		t.Fatal("expected error for unknown key")
	}
	res, err := app.cmdConfigShow(nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.action != "config.show" {
		t.Errorf("action = %s", res.action)
	}
}

func TestCompanySearchLimitAfterQueryRegression(t *testing.T) {
	fc := &fakeClient{companies: []map[string]any{{"id": float64(1), "companyName": "X"}}}
	app := newTestApp(t, fc)
	res, err := app.cmdCompanySearch([]string{"Acme", "Care", "--limit", "5"})
	if err != nil {
		t.Fatal(err)
	}
	if dataMap(t, res)["query"] != "Acme Care" {
		t.Errorf("flag leaked into query: %v", dataMap(t, res)["query"])
	}
}

func TestTicketSearch(t *testing.T) {
	fc := &fakeClient{tickets: []map[string]any{
		{"id": float64(900), "title": "Migration planning", "companyID": float64(1050), "ticketNumber": "T2026.001", "status": float64(1)},
	}}
	app := newTestApp(t, fc)
	res, err := app.cmdTicketSearch([]string{"Migration"})
	if err != nil {
		t.Fatal(err)
	}
	data := dataMap(t, res)
	if asInt64(data["count"]) != 1 {
		t.Errorf("count = %v", data["count"])
	}
}

func TestTicketSearchCompanyAlias(t *testing.T) {
	app := newTestApp(t, &fakeClient{})
	app.cfg.SetAlias("acme", 1050)
	if _, err := app.cmdTicketSearch([]string{"--company", "acme", "Migration"}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.cmdTicketSearch([]string{"--company", "nope", "Migration"}); err == nil {
		t.Fatal("expected unknown-company error")
	}
}

func TestTicketSearchNeedsQuery(t *testing.T) {
	app := newTestApp(t, &fakeClient{})
	if _, err := app.cmdTicketSearch([]string{"--company", "acme"}); err == nil {
		t.Fatal("expected error for missing query")
	}
}

func TestResourceSearch(t *testing.T) {
	fc := &fakeClient{resources: []map[string]any{
		{"id": float64(55), "firstName": "Alex", "lastName": "Example", "email": "alex@example.test", "isActive": true},
	}}
	app := newTestApp(t, fc)
	res, err := app.cmdResourceSearch([]string{"Alex"})
	if err != nil {
		t.Fatal(err)
	}
	if asInt64(dataMap(t, res)["count"]) != 1 {
		t.Errorf("count = %v", dataMap(t, res)["count"])
	}
}

func TestTicketShow(t *testing.T) {
	fc := &fakeClient{items: map[int64]map[string]any{7: {"id": float64(7), "title": "Hello"}}}
	app := newTestApp(t, fc)
	res, err := app.cmdTicketShow([]string{"7"})
	if err != nil {
		t.Fatal(err)
	}
	if asString(dataMap(t, res)["title"]) != "Hello" {
		t.Errorf("title = %v", dataMap(t, res)["title"])
	}
}

func TestDefaultClientMissingCredentials(t *testing.T) {
	app := newTestApp(t, nil)
	if _, err := app.defaultClient(context.Background()); err == nil {
		t.Fatal("expected missing-credentials error")
	}
}

func TestMissingArgErrors(t *testing.T) {
	app := newTestApp(t, &fakeClient{})
	cases := []struct {
		name string
		fn   func() (*cmdResult, error)
	}{
		{"company search empty", func() (*cmdResult, error) { return app.cmdCompanySearch(nil) }},
		{"resource search empty", func() (*cmdResult, error) { return app.cmdResourceSearch(nil) }},
		{"timer start no company", func() (*cmdResult, error) { return app.cmdTimerStart([]string{"--title", "x"}) }},
		{"report no company", func() (*cmdResult, error) { return app.cmdReport(nil) }},
		{"ticket close bad id", func() (*cmdResult, error) { return app.cmdTicketClose([]string{"abc"}) }},
		{"timer note empty", func() (*cmdResult, error) { return app.cmdTimerNote(nil) }},
		{"config set wrong args", func() (*cmdResult, error) { return app.cmdConfigSet([]string{"only-one"}) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := c.fn(); err == nil {
				t.Errorf("%s: expected error", c.name)
			}
		})
	}
}

func TestConfigDoctor(t *testing.T) {
	fc := &fakeClient{
		fields: []atapi.Field{
			{Name: "status", IsPickList: true, PicklistValues: []atapi.PicklistValue{
				{Value: "1", Label: "New", IsActive: true},
				{Value: "5", Label: "Complete", IsActive: true},
				{Value: "9", Label: "Retired", IsActive: false},
			}},
			{Name: "queueID", IsPickList: true, PicklistValues: []atapi.PicklistValue{
				{Value: "8", Label: "Triage", IsActive: true},
			}},
		},
		billingCodes: []map[string]any{{"id": float64(14), "name": "IT Consulting", "useType": float64(1)}},
	}
	app := newTestApp(t, fc)
	app.cfg.Username, app.cfg.Secret, app.cfg.IntegrationCode = "u", "s", "i" // pass the credentials gate
	res, err := app.cmdConfigDoctor(nil)
	if err != nil {
		t.Fatal(err)
	}
	pick, ok := dataMap(t, res)["picklists"].(map[string]any)
	if !ok {
		t.Fatalf("picklists missing")
	}
	if status, _ := pick["ticketStatus"].([]any); len(status) != 2 {
		t.Errorf("expected 2 active statuses, got %d", len(status))
	}
	if wt, _ := pick["workTypes"].([]any); len(wt) != 1 {
		t.Errorf("expected 1 work type, got %d", len(wt))
	}
}

func TestConfigDoctorNeedsCredentials(t *testing.T) {
	app := newTestApp(t, &fakeClient{})
	res, err := app.cmdConfigDoctor(nil)
	if err != nil {
		t.Fatal(err)
	}
	if creds, _ := dataMap(t, res)["credentials"].(map[string]any); creds["ok"] != false {
		t.Errorf("expected credentials ok=false, got %v", creds["ok"])
	}
	if _, hasPick := dataMap(t, res)["picklists"]; hasPick {
		t.Error("should not reach picklists without credentials")
	}
}

func TestDispatchRouting(t *testing.T) {
	app := newTestApp(t, nil)
	if _, err := app.dispatch([]string{"bogus"}); err == nil {
		t.Fatal("expected unknown command error")
	}
	if _, err := app.dispatch([]string{"timer", "bogus"}); err == nil {
		t.Fatal("expected unknown timer subcommand error")
	}
	if _, err := app.dispatch([]string{"company"}); err == nil {
		t.Fatal("expected missing subcommand error")
	}
}

func TestTicketPlanModes(t *testing.T) {
	app := newTestApp(t, nil)
	app.cfg.Defaults.QueueID = 8
	if plan, fields := app.ticketPlan(1, "t", "", 0, false); fields == nil || plan["mode"] != "create" {
		t.Errorf("create plan = %+v", plan)
	}
	if plan, fields := app.ticketPlan(1, "t", "", 99, false); fields != nil || plan["mode"] != "attach" {
		t.Errorf("attach plan = %+v", plan)
	}
	if plan, fields := app.ticketPlan(1, "t", "", 0, true); fields != nil || plan["mode"] != "none" {
		t.Errorf("none plan = %+v", plan)
	}
}

func TestAggregateEntriesSortedAndSummed(t *testing.T) {
	entries := []map[string]any{
		{"ticketID": float64(2), "hoursWorked": float64(1)},
		{"ticketID": float64(1), "hoursWorked": float64(2)},
		{"ticketID": float64(1), "hoursWorked": 0.5},
	}
	out, total := aggregateEntries(entries, map[int64]string{1: "One", 2: "Two"})
	if total != 3.5 {
		t.Errorf("total = %v", total)
	}
	if len(out) != 2 {
		t.Fatalf("groups = %d", len(out))
	}
	if out[0].TicketID != 1 {
		t.Errorf("first group should be ticket 1, got %v", out[0].TicketID)
	}
	if out[0].TotalHours != 2.5 {
		t.Errorf("ticket 1 total = %v", out[0].TotalHours)
	}
}

func TestRenderReportMarkdown(t *testing.T) {
	tickets := []ReportTicket{
		{
			TicketID:   1,
			Title:      "T",
			TotalHours: 2.0,
			Entries:    []ReportEntry{{Date: "2026-06-01", Hours: 2.0, Notes: "did"}},
		},
	}
	md := renderReportMarkdown("Acme", "", "2026-06-01", "2026-06-30", 2.0, tickets)
	for _, want := range []string{"# Tidsrapport – Acme", "Period:", "## Ärende 1 – T", "did"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
}
