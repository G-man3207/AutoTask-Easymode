package main

import (
	"autotask-easymode/internal/atapi"
	"autotask-easymode/internal/timer"
	"strings"
	"time"
)

func (a *App) sessionView(s *timer.Session) SessionView {
	now := a.now()
	return SessionView{
		ID:        s.ID,
		Label:     s.Label,
		CompanyID: s.CompanyID,
		TicketID:  s.TicketID,
		Title:     s.Title,
		Running:   s.Running(),
		Hours:     s.Hours(now),
		Notes:     s.Notes,
	}
}

// resolveSession picks the session named by arg, or the single running session
// when arg is empty.
func (a *App) resolveSession(arg string) (*timer.Session, error) {
	if arg != "" {
		s := a.state.Find(arg)
		if s == nil {
			return nil, hinted("run `atem timer status` to list sessions", "no session %q", arg)
		}
		return s, nil
	}
	s := a.state.Active()
	if s == nil {
		return nil, hinted("specify a session id, e.g. `atem timer stop s1`", "no single active session to act on")
	}
	return s, nil
}

func (a *App) cmdTimerStart(args []string) (*cmdResult, error) {
	fs := newFlagSet("timer start")
	company := fs.String("company", "", "customer alias or companyID")
	title := fs.String("title", "", "ticket title")
	desc := fs.String("desc", "", "ticket description")
	note := fs.String("note", "", "initial work note")
	existingTicket := fs.Int64("ticket", 0, "attach to an existing ticket id instead of creating one")
	noTicket := fs.Bool("no-ticket", false, "do not create or attach a ticket yet")
	keepOthers := fs.Bool("keep-others", false, "keep other timers running")
	dryRun := fs.Bool("dry-run", false, "preview without writing")
	if err := fs.Parse(args); err != nil {
		return nil, usageErr("timer start", err)
	}

	companyGiven := strings.TrimSpace(*company) != ""
	// NoCompany (not 0) marks "unset": 0 is a valid Autotask company id (the
	// owner organization). A session with no company is one attached to an
	// existing ticket.
	companyID := timer.NoCompany
	if companyGiven {
		id, err := a.cfg.ResolveCompany(*company)
		if err != nil {
			return nil, err
		}
		companyID = id
	}
	// Use the flag presence (not companyID == 0) as the sentinel: 0 is a valid
	// Autotask company id (the owner organization).
	if !companyGiven && *existingTicket == 0 {
		return nil, hinted(`e.g. atem timer start --company acme --title "map first day"`, "missing --company (or --ticket)")
	}

	ticketTitle := strings.TrimSpace(*title)
	if ticketTitle == "" {
		ticketTitle = "Arbete " + a.now().Format(dateLayout)
	}
	label := strings.TrimSpace(*title)
	if label == "" {
		label = strings.TrimSpace(*company)
	}
	if label == "" {
		label = ticketTitle
	}

	plan, createFields := a.ticketPlan(companyID, ticketTitle, *desc, *existingTicket, *noTicket)
	if *dryRun {
		return &cmdResult{action: "timer.start", dryRun: true, data: TimerStartDryRun{
			WouldStartSession: map[string]any{"label": label, "companyId": companyID, "title": ticketTitle},
			TicketPlan:        plan,
		}}, nil
	}

	ticketID := *existingTicket
	if createFields != nil {
		ctx, cancel := cmdContext()
		defer cancel()
		client, err := a.newClient(ctx)
		if err != nil {
			return nil, err
		}
		id, err := client.Create(ctx, atapi.EntityTickets, createFields)
		if err != nil {
			return nil, err
		}
		ticketID = id
	}

	s := a.state.Start(label, companyID, ticketTitle, a.now(), *keepOthers)
	s.TicketID = ticketID
	s.AddNote(*note)
	if err := a.state.Save(); err != nil {
		return nil, err
	}
	return &cmdResult{action: "timer.start", data: TimerStartResult{Session: a.sessionView(s), TicketPlan: plan}}, nil
}

