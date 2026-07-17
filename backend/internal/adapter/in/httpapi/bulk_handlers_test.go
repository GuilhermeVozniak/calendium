package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

func TestBulkThreadActionsEndpoint(t *testing.T) {
	h := newHarness(t)
	h.mail.bulkResult = port.BulkActionResult{
		Threads:   []domain.Thread{{ID: "t1"}},
		FailedIDs: []string{"ghost"},
	}
	srv := httptest.NewServer(New(h.deps))
	defer srv.Close()

	body := bytes.NewBufferString(`{"threadIds":["t1","ghost"],"action":"archive"}`)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/mail/threads/bulk-actions", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+defaultToken)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var got port.BulkActionResult
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Threads) != 1 || !reflect.DeepEqual(got.FailedIDs, []string{"ghost"}) {
		t.Fatalf("body = %+v", got)
	}
	if !reflect.DeepEqual(h.mail.bulkThreadIDs, []string{"t1", "ghost"}) || h.mail.bulkAction != domain.ThreadActionArchive {
		t.Fatalf("service got ids=%v action=%q", h.mail.bulkThreadIDs, h.mail.bulkAction)
	}
}
