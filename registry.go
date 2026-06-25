package main

import "strings"

// cmdFlag describes one input of a command: a --flag or a positional argument.
// The same metadata drives the CLI help, `atem describe`, and the MCP tool
// schemas, so the agent-facing surface can never drift from the commands.
type cmdFlag struct {
	Name       string   `json:"name"`
	Type       string   `json:"type"` // string | int | float | bool
	Required   bool     `json:"required,omitempty"`
	Positional bool     `json:"positional,omitempty"`
	Default    string   `json:"default,omitempty"`
	Enum       []string `json:"enum,omitempty"` // allowed values, if constrained
	Desc       string   `json:"desc"`
}

// command is one CLI command (and one MCP tool). Name is the space-separated
// invocation, e.g. "ticket create" or "report".
type command struct {
	Name        string    `json:"name"`
	Summary     string    `json:"summary"`
	Flags       []cmdFlag `json:"flags"`
	Example     string    `json:"example,omitempty"`
	ReadOnly    bool      `json:"readOnly,omitempty"`    // no side effects
	Destructive bool      `json:"destructive,omitempty"` // writes to Autotask (has --dry-run)
	MCPHidden   bool      `json:"-"`                     // not exposed as an MCP tool (interactive)
	// OutputType is a zero value of the result struct, used to generate the MCP
	// outputSchema by reflection. nil means a dynamic/open shape (loose schema).
	// DryRunType, when set, is the distinct --dry-run result shape (-> oneOf).
	OutputType any `json:"-"`
	DryRunType any `json:"-"`
	run        func(*App, []string) (*cmdResult, error)
}

