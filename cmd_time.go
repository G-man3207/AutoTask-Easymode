package main

import (
	"autotask-easymode/internal/atapi"
	"errors"
	"strconv"
	"strings"
	"time"
)

// workWindow is one parsed [start,end] clock window with the note that becomes
// its time entry's summaryNotes.
type workWindow struct {
	start time.Time
	end   time.Time
	note  string
	hours float64
}

var errInvalidClock = errors.New("invalid clock time")

// cmdTimeAdd logs one Autotask time entry per clock window, so split work like
// "11-12,13-15" is recorded as the actual windows rather than one merged block.
func (a *App) cmdTimeAdd(args []string) (*cmdResult, error) {
	fs := newFlagSet("time add")
	company := fs.String("company", "", "customer alias or companyID (creates a ticket)")
	ticket := fs.Int64("ticket", 0, "existing ticket id to log against")
	title := fs.String("title", "", "ticket title (when creating)")
	desc := fs.String("desc", "", "ticket description (when creating)")
	issueType := fs.Int("issue-type", 0, "ticket issue type id when creating")
	subIssueType := fs.Int("sub-issue-type", 0, "ticket sub-issue type id when creating")
	contactID := fs.Int64("contact", 0, "primary contact id when creating a ticket")
	date := fs.String("date", "", "date worked YYYY-MM-DD (default: today)")
	windowsSpec := fs.String("windows", "", `time windows, e.g. "11-12,13-15" or "11-12=fixed X,13-15=did Y"`)
	note := fs.String("note", "", "default note applied to each entry")
	closeTicket := fs.Bool("close", false, "close the ticket after logging time")
	dryRun := fs.Bool("dry-run", false, "preview without writing")
	if err := fs.Parse(args); err != nil {
		return nil, usageErr("time add", err)
	}
	creatingTicket := strings.TrimSpace(*company) != ""
	ticketOpts := ticketFieldOptions{
		issueType:            *issueType,
		subIssueType:         *subIssueType,
		contactID:            *contactID,
		preferClassification: creatingTicket,
		preferContact:        creatingTicket,
	}
	if err := ticketOpts.validate(); err != nil {
		return nil, err
	}

	resourceID := a.resourceID()
	if resourceID == 0 {
		return nil, hinted(
			"find yours via `atem resource search <your name>`, then `atem config set resourceId <id>` (or set ATEM_RESOURCE_ID)",
			"no resource id configured (time must be logged as someone)",
		)
	}
	if strings.TrimSpace(*windowsSpec) == "" {
		return nil, hinted(`e.g. --windows "11-12,13-15"`, "missing --windows")
	}

	loc, err := a.workLocation()
	if err != nil {
		return nil, err
	}
	day, err := a.resolveWorkDay(*date, loc)
	if err != nil {
		return nil, err
	}
	windows, err := parseWindows(*windowsSpec, day, *note)
	if err != nil {
		return nil, err
	}

	createTicket, warnings, err := a.timeAddTicket(*ticket, *company, *title, *desc, day, ticketOpts)
	if err != nil {
		return nil, err
	}

	entries := make([]map[string]any, 0, len(windows))
	total := 0.0
	for _, w := range windows {
		entries = append(entries, a.timeEntryFieldsWindow(resourceID, *ticket, w.start, w.end, w.note))
		total += w.hours
	}
	total = round2(total)

	if *dryRun {
		return &cmdResult{action: "time.add", dryRun: true, data: TimeAddDryRun{
			Date:         day.Format(dateLayout),
			CreateTicket: createTicket,
			Entries:      entries,
			TotalHours:   total,
			CloseTicket:  *closeTicket,
			Warnings:     warnings,
		}}, nil
	}
	return a.executeTimeAdd(createTicket, entries, *closeTicket, day, total, warnings)
}

// timeAddTicket resolves the ticket to log against: nil create payload when an
// existing --ticket is given, otherwise the fields to create one from --company.
func (a *App) timeAddTicket(ticket int64, company, title, desc string, day time.Time, opts ticketFieldOptions) (map[string]any, []string, error) {
	switch {
	case ticket != 0:
		if opts.issueType != 0 || opts.subIssueType != 0 || opts.contactID != 0 {
			return nil, nil, hinted(
				"omit ticket field flags with --ticket, or create a new ticket via --company so atem can set them",
				"issue type/contact can only be set when time add creates a ticket",
			)
		}
		return nil, nil, nil
	case strings.TrimSpace(company) != "":
		// Creating a ticket: it must carry a description for the customer. (When
		// logging against an existing --ticket above, no ticket is created, so no
		// description is needed here.)
		if derr := requireDescription(desc); derr != nil {
			return nil, nil, derr
		}
		companyID, err := a.cfg.ResolveCompany(company)
		if err != nil {
			return nil, nil, err
		}
		t := strings.TrimSpace(title)
		if t == "" {
			t = "Arbete " + day.Format(dateLayout)
		}
		fields, warnings := a.ticketFieldsWithOptions(companyID, t, desc, opts)
		return fields, warnings, nil
	default:
		return nil, nil, hinted("pass --ticket <id> or --company <alias|id>", "no ticket to log against")
	}
}

