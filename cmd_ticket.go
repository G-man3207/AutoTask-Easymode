package main

import (
	"autotask-easymode/internal/atapi"
	"strconv"
	"strings"
)

// requireDescription enforces that any ticket atem creates on the user's behalf
// carries a non-blank description. A ticket with an empty description reads as
// unprofessional to the customer, so the explicit create paths (ticket create,
// time add) refuse to proceed without one.
func requireDescription(desc string) error {
	if strings.TrimSpace(desc) == "" {
		return hinted(
			`add --desc "<what the ticket is about>" — an empty description looks unprofessional to the customer`,
			"a new ticket needs a description",
		)
	}
	return nil
}

// ticketFields builds the payload for creating a ticket from configured
// defaults, returning warnings for any required-but-unset defaults.
func (a *App) ticketFields(companyID int, title, desc string) (map[string]any, []string) {
	fields := map[string]any{
		"companyID": companyID,
		"title":     title,
		"status":    defOr(a.cfg.Defaults.TicketStatusNew, 1),
		"priority":  defOr(a.cfg.Defaults.Priority, 1),
	}
	var warnings []string
	if a.cfg.Defaults.QueueID != 0 {
		fields["queueID"] = a.cfg.Defaults.QueueID
	} else {
		warnings = append(warnings, "defaults.queueId is not set; Autotask usually requires queueID to create a ticket")
	}
	if strings.TrimSpace(desc) != "" {
		fields["description"] = desc
	}
	// Assign the ticket to the configured resource (with their role) so the work
	// is owned by you and easy to follow up — this is the ticket's "Primary
	// Resource (Role)". Autotask requires the role when a resource is assigned.
	if rid, roleID := a.resourceID(), a.roleID(); rid != 0 && roleID != 0 {
		fields["assignedResourceID"] = rid
		fields["assignedResourceRoleID"] = roleID
	}
	return fields, warnings
}

func (a *App) cmdTicketCreate(args []string) (*cmdResult, error) {
	fs := newFlagSet("ticket create")
	company := fs.String("company", "", "customer alias or companyID")
	title := fs.String("title", "", "ticket title")
	desc := fs.String("desc", "", "ticket description")
	dryRun := fs.Bool("dry-run", false, "preview without writing")
	if err := fs.Parse(args); err != nil {
		return nil, usageErr("ticket create", err)
	}
	if strings.TrimSpace(*company) == "" {
		return nil, hinted("e.g. --company acme", "missing --company")
	}
	if strings.TrimSpace(*title) == "" {
		return nil, hinted(`e.g. --title "map first day"`, "missing --title")
	}
	if err := requireDescription(*desc); err != nil {
		return nil, err
	}
	companyID, err := a.cfg.ResolveCompany(*company)
	if err != nil {
		return nil, err
	}

	fields, warnings := a.ticketFields(companyID, *title, *desc)
	if *dryRun {
		return &cmdResult{action: "ticket.create", dryRun: true, data: TicketCreateDryRun{Fields: fields, Warnings: warnings}}, nil
	}

	ctx, cancel := cmdContext()
	defer cancel()
	client, err := a.newClient(ctx)
	if err != nil {
		return nil, err
	}
	id, err := client.Create(ctx, atapi.EntityTickets, fields)
	if err != nil {
		return nil, err
	}
	return &cmdResult{action: "ticket.create", data: TicketCreateResult{TicketID: id, Warnings: warnings}}, nil
}

func (a *App) cmdTicketSearch(args []string) (*cmdResult, error) {
	sa, err := parseSearch(args)
	if err != nil {
		return nil, usageErr("ticket search", err)
	}
	if sa.query == "" {
		return nil, hinted("usage: atem ticket search [--company <alias|id>] <text>", "no search text given")
	}
	// Default to all companies; 0 is a valid company id, so only scope when a
	// --company was actually given.
	companyID := atapi.AllCompanies
	if sa.company != "" {
		id, rerr := a.cfg.ResolveCompany(sa.company)
		if rerr != nil {
			return nil, rerr
		}
		companyID = id
	}

	ctx, cancel := cmdContext()
	defer cancel()
	client, err := a.newClient(ctx)
	if err != nil {
		return nil, err
	}
	items, err := client.SearchTickets(ctx, sa.query, companyID, sa.limit)
	if err != nil {
		return nil, err
	}

	tickets := make([]TicketHit, 0, len(items))
	for _, it := range items {
		tickets = append(tickets, TicketHit{
			ID:           asInt64(it["id"]),
			TicketNumber: asString(it["ticketNumber"]),
			Title:        asString(it["title"]),
			CompanyID:    asInt64(it["companyID"]),
			Status:       it["status"],
		})
	}
	return &cmdResult{
		action: "ticket.search",
		data:   TicketSearchResult{Query: sa.query, Count: len(tickets), Tickets: tickets},
	}, nil
}

func (a *App) cmdTicketShow(args []string) (*cmdResult, error) {
	positional, rest := splitLeadingArgs(args)
	fs := newFlagSet("ticket show")
	if err := fs.Parse(rest); err != nil {
		return nil, usageErr("ticket show", err)
	}
	id, err := parseID(pickArg(positional, fs.Args()))
	if err != nil {
		return nil, err
	}

	ctx, cancel := cmdContext()
	defer cancel()
	client, err := a.newClient(ctx)
	if err != nil {
		return nil, err
	}
	item, err := client.GetByID(ctx, atapi.EntityTickets, id)
	if err != nil {
		return nil, err
	}
	return &cmdResult{action: "ticket.show", data: item}, nil
}

func (a *App) cmdTicketClose(args []string) (*cmdResult, error) {
	positional, rest := splitLeadingArgs(args)
	fs := newFlagSet("ticket close")
	dryRun := fs.Bool("dry-run", false, "preview without writing")
	if err := fs.Parse(rest); err != nil {
		return nil, usageErr("ticket close", err)
	}
	id, err := parseID(pickArg(positional, fs.Args()))
	if err != nil {
		return nil, err
	}

	status := defOr(a.cfg.Defaults.TicketStatusComplete, 5)
	fields := map[string]any{"id": id, "status": status}
	if *dryRun {
		return &cmdResult{action: "ticket.close", dryRun: true, data: TicketCloseDryRun{Fields: fields}}, nil
	}

	ctx, cancel := cmdContext()
	defer cancel()
	client, err := a.newClient(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := client.Update(ctx, atapi.EntityTickets, fields); err != nil {
		return nil, err
	}
	return &cmdResult{action: "ticket.close", data: TicketCloseResult{TicketID: id, Status: status}}, nil
}

func parseID(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, hinted("provide a numeric ticket id", "no ticket id given")
	}
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, hinted("ticket id must be numeric", "invalid ticket id %q", s)
	}
	return id, nil
}
