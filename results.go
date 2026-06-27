package main

import "autotask-easymode/internal/config"

// This file defines the typed result payloads (the `data` of each command's JSON
// result). They are the single source of truth for output shape, and the MCP
// outputSchema is generated from them by reflection.
//
// Field presence mirrors the wire format: no `omitempty` except where a key is
// genuinely optional (e.g. report markdown). Dry-run variants are separate
// structs because their shape differs.
//
// Two commands stay map-based on purpose: `ticket show` (a raw Autotask object,
// arbitrary fields) and `config doctor` (conditional diagnostics). With no
// OutputType they get a loose object schema.

// --- shared ---

// SessionView is a local timer session as surfaced to the user.
type SessionView struct {
	ID        string   `json:"id"`
	Label     string   `json:"label"`
	CompanyID int      `json:"companyId"`
	TicketID  int64    `json:"ticketId"`
	Title     string   `json:"title"`
	Running   bool     `json:"running"`
	Hours     float64  `json:"hours"`
	Notes     []string `json:"notes"`
}

// --- company / resource / ticket search ---

type CompanyHit struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	IsActive bool   `json:"isActive"`
}

type CompanySearchResult struct {
	Query     string       `json:"query"`
	Count     int          `json:"count"`
	Companies []CompanyHit `json:"companies"`
}

type CompanyAliasResult struct {
	Alias     string `json:"alias"`
	CompanyID int    `json:"companyId"`
}

type ContactHit struct {
	ID          int64  `json:"id"`
	CompanyID   int64  `json:"companyId"`
	FirstName   string `json:"firstName"`
	LastName    string `json:"lastName"`
	Email       string `json:"email"`
	Phone       string `json:"phone"`
	MobilePhone string `json:"mobilePhone"`
	IsActive    bool   `json:"isActive"`
}

type ContactSearchResult struct {
	Query    string       `json:"query"`
	Company  string       `json:"company"`
	Count    int          `json:"count"`
	Contacts []ContactHit `json:"contacts"`
	Guidance string       `json:"guidance"`
}

type ContactCreateResult struct {
	ContactID int64    `json:"contactId"`
	Warnings  []string `json:"warnings"`
}

type ContactCreateDryRun struct {
	Fields   map[string]any `json:"fields"`
	Warnings []string       `json:"warnings"`
}

