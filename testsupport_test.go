package main

import (
	"autotask-easymode/internal/atapi"
	"autotask-easymode/internal/config"
	"autotask-easymode/internal/timer"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 6, 17, 10, 0, 0, 0, time.UTC)

type createCall struct {
	entity string
	fields map[string]any
}

// fakeClient is an in-memory autotaskClient for tests. It records writes and
// returns canned reads.
type fakeClient struct {
	companies    []map[string]any
	contacts     []map[string]any
	resources    []map[string]any
	tickets      []map[string]any
	entries      []map[string]any
	items        map[int64]map[string]any
	fields       []atapi.Field
	billingCodes []map[string]any

	creates []createCall
	updates []map[string]any
	nextID  int64
	failOn  string
	failAt  map[string]int
	counts  map[string]int

	// searchCompany records the companyID passed to the last SearchTickets call,
	// letting tests assert company-scoping (0 is a real company, not "all").
	searchCompany int
}

func (f *fakeClient) SearchCompanies(context.Context, string, int) ([]map[string]any, error) {
	return f.companies, nil
}

func (f *fakeClient) SearchContacts(context.Context, string, int, int) ([]map[string]any, error) {
	return f.contacts, nil
}

func (f *fakeClient) CreateContact(ctx context.Context, companyID int, fields map[string]any) (int64, error) {
	if _, ok := fields["companyID"]; !ok {
		fields["companyID"] = companyID
	}
	return f.Create(ctx, "CompanyContacts", fields)
}

func (f *fakeClient) SearchResources(context.Context, string, int) ([]map[string]any, error) {
	return f.resources, nil
}

func (f *fakeClient) SearchTickets(_ context.Context, _ string, companyID, _ int) ([]map[string]any, error) {
	f.searchCompany = companyID
	return f.tickets, nil
}

func (f *fakeClient) TicketsForCompany(context.Context, int, int) ([]map[string]any, error) {
	return f.tickets, nil
}

func (f *fakeClient) TimeEntriesForTickets(context.Context, []int64, string, string, int) ([]map[string]any, error) {
	return f.entries, nil
}

func (f *fakeClient) Create(_ context.Context, entity string, fields map[string]any) (int64, error) {
	if f.counts == nil {
		f.counts = map[string]int{}
	}
	f.counts[entity]++
	if n := f.failAt[entity]; n != 0 && f.counts[entity] == n {
		return 0, errors.New("create failed")
	}
	if f.failOn == entity {
		return 0, errors.New("create failed")
	}
	f.creates = append(f.creates, createCall{entity: entity, fields: fields})
	f.nextID++
	return 1000 + f.nextID, nil
}

func (f *fakeClient) Update(_ context.Context, _ string, fields map[string]any) (int64, error) {
	f.updates = append(f.updates, fields)
	return asInt64(fields["id"]), nil
}

func (f *fakeClient) GetByID(_ context.Context, _ string, id int64) (map[string]any, error) {
	if it, ok := f.items[id]; ok {
		return it, nil
	}
	return map[string]any{"id": id}, nil
}

func (f *fakeClient) EntityFields(context.Context, string) ([]atapi.Field, error) {
	return f.fields, nil
}

func (f *fakeClient) BillingCodes(context.Context, int) ([]map[string]any, error) {
	return f.billingCodes, nil
}

func newTestApp(t *testing.T, fc *fakeClient) *App {
	t.Helper()
	dir := t.TempDir()
	cfg, err := config.LoadFrom(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	state, err := timer.Load(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	app := &App{
		cfg:     cfg,
		state:   state,
		now:     func() time.Time { return testNow },
		journal: filepath.Join(dir, "write-journal.json"),
	}
	app.newClient = func(context.Context) (autotaskClient, error) {
		if fc == nil {
			return nil, errors.New("no client configured in test")
		}
		return fc, nil
	}
	return app
}

// dataMap returns res.data as a generic map by round-tripping through JSON. This
// verifies the actual serialized output and works whether data is a typed result
// struct or a map. Note: nested arrays come back as []any (JSON decoding).
func dataMap(t *testing.T, res *cmdResult) map[string]any {
	t.Helper()
	b, err := json.Marshal(res.data)
	if err != nil {
		t.Fatalf("marshal data: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("data is not a JSON object (%T): %v", res.data, err)
	}
	return m
}

func warningsContain(warnings []string, needle string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, needle) {
			return true
		}
	}
	return false
}