// commands is the single source of truth for what atem can do. dispatch routes
// on it, `atem describe` serializes it, and the MCP server exposes it as tools.
var commands = []command{
	{
		Name: "company search", Summary: "Find companies by name.", ReadOnly: true,
		Example: `company search "Acme"`,
		Flags: []cmdFlag{
			{Name: "query", Type: "string", Required: true, Positional: true, Desc: "name text to search for"},
			{Name: "limit", Type: "int", Default: "25", Desc: "max results"},
		},
		OutputType: CompanySearchResult{},
		run:        (*App).cmdCompanySearch,
	},
	{
		Name: "company alias", Summary: "Save a local alias for a company id.",
		Example: `company alias acme 0`,
		Flags: []cmdFlag{
			{Name: "name", Type: "string", Required: true, Positional: true, Desc: "alias name"},
			{Name: "companyId", Type: "int", Required: true, Positional: true, Desc: "Autotask company id"},
		},
		OutputType: CompanyAliasResult{},
		run:        (*App).cmdCompanyAlias,
	},
	{
		Name: "resource search", Summary: "Find resources by name or email (e.g. your own resourceId).", ReadOnly: true,
		Example: `resource search "Alex"`,
		Flags: []cmdFlag{
			{Name: "query", Type: "string", Required: true, Positional: true, Desc: "name or email text"},
			{Name: "limit", Type: "int", Default: "25", Desc: "max results"},
		},
		OutputType: ResourceSearchResult{},
		run:        (*App).cmdResourceSearch,
	},
	{
		Name: "ticket search", Summary: "Find tickets whose title contains text, optionally scoped to a company.", ReadOnly: true,
		Example: `ticket search --company 0 "hemsida"`,
		Flags: []cmdFlag{
			{Name: "query", Type: "string", Required: true, Positional: true, Desc: "title text to search for"},
			{Name: "company", Type: "string", Desc: "customer alias or companyID (omit for all companies)"},
			{Name: "limit", Type: "int", Default: "25", Desc: "max results"},
		},
		OutputType: TicketSearchResult{},
		run:        (*App).cmdTicketSearch,
	},
	{
		Name: "ticket issue-types", Summary: "List active ticket issue types and sub-issue types.", ReadOnly: true,
		Example:    `ticket issue-types`,
		OutputType: TicketIssueTypesResult{},
		run:        (*App).cmdTicketIssueTypes,
	},
	{
		Name: "ticket create", Summary: "Create a ticket assigned to the configured resource.", Destructive: true,
		Example: `ticket create --company 0 --title "Genomgång" --desc "Vad ärendet gäller" --dry-run`,
		Flags: []cmdFlag{
			{Name: "company", Type: "string", Required: true, Desc: "customer alias or companyID"},
			{Name: "title", Type: "string", Required: true, Desc: "ticket title"},
			{Name: "desc", Type: "string", Required: true, Desc: "ticket description (required — an empty description is customer-facing)"},
			{Name: "issue-type", Type: "int", Desc: "ticket issue type id from ticket issue-types"},
			{Name: "sub-issue-type", Type: "int", Desc: "ticket sub-issue type id from ticket issue-types; requires --issue-type"},
			{Name: "dry-run", Type: "bool", Desc: "preview the payload without writing"},
		},
		OutputType: TicketCreateResult{},
		DryRunType: TicketCreateDryRun{},
		run:        (*App).cmdTicketCreate,
	},
	{
		Name: "ticket show", Summary: "Fetch a ticket by id.", ReadOnly: true,
		Example: `ticket show 121159`,
		Flags: []cmdFlag{
			{Name: "id", Type: "int", Required: true, Positional: true, Desc: "ticket id"},
		},
		run: (*App).cmdTicketShow,
	},
	{
		Name: "ticket close", Summary: "Set a ticket to the configured complete status.", Destructive: true,
		Example: `ticket close 121159 --dry-run`,
		Flags: []cmdFlag{
			{Name: "id", Type: "int", Required: true, Positional: true, Desc: "ticket id"},
			{Name: "dry-run", Type: "bool", Desc: "preview without writing"},
		},
		OutputType: TicketCloseResult{},
		DryRunType: TicketCloseDryRun{},
		run:        (*App).cmdTicketClose,
	},
	{
		Name: "timer start", Summary: "Start a local work session, optionally creating or attaching a ticket.", Destructive: true,
		Example: `timer start --company 0 --title "Arbete"`,
		Flags: []cmdFlag{
			{Name: "company", Type: "string", Desc: "customer alias or companyID"},
			{Name: "title", Type: "string", Desc: "ticket title"},
			{Name: "desc", Type: "string", Desc: "ticket description"},
			{Name: "note", Type: "string", Desc: "initial work note"},
			{Name: "ticket", Type: "int", Desc: "attach to an existing ticket id instead of creating one"},
			{Name: "no-ticket", Type: "bool", Desc: "do not create or attach a ticket yet"},
			{Name: "keep-others", Type: "bool", Desc: "keep other timers running"},
			{Name: "dry-run", Type: "bool", Desc: "preview without writing"},
		},
		OutputType: TimerStartResult{},
		DryRunType: TimerStartDryRun{},
		run:        (*App).cmdTimerStart,
	},
	{
		Name: "timer status", Summary: "List local work sessions and their hours.", ReadOnly: true,
		Example:    `timer status`,
		OutputType: TimerStatusResult{},
		run:        (*App).cmdTimerStatus,
	},
	{
		Name: "timer note", Summary: "Append a work note to a session.",
		Example: `timer note s1 "fixed the printer"`,
		Flags: []cmdFlag{
			{Name: "session", Type: "string", Positional: true, Desc: "session id (default: the single active one)"},
			{Name: "text", Type: "string", Required: true, Positional: true, Desc: "note text"},
		},
		OutputType: SessionView{},
		run:        (*App).cmdTimerNote,
	},
	{
		Name: "timer pause", Summary: "Pause a running session.",
		Example: `timer pause s1`,
		Flags: []cmdFlag{
			{Name: "session", Type: "string", Positional: true, Desc: "session id (default: the single active one)"},
		},
		OutputType: SessionView{},
		run:        (*App).cmdTimerPause,
	},
	{
		Name: "timer resume", Summary: "Resume a paused session.",
		Example: `timer resume s1`,
		Flags: []cmdFlag{
			{Name: "session", Type: "string", Positional: true, Desc: "session id (default: the single active one)"},
		},
		OutputType: SessionView{},
		run:        (*App).cmdTimerResume,
	},
	{
		Name: "timer switch", Summary: "Make a session the only running one.",
		Example: `timer switch s2`,
		Flags: []cmdFlag{
			{Name: "session", Type: "string", Required: true, Positional: true, Desc: "session id"},
		},
		OutputType: SessionView{},
		run:        (*App).cmdTimerSwitch,
	},
	{
		Name: "timer stop", Summary: "Stop a session, log its time (creating a ticket if needed), and optionally close it.", Destructive: true,
		Example: `timer stop s1 --hours 2 --note "done" --close`,
		Flags: []cmdFlag{
			{Name: "session", Type: "string", Positional: true, Desc: "session id (default: the single active one)"},
			{Name: "hours", Type: "float", Desc: "override measured hours"},
			{Name: "date", Type: "string", Desc: "date worked YYYY-MM-DD (default: today)"},
			{Name: "note", Type: "string", Desc: "final work note (required if the session has none)"},
			{Name: "close", Type: "bool", Desc: "close the ticket after logging time"},
			{Name: "dry-run", Type: "bool", Desc: "preview without writing"},
		},
		OutputType: TimerStopResult{},
		DryRunType: TimerStopDryRun{},
		run:        (*App).cmdTimerStop,
	},
	{
		Name: "time add", Summary: "Log one time entry per clock window (e.g. split work), creating or attaching a ticket.", Destructive: true,
		Example: `time add --ticket 121159 --date 2026-06-16 --windows "11-12,13-15" --note "..."`,
		Flags: []cmdFlag{
			{Name: "ticket", Type: "int", Desc: "existing ticket id to log against"},
			{Name: "company", Type: "string", Desc: "customer alias or companyID (creates a ticket)"},
			{Name: "title", Type: "string", Desc: "ticket title (when creating)"},
			{Name: "desc", Type: "string", Desc: "ticket description (required when creating a ticket via --company; not needed with --ticket)"},
			{Name: "issue-type", Type: "int", Desc: "ticket issue type id when creating a ticket"},
			{Name: "sub-issue-type", Type: "int", Desc: "ticket sub-issue type id when creating a ticket; requires --issue-type"},
			{Name: "date", Type: "string", Desc: "date worked YYYY-MM-DD (default: today)"},
			{Name: "windows", Type: "string", Required: true, Desc: `time windows, e.g. "11-12,13-15" or "11-12=fixed X,13-15=did Y"`},
			{Name: "note", Type: "string", Desc: "default note applied to each entry"},
			{Name: "close", Type: "bool", Desc: "close the ticket after logging time"},
			{Name: "dry-run", Type: "bool", Desc: "preview without writing"},
		},
		OutputType: TimeAddResult{},
		DryRunType: TimeAddDryRun{},
		run:        (*App).cmdTimeAdd,
	},
	{
		Name: "report", Summary: "Aggregate time entries for a company, ticket, or title keyword.", ReadOnly: true,
		Example: `report --company 0 --from 2026-06-15 --to 2026-06-17`,
		Flags: []cmdFlag{
			{Name: "company", Type: "string", Desc: "customer alias or companyID"},
			{Name: "ticket", Type: "int", Desc: "limit to a single ticket id"},
			{Name: "match", Type: "string", Desc: "only tickets whose title contains this text"},
			{Name: "from", Type: "string", Desc: "start date YYYY-MM-DD (inclusive)"},
			{Name: "to", Type: "string", Desc: "end date YYYY-MM-DD (inclusive)"},
			{Name: "format", Type: "string", Default: "json", Enum: []string{"json", "md"}, Desc: "output format: json or md"},
			{Name: "limit", Type: "int", Default: "0", Desc: "max time entries (0 = all)"},
			{Name: "out", Type: "string", Desc: "also write the report to this file"},
		},
		OutputType: ReportResult{},
		run:        (*App).cmdReport,
	},
	{
		Name: "config show", Summary: "Show the current configuration (secrets redacted).", ReadOnly: true,
		Example:    `config show`,
		OutputType: ConfigView{},
		run:        (*App).cmdConfigShow,
	},
	{
		Name: "config set", Summary: "Set a single config key.",
		Example: `config set queueId 12345`,
		Flags: []cmdFlag{
			{Name: "key", Type: "string", Required: true, Positional: true, Desc: "config key (e.g. resourceId, queueId)"},
			{Name: "value", Type: "string", Required: true, Positional: true, Desc: "value to set"},
		},
		OutputType: ConfigView{},
		run:        (*App).cmdConfigSet,
	},
	{
		Name: "config doctor", Summary: "Verify credentials and zone, and list org picklist IDs.", ReadOnly: true,
		Example: `config doctor`,
		run:     (*App).cmdConfigDoctor,
	},
	{
		Name: "ui", Summary: "Serve the local config panel in a browser (interactive; blocks).", MCPHidden: true,
		Example: `ui --port 7378`,
		Flags: []cmdFlag{
			{Name: "port", Type: "int", Default: "7378", Desc: "localhost port to serve on"},
			{Name: "no-open", Type: "bool", Desc: "do not open the browser automatically"},
		},
		OutputType: UIResult{},
		run:        (*App).cmdUI,
	},
}

