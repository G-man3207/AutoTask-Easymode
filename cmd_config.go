package main

import (
	"autotask-easymode/internal/atapi"
	"context"
	"strings"
)

const billingCodeLimit = 200

// configData returns a display-safe view of the current configuration, with
// secrets redacted and env-resolved values shown.
func (a *App) configData() ConfigView {
	username, secret, integrationCode := a.cfg.Credentials()
	return ConfigView{
		Path:               a.cfg.Path(),
		Username:           username,
		IntegrationCode:    redact(integrationCode),
		Secret:             redact(secret),
		APIBaseURL:         a.cfg.APIBaseURL,
		ResourceID:         a.cfg.Resource(),
		Defaults:           a.cfg.Defaults,
		Aliases:            a.cfg.Aliases,
		MissingCredentials: a.cfg.MissingCredentials(),
	}
}

func (a *App) cmdConfigShow(args []string) (*cmdResult, error) {
	fs := newFlagSet("config show")
	if err := fs.Parse(args); err != nil {
		return nil, usageErr("config show", err)
	}
	return &cmdResult{action: "config.show", data: a.configData()}, nil
}

func (a *App) cmdConfigSet(args []string) (*cmdResult, error) {
	fs := newFlagSet("config set")
	if err := fs.Parse(args); err != nil {
		return nil, usageErr("config set", err)
	}
	rest := fs.Args()
	if len(rest) != 2 {
		return nil, hinted("usage: atem config set <key> <value>", "expected <key> <value>")
	}
	if err := a.setConfigValue(rest[0], rest[1]); err != nil {
		return nil, err
	}
	if err := a.cfg.Save(); err != nil {
		return nil, err
	}
	return &cmdResult{action: "config.set", data: a.configData()}, nil
}

// setConfigValue assigns a single config key. Secrets are intentionally not
// settable here to keep them out of shell history; use ATEM_SECRET instead.
func (a *App) setConfigValue(key, val string) error {
	switch strings.ToLower(key) {
	case "username":
		a.cfg.Username = val
	case "integrationcode":
		a.cfg.IntegrationCode = val
	case "apibaseurl":
		a.cfg.APIBaseURL = val
	case "resourceid":
		return setInt(&a.cfg.ResourceID, val)
	case "queueid":
		return setInt(&a.cfg.Defaults.QueueID, val)
	case "priority":
		return setInt(&a.cfg.Defaults.Priority, val)
	case "ticketstatusnew":
		return setInt(&a.cfg.Defaults.TicketStatusNew, val)
	case "ticketstatuscomplete":
		return setInt(&a.cfg.Defaults.TicketStatusComplete, val)
	case "billingcodeid":
		return setInt(&a.cfg.Defaults.BillingCodeID, val)
	case "roleid":
		return setInt(&a.cfg.Defaults.RoleID, val)
	case "flaghoursover":
		return setInt(&a.cfg.Defaults.FlagHoursOver, val)
	case "flagnotesunder":
		return setInt(&a.cfg.Defaults.FlagNotesUnder, val)
	case "flaghoursalways":
		return setInt(&a.cfg.Defaults.FlagHoursAlways, val)
	default:
		return hinted(
			"valid keys: username, integrationCode, apiBaseUrl, resourceId, queueId, priority, ticketStatusNew, ticketStatusComplete, billingCodeId, roleId, flagHoursOver, flagNotesUnder, flagHoursAlways",
			"unknown config key %q", key,
		)
	}
	return nil
}

// cmdConfigDoctor verifies credentials and the API zone, then lists the
// org-specific picklist IDs (ticket status/priority/queue, work types) for the
// technician to fill in config without guessing. It degrades gracefully: each
// section reports its own error rather than failing the whole command.
func (a *App) cmdConfigDoctor(args []string) (*cmdResult, error) {
	fs := newFlagSet("config doctor")
	if err := fs.Parse(args); err != nil {
		return nil, usageErr("config doctor", err)
	}
	ctx, cancel := cmdContext()
	defer cancel()
	return &cmdResult{action: "config.doctor", data: a.doctorReport(ctx)}, nil
}

