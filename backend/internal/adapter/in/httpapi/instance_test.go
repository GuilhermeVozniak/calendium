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
		Name:            "Calendium",
		Mode:            ModeSelfHost,
		Version:         Version,
		AuthBaseURL:     "https://app.calendium.com/api/auth",
		AuthProviders:   []string{"email", "google"},
		WebURL:          "https://app.calendium.com",
		UndoSendSeconds: 15,
		Features: InstanceFeatures{
			Billing:   false,
			Google:    true,
			Microsoft: false,
			AI:        true,
			Push:      false,
			Email:     true,
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
	for _, k := range []string{"name", "mode", "version", "authBaseUrl", "authProviders", "webUrl", "undoSendSeconds", "features"} {
		if _, ok := raw[k]; !ok {
			t.Fatalf("missing top-level key %q in %s", k, rec.Body.String())
		}
	}
	var features map[string]json.RawMessage
	if err := json.Unmarshal(raw["features"], &features); err != nil {
		t.Fatalf("decode features: %v", err)
	}
	for _, k := range []string{"billing", "google", "microsoft", "ai", "push", "email"} {
		if _, ok := features[k]; !ok {
			t.Fatalf("missing features key %q in %s", k, rec.Body.String())
		}
	}
}

// TestHandleInstanceFeaturesEmailFalseIsExplicit: features.email is a
// boolean that is always present (clients gate the forgot-password form on
// `=== false`), so an unconfigured SMTP must serialize as false, not vanish.
func TestHandleInstanceFeaturesEmailFalseIsExplicit(t *testing.T) {
	h := New(Deps{Instance: InstanceInfo{Features: InstanceFeatures{Email: false}}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	req := httptest.NewRequest(http.MethodGet, "/v1/instance", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var features map[string]json.RawMessage
	if err := json.Unmarshal(raw["features"], &features); err != nil {
		t.Fatalf("decode features: %v", err)
	}
	if string(features["email"]) != "false" {
		t.Fatalf("features.email = %s, want false", features["email"])
	}
}

// Version is a linker-stamped variable (backend/Dockerfile ARG VERSION):
// the discovery document must echo whatever the build set.
func TestHandleInstance_EchoesTheStampedVersion(t *testing.T) {
	prev := Version
	Version = "9.9.9"
	t.Cleanup(func() { Version = prev })

	h := New(Deps{
		Instance: InstanceInfo{Name: "Calendium", Mode: ModeCloud, Version: Version, AuthProviders: []string{"email"}},
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/instance", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var got InstanceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Version != "9.9.9" {
		t.Fatalf("version = %q, want 9.9.9", got.Version)
	}
}

func TestVersion_DefaultsToDevForSourceBuilds(t *testing.T) {
	if Version != "dev" {
		t.Fatalf("Version = %q, want \"dev\" (release images set it via -ldflags -X)", Version)
	}
}
