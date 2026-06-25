package main

import (
	"autotask-easymode/internal/atapi"
	"autotask-easymode/internal/config"
	"autotask-easymode/internal/timer"
	"context"
	"os"
	"strconv"
	"strings"
	"time"
)

// autotaskClient is the subset of the Autotask API the commands depend on.
// Defining it here (consumer-side) lets tests substitute a fake.
type autotaskClient interface {
	SearchCompanies(ctx context.Context, q string, limit int) ([]map[string]any, error)
	SearchResources(ctx context.Context, q string, limit int) ([]map[string]any, error)
	SearchTickets(ctx context.Context, q string, companyID, limit int) ([]map[string]any, error)
	TicketsForCompany(ctx context.Context, companyID, limit int) ([]map[string]any, error)
	TimeEntriesForTickets(ctx context.Context, ticketIDs []int64, from, to string, limit int) ([]map[string]any, error)
	Create(ctx context.Context, entity string, fields map[string]any) (int64, error)
	Update(ctx context.Context, entity string, fields map[string]any) (int64, error)
	GetByID(ctx context.Context, entity string, id int64) (map[string]any, error)
	EntityFields(ctx context.Context, entity string) ([]atapi.Field, error)
	BillingCodes(ctx context.Context, limit int) ([]map[string]any, error)
}

// App holds everything a command needs. Time and the API client are injected so
// the whole command layer is deterministic and testable without a live API.
type App struct {
	cfg     *config.Config
	state   *timer.State
	now     func() time.Time
	profile *TechnicianProfile

	// newClient builds an Autotask client on demand (zone detection happens
	// lazily and is cached). Tests override this with a fake.
	newClient func(ctx context.Context) (autotaskClient, error)
}

func (a *App) withProfile(profile *TechnicianProfile) *App {
	cp := *a
	cp.profile = profile
	return &cp
}

func (a *App) resourceID() int {
	if a.profile != nil && a.profile.ResourceID != 0 {
		return a.profile.ResourceID
	}
	return a.cfg.Resource()
}

func (a *App) roleID() int {
	if a.profile != nil && a.profile.RoleID != 0 {
		return a.profile.RoleID
	}
	return a.cfg.Defaults.RoleID
}

// newApp wires an App with real config, state and client factory.
func newApp() (*App, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if err := applyEnvDefaults(cfg); err != nil {
		return nil, err
	}
	statePath, err := config.StatePath()
	if err != nil {
		return nil, err
	}
	state, err := timer.Load(statePath)
	if err != nil {
		return nil, err
	}
	app := &App{cfg: cfg, state: state, now: time.Now}
	app.newClient = app.defaultClient
	return app, nil
}

func applyEnvDefaults(cfg *config.Config) error {
	ints := []struct {
		env string
		dst *int
	}{
		{env: "ATEM_QUEUE_ID", dst: &cfg.Defaults.QueueID},
		{env: "ATEM_PRIORITY", dst: &cfg.Defaults.Priority},
		{env: "ATEM_TICKET_STATUS_NEW", dst: &cfg.Defaults.TicketStatusNew},
		{env: "ATEM_TICKET_STATUS_COMPLETE", dst: &cfg.Defaults.TicketStatusComplete},
		{env: "ATEM_BILLING_CODE_ID", dst: &cfg.Defaults.BillingCodeID},
		{env: "ATEM_ROLE_ID", dst: &cfg.Defaults.RoleID},
		{env: "ATEM_FLAG_HOURS_OVER", dst: &cfg.Defaults.FlagHoursOver},
		{env: "ATEM_FLAG_NOTES_UNDER", dst: &cfg.Defaults.FlagNotesUnder},
		{env: "ATEM_FLAG_HOURS_ALWAYS", dst: &cfg.Defaults.FlagHoursAlways},
	}
	for _, item := range ints {
		raw := strings.TrimSpace(os.Getenv(item.env))
		if raw == "" {
			continue
		}
		n, err := strconv.Atoi(raw)
		if err != nil {
			return hinted("set it to a numeric id or unset the variable", "invalid %s %q", item.env, raw)
		}
		*item.dst = n
	}
	return nil
}

// defaultClient resolves credentials and the API zone (caching the zone URL),
// then returns a live Autotask client.
func (a *App) defaultClient(ctx context.Context) (autotaskClient, error) {
	if missing := a.cfg.MissingCredentials(); len(missing) > 0 {
		return nil, hinted(
			"set them via env (ATEM_USERNAME, ATEM_SECRET, ATEM_INTEGRATION_CODE) or in "+a.cfg.Path(),
			"missing Autotask credentials: %s", strings.Join(missing, ", "),
		)
	}
	username, secret, integrationCode := a.cfg.Credentials()
	base := a.cfg.APIBaseURL
	if base == "" {
		detected, err := atapi.DetectZone(ctx, username)
		if err != nil {
			return nil, err
		}
		base = detected
		a.cfg.APIBaseURL = base
		_ = a.cfg.Save() // best-effort zone cache; a failure here is non-fatal
	}
	return atapi.New(base, username, secret, integrationCode), nil
}
