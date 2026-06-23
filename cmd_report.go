package main

import (
	"autotask-easymode/internal/atapi"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

func (a *App) cmdReport(args []string) (*cmdResult, error) {
	fs := newFlagSet("report")
	company := fs.String("company", "", "customer alias or companyID")
	ticket := fs.Int64("ticket", 0, "limit to a single ticket id")
	match := fs.String("match", "", "only include tickets whose title contains this text")
	from := fs.String("from", "", "start date YYYY-MM-DD (inclusive)")
	to := fs.String("to", "", "end date YYYY-MM-DD (inclusive)")
	format := fs.String("format", "json", "output format: json or md")
	limit := fs.Int("limit", 0, "maximum time entries (0 = all)")
	out := fs.String("out", "", "also write the report to this file (markdown if --format md, else JSON)")
	if err := fs.Parse(args); err != nil {
		return nil, usageErr("report", err)
	}
	fromTS, toTS, err := dateRange(*from, *to)
	if err != nil {
		return nil, err
	}

	ctx, cancel := cmdContext()
	defer cancel()
	client, err := a.newClient(ctx)
	if err != nil {
		return nil, err
	}

	ticketIDs, titles, err := a.reportTickets(ctx, client, *company, *ticket, *match)
	if err != nil {
		return nil, err
	}

	var entries []map[string]any
	tickets := []ReportTicket{}
	var total float64
	if len(ticketIDs) > 0 {
		entries, err = client.TimeEntriesForTickets(ctx, ticketIDs, fromTS, toTS, *limit)
		if err != nil {
			return nil, err
		}
		tickets, total = aggregateEntries(entries, titles)
	}

	res := ReportResult{
		Company:     *company,
		Match:       *match,
		From:        *from,
		To:          *to,
		TicketCount: len(tickets),
		EntryCount:  len(entries),
		TotalHours:  round2(total),
		Tickets:     tickets,
		// Flagged is for the operator/AI, not the customer: it is intentionally
		// absent from the rendered markdown.
		Flagged: a.flaggedEntries(entries, titles),
	}
	if *format == "md" {
		res.Markdown = renderReportMarkdown(*company, *match, *from, *to, round2(total), tickets)
	}
	writtenTo, err := writeOut(*out, *format, res)
	if err != nil {
		return nil, err
	}
	res.WrittenTo = writtenTo
	return &cmdResult{action: "report", data: res}, nil
}

// writeOut optionally writes the report to a file: the rendered markdown when
// --format md, otherwise the indented JSON of the report data. Permissions are
// restrictive because reports contain customer data. It records the path under
// "writtenTo" so the JSON result confirms where it went.
func writeOut(path, format string, res ReportResult) (string, error) {
	if path == "" {
		return "", nil
	}
	var content string
	if format == "md" {
		content = res.Markdown
	} else {
		b, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return "", err
		}
		content = string(b)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// reportTickets resolves the set of ticket ids (and their titles) to report on:
// a single --ticket, every ticket of a --company, or (with --match) tickets
// whose title contains the keyword, optionally scoped to a company.
func (a *App) reportTickets(ctx context.Context, client autotaskClient, company string, ticket int64, match string) ([]int64, map[int64]string, error) {
	titles := map[int64]string{}
	if ticket != 0 {
		if item, err := client.GetByID(ctx, atapi.EntityTickets, ticket); err == nil {
			titles[ticket] = asString(item["title"])
		}
		return []int64{ticket}, titles, nil
	}

	companyGiven := strings.TrimSpace(company) != ""
	// Default to all companies; 0 is a valid company id, so only scope when a
	// --company was actually given. (TicketsForCompany is only reached when a
	// company was given, so it never sees the AllCompanies sentinel.)
	companyID := atapi.AllCompanies
	if companyGiven {
		id, err := a.cfg.ResolveCompany(company)
		if err != nil {
			return nil, nil, err
		}
		companyID = id
	}
	// 0 is a valid company id, so gate on the flag presence, not the value.
	if match == "" && !companyGiven {
		return nil, nil, hinted("pass --company, --ticket, or --match", "missing --company / --ticket / --match")
	}

	var (
		tickets []map[string]any
		err     error
	)
	if match != "" {
		tickets, err = client.SearchTickets(ctx, match, companyID, 0)
	} else {
		tickets, err = client.TicketsForCompany(ctx, companyID, 0)
	}
	if err != nil {
		return nil, nil, err
	}

	ids := make([]int64, 0, len(tickets))
	for _, t := range tickets {
		id := asInt64(t["id"])
		if id == 0 {
			continue
		}
		ids = append(ids, id)
		titles[id] = asString(t["title"])
	}
	return ids, titles, nil
}

// aggregateEntries groups time entries by ticket, sums hours, and returns a
// stable, JSON-friendly structure plus the grand total.
func aggregateEntries(entries []map[string]any, titles map[int64]string) ([]ReportTicket, float64) {
	grouped := map[int64][]ReportEntry{}
	totals := map[int64]float64{}
	grand := 0.0
	for _, e := range entries {
		ticketID := asInt64(e["ticketID"])
		hours := asFloat(e["hoursWorked"])
		grouped[ticketID] = append(grouped[ticketID], ReportEntry{
			Date:       asString(e["dateWorked"]),
			Hours:      round2(hours),
			Notes:      asString(e["summaryNotes"]),
			ResourceID: asInt64(e["resourceID"]), // who logged it; JSON only, not the markdown
			RoleID:     asInt64(e["roleID"]),     // billing role; JSON only, not the markdown
		})
		totals[ticketID] += hours
		grand += hours
	}

	ids := make([]int64, 0, len(grouped))
	for id := range grouped {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	out := make([]ReportTicket, 0, len(ids))
	for _, id := range ids {
		out = append(out, ReportTicket{
			TicketID:   id,
			Title:      titles[id],
			TotalHours: round2(totals[id]),
			Entries:    grouped[id],
		})
	}
	return out, grand
}

const (
	defaultFlagHoursOver   = 5
	defaultFlagNotesUnder  = 80
	defaultFlagHoursAlways = 12
)

// flagEntry classifies a time entry for review and returns the reason, or "" if
// it is fine. "large" = at least FlagHoursAlways hours (a big lump worth
// itemizing for a customer regardless of note); "thin" = more than FlagHoursOver
// hours with a note under FlagNotesUnder characters. All thresholds configurable.
func (a *App) flagEntry(hours float64, notes string) string {
	over := float64(defOr(a.cfg.Defaults.FlagHoursOver, defaultFlagHoursOver))
	under := defOr(a.cfg.Defaults.FlagNotesUnder, defaultFlagNotesUnder)
	always := float64(defOr(a.cfg.Defaults.FlagHoursAlways, defaultFlagHoursAlways))
	switch {
	case hours >= always:
		return "large"
	case hours > over && utf8.RuneCountInString(strings.TrimSpace(notes)) < under:
		return "thin"
	default:
		return ""
	}
}

// flaggedEntries returns the entries flagEntry marks, annotated (with a reason)
// for the operator/AI. It is surfaced in the JSON only (never the customer
// markdown) so the AI driving the CLI can offer to break those lumps down.
func (a *App) flaggedEntries(entries []map[string]any, titles map[int64]string) []FlaggedEntry {
	flagged := []FlaggedEntry{}
	for _, e := range entries {
		hours := asFloat(e["hoursWorked"])
		notes := asString(e["summaryNotes"])
		reason := a.flagEntry(hours, notes)
		if reason == "" {
			continue
		}
		ticketID := asInt64(e["ticketID"])
		flagged = append(flagged, FlaggedEntry{
			TicketID:  ticketID,
			Title:     titles[ticketID],
			Date:      asString(e["dateWorked"]),
			Hours:     round2(hours),
			NoteChars: utf8.RuneCountInString(strings.TrimSpace(notes)),
			Reason:    reason,
		})
	}
	return flagged
}

// dateRange validates YYYY-MM-DD bounds and expands them to inclusive
// datetime strings the API understands. Empty bounds are passed through.
func dateRange(from, to string) (string, string, error) {
	var fromTS, toTS string
	if from != "" {
		if _, err := time.Parse(dateLayout, from); err != nil {
			return "", "", hinted("use YYYY-MM-DD", "invalid --from %q", from)
		}
		fromTS = from + "T00:00:00"
	}
	if to != "" {
		if _, err := time.Parse(dateLayout, to); err != nil {
			return "", "", hinted("use YYYY-MM-DD", "invalid --to %q", to)
		}
		toTS = to + "T23:59:59"
	}
	return fromTS, toTS, nil
}

func renderReportMarkdown(company, match, from, to string, total float64, tickets []ReportTicket) string {
	scope := strings.TrimSpace(company)
	if match != "" {
		if scope != "" {
			scope += " — " + match
		} else {
			scope = match
		}
	}
	if scope == "" {
		scope = "alla ärenden"
	}
	lines := []string{
		"# Tidsrapport – " + scope,
		"",
		fmt.Sprintf("**Totalt: %.2f h** över %d ärenden.", total, len(tickets)),
		"",
	}
	if from != "" || to != "" {
		lines = append(lines, fmt.Sprintf("Period: %s – %s", orDash(from), orDash(to)), "")
	}
	for _, t := range tickets {
		lines = append(lines, fmt.Sprintf("## Ärende %d – %s (%.2f h)", t.TicketID, t.Title, t.TotalHours), "")
		for _, e := range t.Entries {
			lines = append(lines, fmt.Sprintf("- %s · %.2f h · %s", e.Date, e.Hours, e.Notes))
		}
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