// lookupCommand returns the command with the exact space-separated name, or nil.
func lookupCommand(name string) *command {
	for i := range commands {
		if commands[i].Name == name {
			return &commands[i]
		}
	}
	return nil
}

// describeData serializes the registry for `atem describe`. It needs no config,
// so the surface is discoverable even before credentials are set.
func describeData() map[string]any {
	return map[string]any{"version": version, "commands": commands}
}

// subcommandsOf returns the second word of every command in the given group.
func subcommandsOf(group string) []string {
	var subs []string
	for _, c := range commands {
		if strings.HasPrefix(c.Name, group+" ") {
			subs = append(subs, strings.TrimPrefix(c.Name, group+" "))
		}
	}
	return subs
}

// commandUsageLine renders one command's invocation, e.g.
// "atem ticket create --company <company> --title <title> [--desc <desc>] [--dry-run]".
func commandUsageLine(c command) string {
	parts := make([]string, 0, 2+len(c.Flags))
	parts = append(parts, "atem", c.Name)
	for _, f := range c.Flags {
		var seg string
		switch {
		case f.Positional:
			seg = "<" + f.Name + ">"
		case f.Type == "bool":
			seg = "--" + f.Name
		default:
			seg = "--" + f.Name + " <" + f.Name + ">"
		}
		if !f.Required {
			seg = "[" + seg + "]"
		}
		parts = append(parts, seg)
	}
	return strings.Join(parts, " ")
}