// doctorReport builds the diagnostics map. It takes a context so callers (CLI and
// the web UI) can supply their own and propagate cancellation.
func (a *App) doctorReport(ctx context.Context) map[string]any {
	report := map[string]any{"config": a.configData()}
	missing := a.cfg.MissingCredentials()
	report["credentials"] = map[string]any{"ok": len(missing) == 0, "missing": missing}
	if len(missing) > 0 {
		report["recommend"] = []string{"set credentials first via env: ATEM_USERNAME, ATEM_SECRET, ATEM_INTEGRATION_CODE"}
		return report
	}
	client, err := a.newClient(ctx)
	if err != nil {
		report["zone"] = map[string]any{"ok": false, "error": err.Error()}
		return report
	}
	report["zone"] = map[string]any{"ok": true, "apiBaseUrl": a.cfg.APIBaseURL}
	report["picklists"] = a.doctorPicklists(ctx, client)
	report["recommend"] = a.doctorRecommendations()
	return report
}

// doctorPicklists fetches the org-specific picklists; an authenticated call here
// also confirms the credentials work.
func (a *App) doctorPicklists(ctx context.Context, client autotaskClient) map[string]any {
	out := map[string]any{}
	if fields, err := client.EntityFields(ctx, atapi.EntityTickets); err != nil {
		out["ticketFieldsError"] = err.Error()
	} else {
		out["ticketStatus"] = pickActive(fields, "status")
		out["ticketPriority"] = pickActive(fields, "priority")
		out["ticketQueue"] = pickActive(fields, "queueID")
	}
	if codes, err := client.BillingCodes(ctx, billingCodeLimit); err != nil {
		out["workTypesError"] = err.Error()
	} else {
		out["workTypes"] = slimBillingCodes(codes)
	}
	return out
}

// doctorRecommendations lists the unset-but-useful config with the command to fix.
func (a *App) doctorRecommendations() []string {
	var rec []string
	d := a.cfg.Defaults
	if d.QueueID == 0 {
		rec = append(rec, "queueId is unset (required to create tickets); pick from picklists.ticketQueue: atem config set queueId <id>")
	}
	if d.TicketStatusNew == 0 {
		rec = append(rec, "ticketStatusNew is unset (new-ticket status); pick from picklists.ticketStatus: atem config set ticketStatusNew <id>")
	}
	if d.TicketStatusComplete == 0 {
		rec = append(rec, "ticketStatusComplete is unset (status used to close tickets); pick from picklists.ticketStatus: atem config set ticketStatusComplete <id>")
	}
	if d.Priority == 0 {
		rec = append(rec, "priority is unset (ticket priority); pick from picklists.ticketPriority: atem config set priority <id>")
	}
	if a.cfg.Resource() == 0 {
		rec = append(rec, `resourceId is unset (who time is logged as); atem resource search "<your name>" then atem config set resourceId <id>`)
	}
	if d.BillingCodeID == 0 {
		rec = append(rec, "billingCodeId is unset (work type for time entries); pick from picklists.workTypes: atem config set billingCodeId <id>")
	}
	if len(rec) == 0 {
		rec = append(rec, "all key defaults are set; you're ready to log time")
	}
	return rec
}

// pickActive returns the active values of a named picklist field as {id,label}.
func pickActive(fields []atapi.Field, name string) []map[string]any {
	out := []map[string]any{}
	for _, f := range fields {
		if !strings.EqualFold(f.Name, name) {
			continue
		}
		for _, v := range f.PicklistValues {
			if v.IsActive {
				out = append(out, map[string]any{"id": v.Value, "label": v.Label, "default": v.IsDefault})
			}
		}
	}
	return out
}

// slimBillingCodes projects billing codes to the fields useful for picking a work type.
func slimBillingCodes(codes []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(codes))
	for _, c := range codes {
		out = append(out, map[string]any{
			"id":      asInt64(c["id"]),
			"name":    asString(c["name"]),
			"useType": c["useType"],
		})
	}
	return out
}
