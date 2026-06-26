package atapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(srv.URL, "user", "secret", "code")
}

func TestDetectZone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("user"); got != "api@example.com" {
			t.Errorf("user param = %q", got)
		}
		_, _ = io.WriteString(w, `{"zoneName":"z","url":"https://webservices2.autotask.net/ATServicesRest/"}`)
	}))
	t.Cleanup(srv.Close)

	got, err := detectZone(context.Background(), srv.URL, "api@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://webservices2.autotask.net/ATServicesRest/" {
		t.Errorf("url = %q", got)
	}
}

func TestDetectZoneError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `boom`)
	}))
	t.Cleanup(srv.Close)

	if _, err := detectZone(context.Background(), srv.URL, "u"); err == nil {
		t.Fatal("expected error")
	}
}

func TestQueryPaginationAndAuthHeaders(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("UserName") != "user" || r.Header.Get("Secret") != "secret" || r.Header.Get("ApiIntegrationCode") != "code" {
			t.Errorf("missing auth headers: %v", r.Header)
		}
		if r.URL.Path != "/v1.0/Companies/query" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("cursor") == "2" {
			_, _ = io.WriteString(w, `{"items":[{"id":3}],"pageDetails":{"nextPageUrl":""}}`)
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("first page method = %s", r.Method)
		}
		next := "http://" + r.Host + r.URL.Path + "?cursor=2"
		_, _ = io.WriteString(w, `{"items":[{"id":1},{"id":2}],"pageDetails":{"nextPageUrl":"`+next+`"}}`)
	})

	items, err := c.Query(context.Background(), "Companies", []Filter{{Op: "exist", Field: "id"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("items = %d want 3", len(items))
	}
}

func TestQueryRetriesTransientRead(t *testing.T) {
	calls := 0
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"errors":["try again"]}`)
			return
		}
		_, _ = io.WriteString(w, `{"items":[{"id":1}],"pageDetails":{"nextPageUrl":""}}`)
	})

	items, err := c.Query(context.Background(), "Companies", []Filter{{Op: "exist", Field: "id"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d want 2", calls)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d want 1", len(items))
	}
}

func TestCreateDoesNotRetryTransientWrite(t *testing.T) {
	calls := 0
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"errors":["not yet"]}`)
	})

	_, err := c.Create(context.Background(), "Tickets", map[string]any{"title": "hello"})
	if err == nil {
		t.Fatal("expected transient write error")
	}
	if calls != 1 {
		t.Fatalf("calls = %d want 1", calls)
	}
}

func TestQueryLimit(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"items":[{"id":1},{"id":2},{"id":3}],"pageDetails":{"nextPageUrl":""}}`)
	})
	items, err := c.Query(context.Background(), "Companies", nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("limit not applied: %d", len(items))
	}
}

func TestCreate(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		var got map[string]any
		_ = json.Unmarshal(body, &got)
		if got["title"] != "hello" {
			t.Errorf("body title = %v", got["title"])
		}
		_, _ = io.WriteString(w, `{"itemId":555}`)
	})
	id, err := c.Create(context.Background(), "Tickets", map[string]any{"title": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if id != 555 {
		t.Errorf("id = %d", id)
	}
}

func TestUpdateAndGetByID(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPatch:
			_, _ = io.WriteString(w, `{"itemId":4242}`)
		case http.MethodGet:
			_, _ = io.WriteString(w, `{"item":{"id":4242,"title":"T"}}`)
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	})
	id, err := c.Update(context.Background(), "Tickets", map[string]any{"id": 4242, "status": 5})
	if err != nil || id != 4242 {
		t.Fatalf("update id=%d err=%v", id, err)
	}
	item, err := c.GetByID(context.Background(), "Tickets", 4242)
	if err != nil {
		t.Fatal(err)
	}
	if item["title"] != "T" {
		t.Errorf("title = %v", item["title"])
	}
}

func TestAPIErrorParsed(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"errors":["bad field X"]}`)
	})
	_, err := c.GetByID(context.Background(), "Tickets", 1)
	if err == nil || !strings.Contains(err.Error(), "bad field X") {
		t.Fatalf("error = %v", err)
	}
}

func TestSearchCompaniesBuildsContainsFilter(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"companyName"`) || !strings.Contains(string(body), `"contains"`) {
			t.Errorf("filter body = %s", body)
		}
		_, _ = io.WriteString(w, `{"items":[{"id":1,"companyName":"Acme Care"}],"pageDetails":{}}`)
	})
	items, err := c.SearchCompanies(context.Background(), "Acme", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d", len(items))
	}
}

func TestSearchContactsBuildsCompanyActiveNameEmailFilter(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s := string(body)
		if !strings.Contains(r.URL.Path, "/Contacts/query") {
			t.Errorf("path = %s", r.URL.Path)
		}
		for _, want := range []string{`"companyID"`, `"isActive"`, `"firstName"`, `"lastName"`, `"emailAddress"`, `"contains"`} {
			if !strings.Contains(s, want) {
				t.Errorf("contact filter missing %s: body=%s", want, s)
			}
		}
		_, _ = io.WriteString(w, `{"items":[{"id":9,"firstName":"Anna"}],"pageDetails":{}}`)
	})
	items, err := c.SearchContacts(context.Background(), "Anna", 7, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d", len(items))
	}
}

func TestCreateContactUsesCompanyChildPath(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/Companies/7/Contacts") {
			t.Errorf("path = %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		for _, want := range []string{`"companyID":7`, `"firstName":"Anna"`, `"emailAddress":"anna@example.com"`} {
			if !strings.Contains(string(body), want) {
				t.Errorf("body missing %s: %s", want, body)
			}
		}
		_, _ = io.WriteString(w, `{"itemId":9}`)
	})
	id, err := c.CreateContact(context.Background(), 7, map[string]any{
		"companyID":    7,
		"firstName":    "Anna",
		"lastName":     "Andersson",
		"emailAddress": "anna@example.com",
		"isActive":     1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != 9 {
		t.Fatalf("id = %d", id)
	}
}

func TestTimeEntriesForTicketsBuildsFilters(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s := string(body)
		for _, want := range []string{`"in"`, `"ticketID"`, `"gte"`, `"lte"`, `"dateWorked"`} {
			if !strings.Contains(s, want) {
				t.Errorf("filter missing %s: %s", want, s)
			}
		}
		_, _ = io.WriteString(w, `{"items":[],"pageDetails":{}}`)
	})
	if _, err := c.TimeEntriesForTickets(context.Background(), []int64{1, 2}, "2026-01-01T00:00:00", "2026-01-31T23:59:59", 0); err != nil {
		t.Fatal(err)
	}
}

func TestSearchTicketsScopesByCompany(t *testing.T) {
	// company 0 is a real company (the owner org) and must add a companyID filter.
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s := string(body)
		if !strings.Contains(s, `"companyID"`) || !strings.Contains(s, `"title"`) {
			t.Errorf("company 0 should scope; body = %s", s)
		}
		_, _ = io.WriteString(w, `{"items":[],"pageDetails":{}}`)
	})
	if _, err := c.SearchTickets(context.Background(), "x", 0, 0); err != nil {
		t.Fatal(err)
	}

	// AllCompanies must NOT add a companyID filter.
	c2 := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), `"companyID"`) {
			t.Errorf("AllCompanies must not scope; body = %s", body)
		}
		_, _ = io.WriteString(w, `{"items":[],"pageDetails":{}}`)
	})
	if _, err := c2.SearchTickets(context.Background(), "x", AllCompanies, 0); err != nil {
		t.Fatal(err)
	}
}