// usageText builds the help screen. The command reference is generated from the
// registry so it cannot drift; only the surrounding prose is curated here.
func usageText() string {
	var b strings.Builder
	b.WriteString("atem " + version + " — AutoTask EasyMode\n\n")
	b.WriteString("A terminal-friendly, AI-driveable wrapper around the Autotask PSA REST API.\n")
	b.WriteString("Every command prints a single JSON object. Writes support --dry-run.\n\n")
	b.WriteString("USAGE\n  atem <group> <command> [flags]\n\nCOMMANDS\n")
	prevGroup := ""
	for _, c := range commands {
		group := strings.Fields(c.Name)[0]
		if prevGroup != "" && group != prevGroup {
			b.WriteString("\n")
		}
		prevGroup = group
		b.WriteString("  " + commandUsageLine(c) + "\n")
		b.WriteString("      " + c.Summary + "\n")
	}
	b.WriteString("\nAGENT / INTROSPECTION\n")
	b.WriteString("  atem describe        JSON of every command/flag (no config needed)\n")
	b.WriteString("  atem mcp             run as an MCP server over stdio (tools = commands)\n")
	b.WriteString("  atem serve           run remote MCP over HTTP (default toolset = m365)\n")
	b.WriteString("\nOTHER\n  atem help\n  atem version\n")
	b.WriteString("\nNOTES\n")
	b.WriteString("  Credentials resolve from env first (ATEM_USERNAME, ATEM_SECRET,\n")
	b.WriteString("  ATEM_INTEGRATION_CODE, ATEM_RESOURCE_ID), else the 0600 config file.\n")
	b.WriteString("  company id 0 is a valid company (the owner org). Preview any write with --dry-run.\n")
	b.WriteString("  report --match finds tickets by title keyword; --out writes the report to a file.\n")
	return b.String()
}
