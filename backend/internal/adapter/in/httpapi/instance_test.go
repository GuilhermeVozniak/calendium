package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// TestHandleInstance verifies GET /v1/instance is unauthenticated and renders
// the exact InstanceInfo JSON shape from the shared contract.
func TestHandleInstance(t *testing.T) {
	info := InstanceInfo{
		Name:          "Calendium",
		Mode:          ModeSelfHost,
		Version:       Version,
		AuthBaseURL:   "https://app.calendium.com/api/auth",
		AuthProviders: []string{"email", "google"},
		Features: InstanceFeatures{
			Billing:   false,
			Google:    true,
			Microsoft: false,
			AI:        true,
			Push:      false,
		},
	}
	h := New(Deps{
		Instance: info,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	// No Authorization header — the endpoint must be public.
	req := httptest.NewRequest(http.MethodGet, "/v1/instance", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// Round-trips into the typed struct unchanged.
	var got InstanceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(got, info) {
		t.Fatalf("body = %+v, want %+v", got, info)
	}

	// Exact JSON key shape (the discovery contract clients depend on).
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	for _, k := range []string{"name", "mode", "version", "authBaseUrl", "authProviders", "features"} {
		if _, ok := raw[k]; !ok {
			t.Fatalf("missing top-level key %q in %s", k, rec.Body.String())
		}
	}
	var features map[string]json.RawMessage
	if err := json.Unmarshal(raw["features"], &features); err != nil {
		t.Fatalf("decode features: %v", err)
	}
	for _, k := range []string{"billing", "google", "microsoft", "ai", "push"} {
		if _, ok := features[k]; !ok {
			t.Fatalf("missing features key %q in %s", k, rec.Body.String())
		}
	}
}
