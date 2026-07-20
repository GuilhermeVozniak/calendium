package hubspot

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

// fakeHubSpot is an httptest-backed stand-in for the HubSpot v3 API. Each
// route records what it received so tests can assert exact request bodies.
type fakeHubSpot struct {
	mu    sync.Mutex
	paths []string

	searchStatus int             // 0 => 200
	searchBody   json.RawMessage // response for POST /crm/v3/objects/contacts/search
	gotSearch    map[string]any
	gotAuth      string

	assocBody json.RawMessage // GET .../associations/deals
	batchBody json.RawMessage // POST /crm/v3/objects/deals/batch/read
	gotBatch  map[string]any

	emailBody json.RawMessage // POST /crm/v3/objects/emails
	gotEmail  map[string]any

	assocPutStatus int // PUT .../associations/... (0 => 200)
	gotAssocPut    string
}

func (f *fakeHubSpot) handler(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	record := func(r *http.Request) {
		f.mu.Lock()
		f.paths = append(f.paths, r.Method+" "+r.URL.Path)
		f.gotAuth = r.Header.Get("Authorization")
		f.mu.Unlock()
	}
	mux.HandleFunc("POST /crm/v3/objects/contacts/search", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_ = json.NewDecoder(r.Body).Decode(&f.gotSearch)
		if f.searchStatus != 0 {
			w.WriteHeader(f.searchStatus)
			return
		}
		_, _ = w.Write(f.searchBody)
	})
	mux.HandleFunc("GET /account-info/v3/details", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_, _ = w.Write([]byte(`{"portalId": 424242}`))
	})
	mux.HandleFunc("GET /crm/v3/objects/contacts/{id}/associations/deals", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_, _ = w.Write(f.assocBody)
	})
	mux.HandleFunc("POST /crm/v3/objects/deals/batch/read", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_ = json.NewDecoder(r.Body).Decode(&f.gotBatch)
		_, _ = w.Write(f.batchBody)
	})
	mux.HandleFunc("POST /crm/v3/objects/emails", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_ = json.NewDecoder(r.Body).Decode(&f.gotEmail)
		_, _ = w.Write(f.emailBody)
	})
	mux.HandleFunc("PUT /crm/v3/objects/emails/{emailId}/associations/contacts/{contactId}/{assocType}", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		f.gotAssocPut = r.URL.Path
		if f.assocPutStatus != 0 {
			w.WriteHeader(f.assocPutStatus)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})
	return mux
}

func newTestClient(t *testing.T, f *fakeHubSpot) *Client {
	t.Helper()
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	c := NewClient(srv.Client())
	c.apiBase = srv.URL
	return c
}

const contactHit = `{"total":1,"results":[{"id":"301","properties":{
	"firstname":"Ada","lastname":"Lovelace","email":"ada@northwind.com",
	"company":"Northwind","jobtitle":"CTO","phone":"+1 555 0100","hubspot_owner_id":"7"}}]}`

func TestContactContextMapsContactAndDeals(t *testing.T) {
	f := &fakeHubSpot{
		searchBody: json.RawMessage(contactHit),
		assocBody:  json.RawMessage(`{"results":[{"id":"9001","type":"contact_to_deal"}]}`),
		batchBody: json.RawMessage(`{"results":[{"id":"9001","properties":{
			"dealname":"FY27 Renewal","dealstage":"contractsent","amount":"1200.5",
			"closedate":"2026-08-01T00:00:00Z"}}]}`),
	}
	c := newTestClient(t, f)

	got, err := c.ContactContext(context.Background(), "tok-1", "ada@northwind.com")
	if err != nil {
		t.Fatalf("ContactContext: %v", err)
	}

	if f.gotAuth != "Bearer tok-1" {
		t.Fatalf("Authorization = %q, want Bearer tok-1", f.gotAuth)
	}
	// The search body must filter on email EQ.
	filterJSON, _ := json.Marshal(f.gotSearch["filterGroups"])
	if want := `"propertyName":"email"`; !json.Valid(filterJSON) || !containsAll(string(filterJSON), want, `"operator":"EQ"`, `"value":"ada@northwind.com"`) {
		t.Fatalf("search filterGroups = %s", filterJSON)
	}

	if got.Vendor != domain.IntegrationHubSpot {
		t.Fatalf("vendor = %q", got.Vendor)
	}
	ct := got.Contact
	if ct == nil {
		t.Fatal("contact = nil, want mapped contact")
	}
	if ct.ID != "301" || ct.Name != "Ada Lovelace" || ct.Company != "Northwind" ||
		ct.Title != "CTO" || ct.Phone != "+1 555 0100" || ct.Owner != "7" || ct.Email != "ada@northwind.com" {
		t.Fatalf("contact = %+v", *ct)
	}
	if want := "https://app.hubspot.com/contacts/424242/record/0-1/301"; ct.VendorURL != want {
		t.Fatalf("contact VendorURL = %q, want %q", ct.VendorURL, want)
	}

	if len(got.Deals) != 1 {
		t.Fatalf("deals = %+v, want 1", got.Deals)
	}
	deal := got.Deals[0]
	if deal.ID != "9001" || deal.Name != "FY27 Renewal" || deal.Stage != "contractsent" {
		t.Fatalf("deal = %+v", deal)
	}
	if deal.Amount == nil || *deal.Amount != 1200.5 {
		t.Fatalf("deal amount = %v, want 1200.5", deal.Amount)
	}
	wantClose := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	if deal.CloseDate == nil || !deal.CloseDate.Equal(wantClose) {
		t.Fatalf("deal closeDate = %v, want %v", deal.CloseDate, wantClose)
	}
	if want := "https://app.hubspot.com/contacts/424242/record/0-3/9001"; deal.VendorURL != want {
		t.Fatalf("deal VendorURL = %q, want %q", deal.VendorURL, want)
	}
	// Batch read asked for the associated deal by id.
	batchJSON, _ := json.Marshal(f.gotBatch["inputs"])
	if !containsAll(string(batchJSON), `"id":"9001"`) {
		t.Fatalf("batch read inputs = %s", batchJSON)
	}
}

