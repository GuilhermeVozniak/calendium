package openmeteo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"calendium/backend/internal/domain"
)

const forecastJSON = `{
	"daily": {
		"time": ["2026-07-19", "2026-07-20", "2026-07-21"],
		"weather_code": [0, 61, 95],
		"temperature_2m_max": [24.6, 19.2, 17.8],
		"temperature_2m_min": [13.1, 12.4, 11.0],
		"precipitation_probability_max": [5, 80, null]
	}
}`

func TestDailyForecastSendsQueryAndMapsFields(t *testing.T) {
	var gotPath string
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(forecastJSON))
	}))
	defer srv.Close()

	got, err := New(srv.URL).DailyForecast(context.Background(), 52.52, 13.405, "Europe/Berlin", 3)
	if err != nil {
		t.Fatalf("DailyForecast: %v", err)
	}

	if gotPath != "/v1/forecast" {
		t.Fatalf("path = %q, want /v1/forecast", gotPath)
	}
	wantParams := map[string]string{
		"latitude":      "52.5200",
		"longitude":     "13.4050",
		"daily":         "weather_code,temperature_2m_max,temperature_2m_min,precipitation_probability_max",
		"timezone":      "Europe/Berlin",
		"forecast_days": "3",
	}
	for key, want := range wantParams {
		if gotQuery.Get(key) != want {
			t.Errorf("query %s = %q, want %q", key, gotQuery.Get(key), want)
		}
	}

	want := []domain.DayForecast{
		{Date: "2026-07-19", Code: 0, HighCelsius: 24.6, LowCelsius: 13.1, PrecipChance: 5},
		{Date: "2026-07-20", Code: 61, HighCelsius: 19.2, LowCelsius: 12.4, PrecipChance: 80},
		// Null precipitation probability degrades to 0, never an error.
		{Date: "2026-07-21", Code: 95, HighCelsius: 17.8, LowCelsius: 11.0, PrecipChance: 0},
	}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (%+v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("day[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestDailyForecastErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"reason":"out of range"}`, http.StatusBadRequest)
	}))
	defer srv.Close()

	if _, err := New(srv.URL).DailyForecast(context.Background(), 0, 0, "UTC", 7); err == nil {
		t.Fatal("non-200 status accepted, want error")
	}
}

func TestDailyForecastMalformedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	if _, err := New(srv.URL).DailyForecast(context.Background(), 0, 0, "UTC", 7); err == nil {
		t.Fatal("malformed body accepted, want error")
	}
}

func TestNewTrimsTrailingSlashAndSetsTimeout(t *testing.T) {
	c := New("https://api.open-meteo.com/")
	if c.baseURL != "https://api.open-meteo.com" {
		t.Fatalf("baseURL = %q", c.baseURL)
	}
	if c.HTTP.Timeout != requestTimeout {
		t.Fatalf("timeout = %v, want %v (short, fail-soft)", c.HTTP.Timeout, requestTimeout)
	}
}
