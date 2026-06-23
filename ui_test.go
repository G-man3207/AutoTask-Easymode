package main

import (
	"autotask-easymode/internal/atapi"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func uiDo(t *testing.T, h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), method, target, strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestUIServesHTML(t *testing.T) {
	app := newTestApp(t, nil)
	rr := uiDo(t, app.uiHandler(), http.MethodGet, "/", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "atem") {
		t.Error("html body missing title")
	}
}

func TestUIConfigRoundTrip(t *testing.T) {
	app := newTestApp(t, nil)
	h := app.uiHandler()

	body := `{"username":"api@example.test","secret":"sek","integrationCode":"ic","queueId":8,"resourceId":1001,"flagHoursAlways":16}`
	if rr := uiDo(t, h, http.MethodPost, "/api/config", body); rr.Code != http.StatusOK {
		t.Fatalf("save code = %d (%s)", rr.Code, rr.Body.String())
	}
	if app.cfg.Username != "api@example.test" || app.cfg.Secret != "sek" || app.cfg.Defaults.QueueID != 8 {
		t.Errorf("config not applied: %+v", app.cfg)
	}
	if app.cfg.ResourceID != 1001 || app.cfg.Defaults.FlagHoursAlways != 16 {
		t.Error("numeric fields not applied")
	}

	rr := uiDo(t, h, http.MethodGet, "/api/config", "")
	var got uiConfig
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Username != "api@example.test" || got.Secret != "sek" || got.QueueID != 8 {
		t.Errorf("get config = %+v", got)
	}
}

func TestUIDoctorEndpoint(t *testing.T) {
	fc := &fakeClient{
		fields: []atapi.Field{
			{Name: "status", IsPickList: true, PicklistValues: []atapi.PicklistValue{{Value: "1", Label: "New", IsActive: true}}},
		},
		billingCodes: []map[string]any{{"id": float64(14), "name": "Consulting"}},
	}
	app := newTestApp(t, fc)
	app.cfg.Username, app.cfg.Secret, app.cfg.IntegrationCode = "u", "s", "i"

	rr := uiDo(t, app.uiHandler(), http.MethodPost, "/api/doctor", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("doctor code = %d", rr.Code)
	}
	var d map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if pick, _ := d["picklists"].(map[string]any); pick == nil || pick["ticketStatus"] == nil {
		t.Errorf("expected picklists in doctor response: %v", d)
	}
}

func TestUIMethodNotAllowed(t *testing.T) {
	app := newTestApp(t, nil)
	if rr := uiDo(t, app.uiHandler(), http.MethodGet, "/api/doctor", ""); rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}