func TestContactContextMissReturnsNilContactWithoutExtraCalls(t *testing.T) {
	f := &fakeHubSpot{searchBody: json.RawMessage(`{"total":0,"results":[]}`)}
	c := newTestClient(t, f)

	got, err := c.ContactContext(context.Background(), "tok", "ghost@example.com")
	if err != nil {
		t.Fatalf("ContactContext: %v", err)
	}
	if got.Contact != nil {
		t.Fatalf("contact = %+v, want nil", got.Contact)
	}
	if got.Deals == nil || len(got.Deals) != 0 {
		t.Fatalf("deals = %#v, want empty non-nil slice", got.Deals)
	}
	if len(f.paths) != 1 {
		t.Fatalf("vendor calls = %v, want only the contact search", f.paths)
	}
}

func TestContactContext401MapsToErrUnauthorized(t *testing.T) {
	f := &fakeHubSpot{searchStatus: http.StatusUnauthorized}
	c := newTestClient(t, f)

	_, err := c.ContactContext(context.Background(), "expired", "a@b.c")
	if !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("err = %v, want domain.ErrUnauthorized", err)
	}
	if err := c.LogEmail(context.Background(), "expired", domain.CrmEmailLog{ContactEmail: "a@b.c"}); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("LogEmail err = %v, want domain.ErrUnauthorized", err)
	}
}

func TestLogEmailPostsEngagementAndAssociation(t *testing.T) {
	f := &fakeHubSpot{
		searchBody: json.RawMessage(contactHit),
		emailBody:  json.RawMessage(`{"id":"555"}`),
	}
	c := newTestClient(t, f)

	sentAt := time.Date(2026, 7, 18, 9, 30, 0, 0, time.UTC)
	err := c.LogEmail(context.Background(), "tok", domain.CrmEmailLog{
		ContactEmail: "ada@northwind.com",
		Subject:      "Renewal terms",
		BodyText:     "Attached the redlines.",
		SentAt:       sentAt,
		Direction:    "outbound",
	})
	if err != nil {
		t.Fatalf("LogEmail: %v", err)
	}

	props, _ := f.gotEmail["properties"].(map[string]any)
	if props["hs_email_subject"] != "Renewal terms" ||
		props["hs_email_text"] != "Attached the redlines." ||
		props["hs_email_direction"] != "EMAIL" ||
		props["hs_timestamp"] != "2026-07-18T09:30:00Z" {
		t.Fatalf("email properties = %+v", props)
	}
	if want := "/crm/v3/objects/emails/555/associations/contacts/301/email_to_contact"; f.gotAssocPut != want {
		t.Fatalf("association path = %q, want %q", f.gotAssocPut, want)
	}
}

func TestLogEmailInboundDirection(t *testing.T) {
	f := &fakeHubSpot{
		searchBody: json.RawMessage(contactHit),
		emailBody:  json.RawMessage(`{"id":"556"}`),
	}
	c := newTestClient(t, f)

	err := c.LogEmail(context.Background(), "tok", domain.CrmEmailLog{
		ContactEmail: "ada@northwind.com",
		Direction:    "inbound",
		SentAt:       time.Now(),
	})
	if err != nil {
		t.Fatalf("LogEmail: %v", err)
	}
	props, _ := f.gotEmail["properties"].(map[string]any)
	if props["hs_email_direction"] != "INCOMING_EMAIL" {
		t.Fatalf("direction = %v, want INCOMING_EMAIL", props["hs_email_direction"])
	}
}

func TestLogEmailUnknownContactIsNotFound(t *testing.T) {
	f := &fakeHubSpot{searchBody: json.RawMessage(`{"total":0,"results":[]}`)}
	c := newTestClient(t, f)

	err := c.LogEmail(context.Background(), "tok", domain.CrmEmailLog{ContactEmail: "ghost@example.com"})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want domain.ErrNotFound", err)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
