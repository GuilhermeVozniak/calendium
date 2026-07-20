package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

type fakePlacesService struct {
	ret      []domain.Place
	err      error
	calls    int
	gotUser  string
	gotQuery string
}

func (f *fakePlacesService) Autocomplete(_ context.Context, userID, query string) ([]domain.Place, error) {
	f.calls++
	f.gotUser = userID
	f.gotQuery = query
	return f.ret, f.err
}

var _ port.PlacesService = (*fakePlacesService)(nil)

func TestPlacesAutocompleteAnswers501WhenUnwired(t *testing.T) {
	h := newHarness(t) // Places left nil, mirroring an unconfigured deployment
	rec := h.authed(http.MethodGet, "/v1/places/autocomplete?q=berlin", nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501 (body=%s)", rec.Code, rec.Body.String())
	}
	if got := decodeErr(t, rec).Code; got != "not_implemented" {
		t.Fatalf("error code = %q, want not_implemented", got)
	}
}

func TestPlacesAutocompleteRequiresAuth(t *testing.T) {
	h := newHarness(t)
	h.deps.Places = &fakePlacesService{}
	rec := h.anon(http.MethodGet, "/v1/places/autocomplete?q=berlin", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestPlacesAutocompleteServesSuggestions(t *testing.T) {
	h := newHarness(t)
	fake := &fakePlacesService{ret: []domain.Place{
		{Name: "Berlin", Address: "Berlin, Germany", Lat: 52.52, Lon: 13.405},
	}}
	h.deps.Places = fake

	// The querystring value arrives percent-decoded at the service.
	rec := h.authed(http.MethodGet, "/v1/places/autocomplete?q=caf%C3%A9%20berlin", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var got []domain.Place
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Berlin" || got[0].Lat != 52.52 {
		t.Fatalf("got = %+v", got)
	}
	if fake.gotUser != defaultUserID {
		t.Fatalf("gotUser = %q, want %q", fake.gotUser, defaultUserID)
	}
	if fake.gotQuery != "café berlin" {
		t.Fatalf("gotQuery = %q, want the decoded user query", fake.gotQuery)
	}
}

func TestPlacesAutocompleteMapsServiceErrors(t *testing.T) {
	h := newHarness(t)
	h.deps.Places = &fakePlacesService{err: fmt.Errorf("%w: query must be at least 3 characters", domain.ErrValidation)}
	rec := h.authed(http.MethodGet, "/v1/places/autocomplete?q=ab", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
}
