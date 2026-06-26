package main

import (
	"autotask-easymode/internal/atapi"
	"context"
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
	return a.ticketFieldsWithOptions(companyID, title, desc, ticketFieldOptions{})
}

type ticketFieldOptions struct {
	issueType            int
	subIssueType         int
	contactID            int64
	preferClassification bool
	preferContact        bool
}

func (o ticketFieldOptions) validate() error {
	if o.subIssueType != 0 && o.issueType == 0 {
		return hinted(
			"first choose an issue type with `atem ticket issue-types`, then pass both --issue-type and --sub-issue-type",
			"--sub-issue-type requires --issue-type",
		)
	}
	return nil
}

func (a *App) ticketFieldsWithOptions(companyID int, title, desc string, opts ticketFieldOptions) (map[string]any, []string) {
	fields := map[string]any{
		"companyID": companyID,
		"title":     title,
		"status":    defOr(a.cfg.Defaults.TicketStatusNew, 1),
		"priority":  defOr(a.cfg.Defaults.Priority, 1),
	}
	if opts.issueType != 0 {
		fields["issueType"] = opts.issueType
	}
	if opts.subIssueType != 0 {
		fields["subIssueType"] = opts.subIssueType
	}
	if opts.contactID != 0 {
		fields["contactID"] = opts.contactID
	}
	var warnings []string
	if a.cfg.Defaults.QueueID != 0 {
		fields["queueID"] = a.cfg.Defaults.QueueID
	} else {
		warnings = append(warnings, "defaults.queueId is not set; Autotask usually requires queueID to create a ticket")
	}
	warnings = append(warnings, opts.classificationWarnings()...)
	warnings = append(warnings, opts.contactWarnings()...)
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

func (o ticketFieldOptions) classificationWarnings() []string {
	if !o.preferClassification {
		return nil
	}
	switch {
	case o.issueType == 0:
		return []string{"issueType/subIssueType are unset; most new tickets should be classified with ticket issue-types, and omitting them should be an exception for genuinely unclear or unusual cases"}
	case o.subIssueType == 0:
		return []string{"subIssueType is unset; most classified tickets should include a sub-issue type from the selected issue type unless no suitable option exists"}
	default:
		return nil
	}
}

func (o ticketFieldOptions) contactWarnings() []string {
	if !o.preferContact || o.contactID != 0 {
		return nil
	}
	return []string{"contactID is unset; ask who the user spoke with when the work involved a customer person, then run contact search within the target company and pass --contact. Omit only for internal/system work, unclear cases, or when no person is known."}
}

func validateTicketContact(ctx context.Context, client autotaskClient, companyID int, contactID int64) error {
	if contactID == 0 {
		return nil
	}
	contact, err := client.GetByID(ctx, atapi.EntityContacts, contactID)
	if err != nil {
		return err
	}
	rawCompanyID, ok := contact["companyID"]
	if !ok {
		return hinted(
			"run contact search for the target company and use one of the returned ids",
			"contact %d lookup did not return a companyID",
			contactID,
		)
	}
	contactCompanyID := asInt64(rawCompanyID)
	if contactCompanyID != int64(companyID) {
		return hinted(
			"run contact search/create under the same company as the ticket; never reuse a contact id from another company",
			"contact %d belongs to company %d, not company %d",
			contactID, contactCompanyID, companyID,
		)
	}
	if active, ok := contact["isActive"]; ok && !asBool(active) {
		return hinted(
			"ask the user for a different active contact, or omit --contact if no active person is known",
			"contact %d is not active",
			contactID,
		)
	}
	return nil
}

func validateCreateTicketContact(ctx context.Context, client autotaskClient, fields map[string]any) error {
	contactID := asInt64(fields["contactID"])
	if contactID == 0 {
		return nil
	}
	return validateTicketContact(ctx, client, int(asInt64(fields["companyID"])), contactID)
}

func (a *App) cmdTicketIssueTypes(args []string) (*cmdResult, error) {
	fs := newFlagSet("ticket issue-types")
	if err := fs.Parse(args); err != nil {
		return nil, usageErr("ticket issue-types", err)
	}

	ctx, cancel := cmdContext()
	defer cancel()
	client, err := a.newClient(ctx)
	if err != nil {
		return nil, err
	}
	fields, err := client.EntityFields(ctx, atapi.EntityTickets)
	if err != nil {
		return nil, err
	}
	return &cmdResult{action: "ticket.issue-types", data: ticketIssueTypesFromFields(fields)}, nil
}

func ticketIssueTypesFromFields(fields []atapi.Field) TicketIssueTypesResult {
	issues := activeIssueTypes(fields, "issueType")
	subIssues := activeSubIssueTypes(fields, "subIssueType")
	byIssue := make(map[int64][]SubIssueTypeOption, len(issues))
	activeIssues := make(map[int64]bool, len(issues))
	for _, issue := range issues {
		activeIssues[issue.ID] = true
	}
	subIssueCount := 0
	for _, sub := range subIssues {
		if !activeIssues[sub.IssueTypeID] {
			continue
		}
		byIssue[sub.IssueTypeID] = append(byIssue[sub.IssueTypeID], sub)
		subIssueCount++
	}
	for i := range issues {
		issues[i].SubIssueTypes = byIssue[issues[i].ID]
	}
	return TicketIssueTypesResult{
		Count:         len(issues),
		SubIssueCount: subIssueCount,
		IssueTypes:    issues,
		Guidance:      "Use these ids with ticket_create/time_add flags issue-type and sub-issue-type. Treat issue/sub-issue classification as expected for new tickets, not optional polish. Only omit it for genuinely unclear or unusual cases; if several options fit or the information is too thin, ask the user before creating the ticket. A sub-issue id must belong to the selected issue type.",
	}
}

func activeIssueTypes(fields []atapi.Field, name string) []IssueTypeOption {
	var out []IssueTypeOption
	for _, f := range fields {
		if !strings.EqualFold(f.Name, name) {
			continue
		}
		for _, v := range f.PicklistValues {
			if !v.IsActive {
				continue
			}
			id, ok := picklistInt(v.Value)
			if !ok {
				continue
			}
			out = append(out, IssueTypeOption{ID: id, Label: v.Label, Default: v.IsDefault})
		}
	}
	return out
}

func activeSubIssueTypes(fields []atapi.Field, name string) []SubIssueTypeOption {
	var out []SubIssueTypeOption
	for _, f := range fields {
		if !strings.EqualFold(f.Name, name) {
			continue
		}
		for _, v := range f.PicklistValues {
			if !v.IsActive {
				continue
			}
			id, ok := picklistInt(v.Value)
			if !ok {
				continue
			}
			parentID, ok := picklistInt(v.ParentValue)
			if !ok {
				continue
			}
			out = append(out, SubIssueTypeOption{ID: id, Label: v.Label, IssueTypeID: parentID, Default: v.IsDefault})
		}
	}
	return out
}

func picklistInt(s string) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return id, err == nil
}

