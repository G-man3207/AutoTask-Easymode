package main

func (a *App) cmdResourceSearch(args []string) (*cmdResult, error) {
	sa, err := parseSearch(args)
	if err != nil {
		return nil, usageErr("resource search", err)
	}
	if sa.query == "" {
		return nil, hinted("usage: atem resource search <name or email>", "no search query given")
	}

	ctx, cancel := cmdContext()
	defer cancel()
	client, err := a.newClient(ctx)
	if err != nil {
		return nil, err
	}
	items, err := client.SearchResources(ctx, sa.query, sa.limit)
	if err != nil {
		return nil, err
	}

	resources := make([]ResourceHit, 0, len(items))
	for _, it := range items {
		resources = append(resources, ResourceHit{
			ID:        asInt64(it["id"]),
			FirstName: asString(it["firstName"]),
			LastName:  asString(it["lastName"]),
			Email:     asString(it["email"]),
			IsActive:  asBool(it["isActive"]),
		})
	}
	return &cmdResult{
		action: "resource.search",
		data:   ResourceSearchResult{Query: sa.query, Count: len(resources), Resources: resources},
	}, nil
}
