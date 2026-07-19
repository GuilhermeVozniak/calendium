// Package openmeteo implements port.WeatherProvider against the Open-Meteo
// forecast API (https://open-meteo.com) using only the standard library.
// Open-Meteo is keyless; the base URL is configurable (OPEN_METEO_URL) so
// tests and self-hosters can point elsewhere. Weather is best-effort
// decoration, so the HTTP client uses a deliberately short timeout — a slow
// vendor must never stall a calendar surface.
package openmeteo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// requestTimeout bounds every forecast call (fail-soft: weather is
// decoration, never worth a slow page).
const requestTimeout = 5 * time.Second

// dailyFields is the Open-Meteo `daily` parameter this adapter requests.
const dailyFields = "weather_code,temperature_2m_max,temperature_2m_min,precipitation_probability_max"

// Client calls GET {base}/v1/forecast.
type Client struct {
	baseURL string
	// HTTP is exported so the composition root or tests can substitute a
	// client; New seeds it with the short requestTimeout.
	HTTP *http.Client
}

func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Timeout: requestTimeout},
	}
}

// dailyPayload mirrors the slice-per-field shape of Open-Meteo's `daily`
// block. Pointer elements: Open-Meteo sends explicit nulls for values it
// cannot provide (e.g. precipitation probability outside its window), and a
// null must not fail the whole forecast.
type dailyPayload struct {
	Time         []string   `json:"time"`
	WeatherCode  []*int     `json:"weather_code"`
	TempMax      []*float64 `json:"temperature_2m_max"`
	TempMin      []*float64 `json:"temperature_2m_min"`
	PrecipChance []*int     `json:"precipitation_probability_max"`
}

func deref[T any](s []*T, i int) T {
	var zero T
	if i >= len(s) || s[i] == nil {
		return zero
	}
	return *s[i]
}

func (c *Client) DailyForecast(ctx context.Context, lat, lon float64, timeZone string, days int) ([]domain.DayForecast, error) {
	q := url.Values{}
	q.Set("latitude", strconv.FormatFloat(lat, 'f', 4, 64))
	q.Set("longitude", strconv.FormatFloat(lon, 'f', 4, 64))
	q.Set("daily", dailyFields)
	q.Set("timezone", timeZone)
	q.Set("forecast_days", strconv.Itoa(days))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/forecast?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openmeteo: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openmeteo: forecast returned status %d", res.StatusCode)
	}

	var body struct {
		Daily dailyPayload `json:"daily"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("openmeteo: decode forecast: %w", err)
	}
	d := body.Daily
	out := make([]domain.DayForecast, 0, len(d.Time))
	for i, date := range d.Time {
		out = append(out, domain.DayForecast{
			Date:         date,
			Code:         deref(d.WeatherCode, i),
			HighCelsius:  deref(d.TempMax, i),
			LowCelsius:   deref(d.TempMin, i),
			PrecipChance: deref(d.PrecipChance, i),
		})
	}
	return out, nil
}

var _ port.WeatherProvider = (*Client)(nil)