func (a *App) cmdTicketCreate(args []string) (*cmdResult, error) {
	fs := newFlagSet("ticket create")
	company := fs.String("company", "", "customer alias or companyID")
	title := fs.String("title", "", "ticket title")
	desc := fs.String("desc", "", "ticket description")
	issueType := fs.Int("issue-type", 0, "ticket issue type id from `ticket issue-types`")
	subIssueType := fs.Int("sub-issue-type", 0, "ticket sub-issue type id from `ticket issue-types`")
	contactID := fs.Int64("contact", 0, "primary contact id from `contact search`")
	dryRun := fs.Bool("dry-run", false, "preview without writing")
	if err := fs.Parse(args); err != nil {
		return nil, usageErr("ticket create", err)
	}
	opts := ticketFieldOptions{issueType: *issueType, subIssueType: *subIssueType, contactID: *contactID, preferClassification: true, preferContact: true}
	if err := opts.validate(); err != nil {
		return nil, err
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

	fields, warnings := a.ticketFieldsWithOptions(companyID, *title, *desc, opts)
	if *dryRun {
		return &cmdResult{action: "ticket.create", dryRun: true, data: TicketCreateDryRun{Fields: fields, Warnings: warnings}}, nil
	}

	ctx, cancel := cmdContext()
	defer cancel()
	client, err := a.newClient(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateTicketContact(ctx, client, companyID, *contactID); err != nil {
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
