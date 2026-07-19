package httpapi

// Handler tests for GET /v1/weather (M2.8 Task 13).

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

type fakeWeatherService struct {
	ret       []domain.DayForecast
	err       error
	calls     int
	gotUserID string
	gotLat    float64
	gotLon    float64
	gotTZ     string
	gotDays   int
}

func (f *fakeWeatherService) Forecast(_ context.Context, userID string, lat, lon float64, tz string, days int) ([]domain.DayForecast, error) {
	f.calls++
	f.gotUserID, f.gotLat, f.gotLon, f.gotTZ, f.gotDays = userID, lat, lon, tz, days
	return f.ret, f.err
}

var _ port.WeatherService = (*fakeWeatherService)(nil)

func TestWeatherRequiresAuth(t *testing.T) {
	h := newHarness(t)
	h.deps.Weather = &fakeWeatherService{}
	rec := h.anon(http.MethodGet, "/v1/weather?lat=1&lon=2&tz=UTC", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestWeatherNotConfiguredAnswers501(t *testing.T) {
	h := newHarness(t) // Weather left nil (no vendor configured)
	rec := h.authed(http.MethodGet, "/v1/weather?lat=1&lon=2&tz=UTC", nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501 when weather is not configured", rec.Code)
	}
	if got := decodeErr(t, rec).Code; got != "not_implemented" {
		t.Fatalf("error code = %q, want not_implemented", got)
	}
}

func TestWeatherValidatesParams(t *testing.T) {
	fake := &fakeWeatherService{}
	h := newHarness(t)
	h.deps.Weather = fake

	for name, target := range map[string]string{
		"missing lat":     "/v1/weather?lon=2&tz=UTC",
		"garbage lat":     "/v1/weather?lat=north&lon=2&tz=UTC",
		"garbage lon":     "/v1/weather?lat=1&lon=west&tz=UTC",
		"missing tz":      "/v1/weather?lat=1&lon=2",
		"fractional days": "/v1/weather?lat=1&lon=2&tz=UTC&days=2.5",
		"garbage days":    "/v1/weather?lat=1&lon=2&tz=UTC&days=soon",
	} {
		rec := h.authed(http.MethodGet, target, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, rec.Code)
		}
	}
	if fake.calls != 0 {
		t.Fatalf("service called %d times on invalid input, want 0", fake.calls)
	}
}

func TestWeatherPassesParamsAndReturnsRows(t *testing.T) {
	rows := []domain.DayForecast{
		{Date: "2026-07-19", Code: 3, HighCelsius: 24.5, LowCelsius: 14.1, PrecipChance: 20},
		{Date: "2026-07-20", Code: 61, HighCelsius: 19.0, LowCelsius: 12.0, PrecipChance: 85},
	}
	fake := &fakeWeatherService{ret: rows}
	h := newHarness(t)
	h.deps.Weather = fake

	rec := h.authed(http.MethodGet, "/v1/weather?lat=52.52&lon=13.405&tz=Europe%2FBerlin&days=5", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if fake.gotUserID != defaultUserID || fake.gotLat != 52.52 || fake.gotLon != 13.405 ||
		fake.gotTZ != "Europe/Berlin" || fake.gotDays != 5 {
		t.Fatalf("service called with (%q, %v, %v, %q, %d)", fake.gotUserID, fake.gotLat, fake.gotLon, fake.gotTZ, fake.gotDays)
	}
	var got []domain.DayForecast
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	if len(got) != 2 || got[0] != rows[0] || got[1] != rows[1] {
		t.Fatalf("rows = %+v, want %+v", got, rows)
	}
}

func TestWeatherDefaultsDaysToSeven(t *testing.T) {
	fake := &fakeWeatherService{}
	h := newHarness(t)
	h.deps.Weather = fake

	rec := h.authed(http.MethodGet, "/v1/weather?lat=1&lon=2&tz=UTC", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if fake.gotDays != 7 {
		t.Fatalf("days = %d, want default 7", fake.gotDays)
	}
}

func TestWeatherMapsServiceErrors(t *testing.T) {
	h := newHarness(t)
	h.deps.Weather = &fakeWeatherService{err: domain.ErrPaymentRequired}
	rec := h.authed(http.MethodGet, "/v1/weather?lat=1&lon=2&tz=UTC", nil)
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402", rec.Code)
	}
	if got := decodeErr(t, rec).Code; got != "payment_required" {
		t.Fatalf("error code = %q, want payment_required", got)
	}
}
