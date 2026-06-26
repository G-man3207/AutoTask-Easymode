package main

import (
	"strconv"
	"strings"
)

func (a *App) cmdCompanySearch(args []string) (*cmdResult, error) {
	sa, err := parseSearch(args)
	if err != nil {
		return nil, usageErr("company search", err)
	}
	if sa.query == "" {
		return nil, hinted("usage: atem company search <query>", "no search query given")
	}

	ctx, cancel := cmdContext()
	defer cancel()
	client, err := a.newClient(ctx)
	if err != nil {
		return nil, err
	}
	items, err := client.SearchCompanies(ctx, sa.query, sa.limit)
	if err != nil {
		return nil, err
	}

	companies := make([]CompanyHit, 0, len(items))
	for _, it := range items {
		companies = append(companies, CompanyHit{
			ID:       asInt64(it["id"]),
			Name:     asString(it["companyName"]),
			IsActive: asBool(it["isActive"]),
		})
	}
	return &cmdResult{
		action: "company.search",
		data:   CompanySearchResult{Query: sa.query, Count: len(companies), Companies: companies},
	}, nil
}

func (a *App) cmdCompanyAlias(args []string) (*cmdResult, error) {
	fs := newFlagSet("company alias")
	if err := fs.Parse(args); err != nil {
		return nil, usageErr("company alias", err)
	}
	rest := fs.Args()
	if len(rest) != 2 {
		return nil, hinted("usage: atem company alias <name> <companyID>", "expected <name> <companyID>")
	}
	companyID, err := strconv.Atoi(strings.TrimSpace(rest[1]))
	if err != nil {
		return nil, hinted("companyID must be numeric", "invalid companyID %q", rest[1])
	}
	a.cfg.SetAlias(rest[0], companyID)
	if err := a.cfg.Save(); err != nil {
		return nil, err
	}
	return &cmdResult{
		action: "company.alias",
		data:   CompanyAliasResult{Alias: strings.ToLower(strings.TrimSpace(rest[0])), CompanyID: companyID},
	}, nil
}