type ResourceHit struct {
	ID        int64  `json:"id"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	Email     string `json:"email"`
	IsActive  bool   `json:"isActive"`
}

type ResourceSearchResult struct {
	Query     string        `json:"query"`
	Count     int           `json:"count"`
	Resources []ResourceHit `json:"resources"`
}

type TicketHit struct {
	ID           int64  `json:"id"`
	TicketNumber string `json:"ticketNumber"`
	Title        string `json:"title"`
	CompanyID    int64  `json:"companyId"`
	Status       any    `json:"status"`
}

type TicketSearchResult struct {
	Query   string      `json:"query"`
	Count   int         `json:"count"`
	Tickets []TicketHit `json:"tickets"`
}

type SubIssueTypeOption struct {
	ID          int64  `json:"id"`
	Label       string `json:"label"`
	IssueTypeID int64  `json:"issueTypeId"`
	Default     bool   `json:"default"`
}

type IssueTypeOption struct {
	ID            int64                `json:"id"`
	Label         string               `json:"label"`
	Default       bool                 `json:"default"`
	SubIssueTypes []SubIssueTypeOption `json:"subIssueTypes"`
}

type TicketIssueTypesResult struct {
	Count         int               `json:"count"`
	SubIssueCount int               `json:"subIssueCount"`
	IssueTypes    []IssueTypeOption `json:"issueTypes"`
	Guidance      string            `json:"guidance"`
}

// --- ticket create / close ---

type TicketCreateResult struct {
	TicketID int64    `json:"ticketId"`
	Warnings []string `json:"warnings"`
}

type TicketCreateDryRun struct {
	Fields   map[string]any `json:"fields"`
	Warnings []string       `json:"warnings"`
}

type TicketCloseResult struct {
	TicketID int64 `json:"ticketId"`
	Status   int   `json:"status"`
}

type TicketCloseDryRun struct {
	Fields map[string]any `json:"fields"`
}

// --- timer ---

type TimerStartResult struct {
	Session    SessionView    `json:"session"`
	TicketPlan map[string]any `json:"ticketPlan"`
}

type TimerStartDryRun struct {
	WouldStartSession map[string]any `json:"wouldStartSession"`
	TicketPlan        map[string]any `json:"ticketPlan"`
}

type TimerStatusResult struct {
	Count      int           `json:"count"`
	Sessions   []SessionView `json:"sessions"`
	TotalHours float64       `json:"totalHours"`
}

type TimerStopResult struct {
	SessionID   string  `json:"sessionId"`
	TicketID    int64   `json:"ticketId"`
	TimeEntryID int64   `json:"timeEntryId"`
	Hours       float64 `json:"hours"`
	Closed      bool    `json:"closed"`
}

type TimerStopDryRun struct {
	SessionID    string         `json:"sessionId"`
	Hours        float64        `json:"hours"`
	CreateTicket map[string]any `json:"createTicket"`
	TimeEntry    map[string]any `json:"timeEntry"`
	CloseTicket  bool           `json:"closeTicket"`
}

// --- time add ---

type TimeAddResult struct {
	TicketID     int64    `json:"ticketId"`
	TimeEntryIDs []int64  `json:"timeEntryIds"`
	Date         string   `json:"date"`
	TotalHours   float64  `json:"totalHours"`
	Closed       bool     `json:"closed"`
	Warnings     []string `json:"warnings"`
}

type TimeAddDryRun struct {
	Date         string           `json:"date"`
	CreateTicket map[string]any   `json:"createTicket"`
	Entries      []map[string]any `json:"entries"`
	TotalHours   float64          `json:"totalHours"`
	CloseTicket  bool             `json:"closeTicket"`
	Warnings     []string         `json:"warnings"`
}

// --- report ---

type ReportEntry struct {
	Date       string  `json:"date"`
	Hours      float64 `json:"hours"`
	Notes      string  `json:"notes"`
	ResourceID int64   `json:"resourceId"`
	RoleID     int64   `json:"roleId"`
}

type ReportTicket struct {
	TicketID   int64         `json:"ticketId"`
	Title      string        `json:"title"`
	TotalHours float64       `json:"totalHours"`
	Entries    []ReportEntry `json:"entries"`
}

type FlaggedEntry struct {
	TicketID  int64   `json:"ticketId"`
	Title     string  `json:"title"`
	Date      string  `json:"date"`
	Hours     float64 `json:"hours"`
	NoteChars int     `json:"noteChars"`
	Reason    string  `json:"reason"`
}

type ReportResult struct {
	Company     string         `json:"company"`
	Match       string         `json:"match"`
	From        string         `json:"from"`
	To          string         `json:"to"`
	TicketCount int            `json:"ticketCount"`
	EntryCount  int            `json:"entryCount"`
	TotalHours  float64        `json:"totalHours"`
	Tickets     []ReportTicket `json:"tickets"`
	Flagged     []FlaggedEntry `json:"flagged"`
	Markdown    string         `json:"markdown,omitempty"`
	WrittenTo   string         `json:"writtenTo,omitempty"` // set when --out wrote the report to a file
}

// --- config ---

type ConfigView struct {
	Path               string          `json:"path"`
	Username           string          `json:"username"`
	IntegrationCode    string          `json:"integrationCode"`
	Secret             string          `json:"secret"`
	APIBaseURL         string          `json:"apiBaseUrl"`
	ResourceID         int             `json:"resourceId"`
	Defaults           config.Defaults `json:"defaults"`
	Aliases            map[string]int  `json:"aliases"`
	MissingCredentials []string        `json:"missingCredentials"`
}
