package domain

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseIntegrationVendor(t *testing.T) {
	for _, valid := range []string{"todoist", "hubspot"} {
		v, err := ParseIntegrationVendor(valid)
		if err != nil {
			t.Fatalf("ParseIntegrationVendor(%q) error = %v, want nil", valid, err)
		}
		if string(v) != valid {
			t.Fatalf("ParseIntegrationVendor(%q) = %q", valid, v)
		}
	}
	for _, invalid := range []string{"", "linear", "TODOIST", "todoist ", "google"} {
		if _, err := ParseIntegrationVendor(invalid); !errors.Is(err, ErrValidation) {
			t.Fatalf("ParseIntegrationVendor(%q) error = %v, want ErrValidation", invalid, err)
		}
	}
}

// TestIntegrationConnectionJSONNeverLeaksOwnerOrTokens is the leak-guard: the
// serialized connection must not carry the owner id, and the entity has no
// token fields at all — any "token"-ish key in the JSON is a regression.
func TestIntegrationConnectionJSONNeverLeaksOwnerOrTokens(t *testing.T) {
	lastErr := "boom"
	c := IntegrationConnection{
		ID:              "conn1",
		UserID:          "secret-owner-id",
		Vendor:          IntegrationTodoist,
		ExternalAccount: "person@example.com",
		Status:          IntegrationStatusError,
		LastError:       &lastErr,
		CreatedAt:       time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC),
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(b)
	if strings.Contains(body, "secret-owner-id") {
		t.Fatalf("serialized connection leaks the owner id: %s", body)
	}
	if strings.Contains(strings.ToLower(body), "token") {
		t.Fatalf("serialized connection carries a token-ish field: %s", body)
	}
	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := decoded["userId"]; ok {
		t.Fatalf("serialized connection has a userId key: %s", body)
	}
	for _, want := range []string{"id", "vendor", "externalAccount", "status", "lastError", "createdAt"} {
		if _, ok := decoded[want]; !ok {
			t.Fatalf("serialized connection missing %q: %s", want, body)
		}
	}
}
