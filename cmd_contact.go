package main

import (
	"autotask-easymode/internal/atapi"
	"net/mail"
	"strings"
)

func (a *App) cmdContactSearch(args []string) (*cmdResult, error) {
	sa, err := parseSearch(args)
	if err != nil {
		return nil, usageErr("contact search", err)
	}
	if strings.TrimSpace(sa.company) == "" {
		return nil, hinted("search contacts within a company, e.g. --company 0 Anna", "missing --company")
	}
	if sa.query == "" {
		return nil, hinted("usage: atem contact search --company <alias|id> <name-or-email>", "no contact search text given")
	}
	companyID, err := a.cfg.ResolveCompany(sa.company)
	if err != nil {
		return nil, err
	}

	ctx, cancel := cmdContext()
	defer cancel()
	client, err := a.newClient(ctx)
	if err != nil {
		return nil, err
	}
	items, err := client.SearchContacts(ctx, sa.query, companyID, sa.limit)
	if err != nil {
		return nil, err
	}

	contacts := make([]ContactHit, 0, len(items))
	for _, it := range items {
		contacts = append(contacts, ContactHit{
			ID:          asInt64(it["id"]),
			CompanyID:   asInt64(it["companyID"]),
			FirstName:   asString(it["firstName"]),
			LastName:    asString(it["lastName"]),
			Email:       asString(it["emailAddress"]),
			Phone:       asString(it["phone"]),
			MobilePhone: asString(it["mobilePhone"]),
			IsActive:    it["isActive"],
		})
	}
	return &cmdResult{action: "contact.search", data: ContactSearchResult{
		Query:    sa.query,
		Company:  sa.company,
		Count:    len(contacts),
		Contacts: contacts,
		Guidance: "Use a returned contact id as ticket_create/time_add contact. If the person is not found, ask the user whether to create a new contact and collect first name, last name, and email before calling contact_create.",
	}}, nil
}

func (a *App) cmdContactCreate(args []string) (*cmdResult, error) {
	fs := newFlagSet("contact create")
	company := fs.String("company", "", "customer alias or companyID")
	firstName := fs.String("first-name", "", "contact first name")
	lastName := fs.String("last-name", "", "contact last name")
	email := fs.String("email", "", "contact email address")
	dryRun := fs.Bool("dry-run", false, "preview without writing")
	if err := fs.Parse(args); err != nil {
		return nil, usageErr("contact create", err)
	}
	if strings.TrimSpace(*company) == "" {
		return nil, hinted("e.g. --company acme", "missing --company")
	}
	if strings.TrimSpace(*firstName) == "" {
		return nil, hinted("ask the user for the contact's first name", "missing --first-name")
	}
	if strings.TrimSpace(*lastName) == "" {
		return nil, hinted("ask the user for the contact's last name", "missing --last-name")
	}
	normalizedEmail, err := normalizeEmail(*email)
	if err != nil {
		return nil, err
	}
	companyID, err := a.cfg.ResolveCompany(*company)
	if err != nil {
		return nil, err
	}
	fields := contactFields(companyID, *firstName, *lastName, normalizedEmail)
	warnings := []string{"creating a contact changes customer CRM data; only do this after the user confirms the person does not already exist"}
	if *dryRun {
		return &cmdResult{action: "contact.create", dryRun: true, data: ContactCreateDryRun{Fields: fields, Warnings: warnings}}, nil
	}

	ctx, cancel := cmdContext()
	defer cancel()
	client, err := a.newClient(ctx)
	if err != nil {
		return nil, err
	}
	id, err := client.Create(ctx, atapi.EntityContacts, fields)
	if err != nil {
		return nil, err
	}
	return &cmdResult{action: "contact.create", data: ContactCreateResult{ContactID: id, Warnings: warnings}}, nil
}

func contactFields(companyID int, firstName, lastName, email string) map[string]any {
	return map[string]any{
		"companyID":    companyID,
		"firstName":    strings.TrimSpace(firstName),
		"lastName":     strings.TrimSpace(lastName),
		"emailAddress": strings.TrimSpace(email),
		"isActive":     1,
	}
}

func normalizeEmail(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", hinted("ask the user for the contact's email address", "missing --email")
	}
	addr, err := mail.ParseAddress(raw)
	if err != nil {
		return "", hinted("provide a normal email address, e.g. anna@example.com", "invalid --email %q", raw)
	}
	return strings.TrimSpace(addr.Address), nil
}