// ticketPlan describes the intended ticket action and returns the create
// payload (nil when attaching to or skipping ticket creation).
func (a *App) ticketPlan(companyID int, title, desc string, existingTicket int64, noTicket bool) (map[string]any, map[string]any) {
	switch {
	case existingTicket != 0:
		return map[string]any{"mode": "attach", "ticketId": existingTicket}, nil
	case noTicket:
		return map[string]any{"mode": "none"}, nil
	default:
		fields, warnings := a.ticketFields(companyID, title, desc)
		return map[string]any{"mode": "create", "fields": fields, "warnings": warnings}, fields
	}
}

func (a *App) cmdTimerStatus(args []string) (*cmdResult, error) {
	fs := newFlagSet("timer status")
	if err := fs.Parse(args); err != nil {
		return nil, usageErr("timer status", err)
	}
	now := a.now()
	sessions := a.state.Sorted()
	views := make([]SessionView, 0, len(sessions))
	total := 0.0
	for _, s := range sessions {
		views = append(views, a.sessionView(s))
		total += s.Hours(now)
	}
	return &cmdResult{action: "timer.status", data: TimerStatusResult{Count: len(views), Sessions: views, TotalHours: round2(total)}}, nil
}

func (a *App) cmdTimerPause(args []string) (*cmdResult, error) {
	fs := newFlagSet("timer pause")
	if err := fs.Parse(args); err != nil {
		return nil, usageErr("timer pause", err)
	}
	s, err := a.resolveSession(firstArg(fs.Args()))
	if err != nil {
		return nil, err
	}
	s.Pause(a.now())
	if err := a.state.Save(); err != nil {
		return nil, err
	}
	return &cmdResult{action: "timer.pause", data: a.sessionView(s)}, nil
}

func (a *App) cmdTimerResume(args []string) (*cmdResult, error) {
	fs := newFlagSet("timer resume")
	if err := fs.Parse(args); err != nil {
		return nil, usageErr("timer resume", err)
	}
	s, err := a.resolveSession(firstArg(fs.Args()))
	if err != nil {
		return nil, err
	}
	s.Resume(a.now())
	if err := a.state.Save(); err != nil {
		return nil, err
	}
	return &cmdResult{action: "timer.resume", data: a.sessionView(s)}, nil
}

func (a *App) cmdTimerSwitch(args []string) (*cmdResult, error) {
	fs := newFlagSet("timer switch")
	if err := fs.Parse(args); err != nil {
		return nil, usageErr("timer switch", err)
	}
	arg := firstArg(fs.Args())
	if arg == "" {
		return nil, hinted("usage: atem timer switch <session>", "no session id given")
	}
	s, err := a.resolveSession(arg)
	if err != nil {
		return nil, err
	}
	a.state.Switch(s, a.now())
	if err := a.state.Save(); err != nil {
		return nil, err
	}
	return &cmdResult{action: "timer.switch", data: a.sessionView(s)}, nil
}

func (a *App) cmdTimerNote(args []string) (*cmdResult, error) {
	fs := newFlagSet("timer note")
	if err := fs.Parse(args); err != nil {
		return nil, usageErr("timer note", err)
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return nil, hinted("usage: atem timer note [session] <text>", "no note text given")
	}
	sessionArg := ""
	text := strings.Join(rest, " ")
	if len(rest) >= 2 && a.state.Find(rest[0]) != nil {
		sessionArg = rest[0]
		text = strings.Join(rest[1:], " ")
	}
	s, err := a.resolveSession(sessionArg)
	if err != nil {
		return nil, err
	}
	s.AddNote(text)
	if err := a.state.Save(); err != nil {
		return nil, err
	}
	return &cmdResult{action: "timer.note", data: a.sessionView(s)}, nil
}