// executeTimeAdd performs the Autotask writes for time add: optionally create a
// ticket, create one time entry per window, optionally close the ticket.
func (a *App) executeTimeAdd(createTicket map[string]any, entries []map[string]any, closeTicket bool, day time.Time, total float64, warnings []string) (*cmdResult, error) {
	journal, rec, err := a.beginWriteOperation("time.add", timeAddJournalPayload{
		CreateTicket: createTicket,
		Entries:      entries,
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

	ticketID, err := a.ensureJournalTicket(ctx, client, journal, rec, asInt64(entries[0]["ticketID"]), createTicket, true)
	if err != nil {
		return nil, err
	}
	rec.EntryIDs = ensureEntrySlots(rec.EntryIDs, len(entries))
	if err := journal.touch(rec, a.now()); err != nil {
		return nil, err
	}
	ids := make([]int64, len(entries))
	for i, e := range entries {
		if rec.EntryIDs[i] != 0 {
			ids[i] = rec.EntryIDs[i]
			continue
		}
		e["ticketID"] = ticketID
		id, cerr := client.Create(ctx, atapi.EntityTimeEntries, e)
		if cerr != nil {
			return nil, hinted(
				"earlier entries were already logged — check the ticket before retrying",
				"failed after %d of %d time entries: %v", completedEntryCount(rec.EntryIDs), len(entries), cerr,
			)
		}
		rec.EntryIDs[i] = id
		ids[i] = id
		if jerr := journal.touch(rec, a.now()); jerr != nil {
			return nil, jerr
		}
	}

	closed, err := a.ensureJournalClosed(ctx, client, journal, rec, ticketID, closeTicket)
	if err != nil {
		return nil, err
	}
	if err := journal.complete(rec.Key); err != nil {
		return nil, err
	}

	return &cmdResult{action: "time.add", data: TimeAddResult{
		TicketID:     ticketID,
		TimeEntryIDs: ids,
		Date:         day.Format(dateLayout),
		TotalHours:   total,
		Closed:       closed,
		Warnings:     warnings,
	}}, nil
}

type timeAddJournalPayload struct {
	CreateTicket map[string]any   `json:"createTicket,omitempty"`
	Entries      []map[string]any `json:"entries"`
	CloseTicket  bool             `json:"closeTicket"`
}

func completedEntryCount(ids []int64) int {
	count := 0
	for _, id := range ids {
		if id != 0 {
			count++
		}
	}
	return count
}

// resolveWorkDay returns the worked day at midnight in loc, defaulting to today.
func (a *App) resolveWorkDay(date string, loc *time.Location) (time.Time, error) {
	if strings.TrimSpace(date) == "" {
		y, m, d := a.now().In(loc).Date()
		return time.Date(y, m, d, 0, 0, 0, 0, loc), nil
	}
	d, err := time.ParseInLocation(dateLayout, strings.TrimSpace(date), loc)
	if err != nil {
		return time.Time{}, hinted("use YYYY-MM-DD, e.g. --date 2026-06-15", "invalid --date %q", date)
	}
	return d, nil
}

// parseWindows parses a "START-END[=note],..." spec into windows on day, falling
// back to defaultNote for windows without their own note.
func parseWindows(spec string, day time.Time, defaultNote string) ([]workWindow, error) {
	windows := make([]workWindow, 0)
	for _, raw := range strings.Split(spec, ",") {
		item := strings.TrimSpace(raw)
		if item == "" {
			continue
		}
		rangePart := item
		note := strings.TrimSpace(defaultNote)
		if r, n, ok := strings.Cut(item, "="); ok {
			rangePart = strings.TrimSpace(r)
			note = strings.TrimSpace(n)
		}
		start, end, err := parseWindowRange(rangePart, day)
		if err != nil {
			return nil, err
		}
		if note == "" {
			return nil, hinted(
				`add --note or per-window text, e.g. --windows "11-12=did X"`,
				"window %q has no note (Autotask requires one per entry)", rangePart,
			)
		}
		windows = append(windows, workWindow{start: start, end: end, note: note, hours: round2(end.Sub(start).Hours())})
	}
	if len(windows) == 0 {
		return nil, hinted(`e.g. --windows "11-12,13-15"`, "no time windows parsed")
	}
	return windows, nil
}

// parseWindowRange parses "START-END" (each HH or HH:MM, 24h) into times on day.
func parseWindowRange(s string, day time.Time) (time.Time, time.Time, error) {
	lo, hi, ok := strings.Cut(s, "-")
	if !ok {
		return time.Time{}, time.Time{}, hinted("use START-END, e.g. 11-12 or 13:00-15:30", "invalid window %q", s)
	}
	sh, sm, err := parseClock(lo)
	if err != nil {
		return time.Time{}, time.Time{}, hinted("times are HH or HH:MM (24h)", "invalid start time in window %q", s)
	}
	eh, em, err := parseClock(hi)
	if err != nil {
		return time.Time{}, time.Time{}, hinted("times are HH or HH:MM (24h)", "invalid end time in window %q", s)
	}
	y, mo, d := day.Date()
	start := time.Date(y, mo, d, sh, sm, 0, 0, day.Location())
	end := time.Date(y, mo, d, eh, em, 0, 0, day.Location())
	if !end.After(start) {
		return time.Time{}, time.Time{}, hinted("end must be after start", "window %q is not a positive range", s)
	}
	return start, end, nil
}

// parseClock parses "HH" or "HH:MM" (24h) into hours and minutes.
func parseClock(s string) (int, int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, errInvalidClock
	}
	hStr, mStr, hasMin := strings.Cut(s, ":")
	h, err := strconv.Atoi(strings.TrimSpace(hStr))
	if err != nil || h < 0 || h > 23 {
		return 0, 0, errInvalidClock
	}
	m := 0
	if hasMin {
		m, err = strconv.Atoi(strings.TrimSpace(mStr))
		if err != nil || m < 0 || m > 59 {
			return 0, 0, errInvalidClock
		}
	}
	return h, m, nil
}