func (a *App) cmdTimerStop(args []string) (*cmdResult, error) {
	positional, rest := splitLeadingArgs(args)
	fs := newFlagSet("timer stop")
	hoursOverride := fs.Float64("hours", 0, "override measured hours")
	note := fs.String("note", "", "final work note")
	date := fs.String("date", "", "date worked YYYY-MM-DD (default: today)")
	closeTicket := fs.Bool("close", false, "close the ticket after logging time")
	dryRun := fs.Bool("dry-run", false, "preview without writing")
	if err := fs.Parse(rest); err != nil {
		return nil, usageErr("timer stop", err)
	}
	s, err := a.resolveSession(pickArg(positional, fs.Args()))
	if err != nil {
		return nil, err
	}

	resourceID := a.resourceID()
	if resourceID == 0 {
		return nil, hinted(
			"find yours via `atem resource search <your name>`, then `atem config set resourceId <id>` (or set ATEM_RESOURCE_ID)",
			"no resource id configured (time must be logged as someone)",
		)
	}

	worked, err := a.workedAnchor(*date)
	if err != nil {
		return nil, err
	}
	hours := round2(positiveOr(*hoursOverride, s.Hours(a.now())))
	notesText := joinNotes(s.Notes, *note)
	// Autotask rejects a blank summaryNotes with a 500; catch it here with a hint
	// (and in --dry-run) instead of letting the write fail.
	if strings.TrimSpace(notesText) == "" {
		return nil, hinted(`add --note "..." (or jot notes during the session with `+"`atem timer note`"+`)`, "time entry needs a note (Autotask rejects a blank summaryNotes)")
	}
	if *closeTicket {
		if _, err := a.completeTicketStatus(); err != nil {
			return nil, err
		}
	}
	timeEntry := a.timeEntryFields(resourceID, s.TicketID, hours, notesText, worked)

	var createTicket map[string]any
	if s.TicketID == 0 {
		// 0 is a valid company id (the owner org); only NoCompany means unset.
		if s.CompanyID == timer.NoCompany {
			return nil, hinted("start the session with --company or attach a --ticket", "session has no ticket and no company to log against")
		}
		createTicket, _ = a.ticketFields(s.CompanyID, s.Title, "")
	}

	if *dryRun {
		return &cmdResult{action: "timer.stop", dryRun: true, data: TimerStopDryRun{
			SessionID:    s.ID,
			Hours:        hours,
			CreateTicket: createTicket,
			TimeEntry:    timeEntry,
			CloseTicket:  *closeTicket,
		}}, nil
	}

	return a.executeStop(s, timeEntry, createTicket, *closeTicket, hours)
}

// executeStop performs the Autotask writes for a timer stop and clears the
// session. It is separated from flag handling to keep each function small.
func (a *App) executeStop(s *timer.Session, timeEntry, createTicket map[string]any, closeTicket bool, hours float64) (*cmdResult, error) {
	journal, rec, err := a.beginWriteOperation("timer.stop", timerStopJournalPayload{
		SessionID:    s.ID,
		CreateTicket: createTicket,
		TimeEntry:    timeEntry,
		CloseTicket:  closeTicket,
	})
	if err != nil {
		return nil, err
	}
	ctx, cancel := cmdContext()
	defer cancel()
	client, err := a.newClient(ctx)
	if err != nil {
		return nil, err
	}

	ticketID, err := a.ensureJournalTicket(ctx, client, journal, rec, s.TicketID, createTicket, false)
	if err != nil {
		return nil, err
	}
	timeEntry["ticketID"] = ticketID

	rec.EntryIDs = ensureEntrySlots(rec.EntryIDs, 1)
	if err := journal.touch(rec, a.now()); err != nil {
		return nil, err
	}
	timeEntryID, err := a.ensureJournalTimeEntry(ctx, client, journal, rec, ticketID, 0, timeEntry)
	if err != nil {
		return nil, hinted(
			"retry the same command; atem will resume from the local write journal and skip writes already completed",
			"failed while logging timer time entry: %v", err,
		)
	}

	closed, err := a.ensureJournalClosed(ctx, client, journal, rec, ticketID, closeTicket)
	if err != nil {
		return nil, err
	}

	a.state.Remove(s.ID)
	if err := a.state.Save(); err != nil {
		return nil, err
	}
	if err := journal.complete(rec.Key); err != nil {
		return nil, err
	}
	return &cmdResult{action: "timer.stop", data: TimerStopResult{
		SessionID:   s.ID,
		TicketID:    ticketID,
		TimeEntryID: timeEntryID,
		Hours:       hours,
		Closed:      closed,
	}}, nil
}

type timerStopJournalPayload struct {
	SessionID    string         `json:"sessionId"`
	CreateTicket map[string]any `json:"createTicket,omitempty"`
	TimeEntry    map[string]any `json:"timeEntry"`
	CloseTicket  bool           `json:"closeTicket"`
}

// workdayEndHour anchors the end of a backdated work window so the derived
// start/end times fall within the worked business day.
const workdayEndHour = 17

// workedAnchor returns the timestamp that ends a time entry's window. With no
// --date it is the current time (log-as-you-go). With --date it is that day at
// the end of the business day, so a backdated entry lands on the worked date.
func (a *App) workedAnchor(date string) (time.Time, error) {
	loc, err := a.workLocation()
	if err != nil {
		return time.Time{}, err
	}
	if strings.TrimSpace(date) == "" {
		return a.now().In(loc), nil
	}
	d, err := time.ParseInLocation(dateLayout, strings.TrimSpace(date), loc)
	if err != nil {
		return time.Time{}, hinted("use YYYY-MM-DD, e.g. --date 2026-06-15", "invalid --date %q", date)
	}
	return d.Add(workdayEndHour * time.Hour), nil
}

func (a *App) timeEntryFields(resourceID int, ticketID int64, hours float64, notes string, worked time.Time) map[string]any {
	// Service tickets require start/stop times, so derive a window ending at the
	// worked anchor whose length matches the hours.
	start := worked.Add(-time.Duration(hours * float64(time.Hour)))
	return a.timeEntryFieldsWindow(resourceID, ticketID, start, worked, notes)
}

// timeEntryFieldsWindow builds a time entry for an explicit [start,end] window.
// hoursWorked is the window length. Times carry their zone (sent as UTC) so
// Autotask records the right instant instead of treating a naive local time as
// UTC and shifting the displayed window by the local offset.
func (a *App) timeEntryFieldsWindow(resourceID int, ticketID int64, start, end time.Time, notes string) map[string]any {
	// dateWorked only needs the calendar day; anchor it at local noon so the day
	// is stable no matter the UTC offset.
	y, m, d := end.Date()
	day := time.Date(y, m, d, 12, 0, 0, 0, end.Location())
	fields := map[string]any{
		"resourceID":    resourceID,
		"ticketID":      ticketID,
		"dateWorked":    day.UTC().Format(dateTimeZoned),
		"startDateTime": start.UTC().Format(dateTimeZoned),
		"endDateTime":   end.UTC().Format(dateTimeZoned),
		"hoursWorked":   round2(end.Sub(start).Hours()),
		"summaryNotes":  notes,
	}
	if a.cfg.Defaults.BillingCodeID != 0 {
		fields["billingCodeID"] = a.cfg.Defaults.BillingCodeID
	}
	if roleID := a.roleID(); roleID != 0 {
		fields["roleID"] = roleID
	}
	return fields
}

// positiveOr returns v when it is greater than zero, otherwise fallback.
func positiveOr(v, fallback float64) float64 {
	if v > 0 {
		return v
	}
	return fallback
}

// joinNotes combines accumulated notes with an optional extra note.
func joinNotes(notes []string, extra string) string {
	all := append([]string{}, notes...)
	if strings.TrimSpace(extra) != "" {
		all = append(all, strings.TrimSpace(extra))
	}
	return strings.Join(all, "\n")
}
