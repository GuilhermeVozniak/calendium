// Package nominatim implements port.MapsProvider against a Nominatim
// geocoding endpoint (place autocomplete) and an OSRM routing endpoint
// (travel-time estimates). Both are plain JSON-over-HTTP APIs spoken with
// the standard library only; base URLs are configurable so self-hosters can
// point at their own instances.
//
// Nominatim's usage policy (https://operations.osmfoundation.org/policies/
// nominatim/) requires an identifying User-Agent and at most one request per
// second: every request carries UserAgent, and Nominatim calls are
// serialized with a minimum spacing (see pace).
package nominatim

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// UserAgent identifies Calendium to the vendors per Nominatim's usage policy.
const UserAgent = "calendium/1.0"

// minSpacing is the minimum gap between two Nominatim requests (1 req/s).
const minSpacing = time.Second

// transitFactor approximates transit as driving time * 1.5 until a real
// transit vendor lands (per the M2.8 plan).
const transitFactor = 1.5

// maxResponseBytes bounds vendor response reads.
const maxResponseBytes = 1 << 20

// Client implements port.MapsProvider. Construct with New.
type Client struct {
	nominatimBase string
	osrmBase      string
	hc            *http.Client

	// now/sleep are swappable in tests so pacing is assertable without
	// real waiting.
	now   func() time.Time
	sleep func(time.Duration)

	mu   sync.Mutex // serializes Nominatim requests (1 req/s policy)
	last time.Time  // completion time of the previous Nominatim request
}

var _ port.MapsProvider = (*Client)(nil)

// New builds a MapsProvider from the two vendor base URLs (trailing slashes
// tolerated; either may be empty to disable that half). hc == nil falls back
// to a 10-second-timeout client — vendor calls must never hang a handler.
func New(nominatimBaseURL, osrmBaseURL string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{
		nominatimBase: strings.TrimRight(nominatimBaseURL, "/"),
		osrmBase:      strings.TrimRight(osrmBaseURL, "/"),
		hc:            hc,
		now:           time.Now,
		sleep:         time.Sleep,
	}
}

// pace serializes Nominatim calls and enforces minSpacing between them. It
// returns holding the lock; the returned func stamps the completion time and
// releases it.
func (c *Client) pace() (done func()) {
	c.mu.Lock()
	if !c.last.IsZero() {
		if wait := minSpacing - c.now().Sub(c.last); wait > 0 {
			c.sleep(wait)
		}
	}
	return func() {
		c.last = c.now()
		c.mu.Unlock()
	}
}

// nominatimPlace is one row of a Nominatim /search jsonv2 response.
type nominatimPlace struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Lat         string `json:"lat"`
	Lon         string `json:"lon"`
}

// Autocomplete implements port.MapsProvider via GET {base}/search?format=jsonv2.
// The user's query string is percent-encoded via url.Values (M2.5 lesson:
// user input never lands raw in a vendor URL).
func (c *Client) Autocomplete(ctx context.Context, query string, limit int) ([]domain.Place, error) {
	if c.nominatimBase == "" {
		return nil, fmt.Errorf("nominatim: no base URL configured")
	}
	if limit <= 0 {
		limit = 5
	}
	q := url.Values{}
	q.Set("format", "jsonv2")
	q.Set("q", query)
	q.Set("limit", strconv.Itoa(limit))
	reqURL := c.nominatimBase + "/search?" + q.Encode()

	done := c.pace()
	defer done()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("nominatim: build request: %w", err)
	}
	req.Header.Set("User-Agent", UserAgent)
	res, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("nominatim: search: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nominatim: search returned status %d", res.StatusCode)
	}
	var rows []nominatimPlace
	if err := json.NewDecoder(io.LimitReader(res.Body, maxResponseBytes)).Decode(&rows); err != nil {
		return nil, fmt.Errorf("nominatim: decode search response: %w", err)
	}
	places := make([]domain.Place, 0, len(rows))
	for _, row := range rows {
		lat, latErr := strconv.ParseFloat(row.Lat, 64)
		lon, lonErr := strconv.ParseFloat(row.Lon, 64)
		if latErr != nil || lonErr != nil {
			continue // malformed row: drop it rather than fail the whole page
		}
		name := row.Name
		if name == "" {
			name = firstSegment(row.DisplayName)
		}
		places = append(places, domain.Place{Name: name, Address: row.DisplayName, Lat: lat, Lon: lon})
		if len(places) == limit {
			break
		}
	}
	return places, nil
}

// firstSegment returns the leading comma-separated segment of a Nominatim
// display_name — the closest thing to a short place name when `name` is empty.
func firstSegment(displayName string) string {
	if i := strings.Index(displayName, ","); i > 0 {
		return strings.TrimSpace(displayName[:i])
	}
	return displayName
}

// osrmRouteResponse is the subset of an OSRM /route/v1 response we consume.
type osrmRouteResponse struct {
	Code   string `json:"code"`
	Routes []struct {
		Duration float64 `json:"duration"` // seconds
	} `json:"routes"`
}

// TravelTime implements port.MapsProvider via GET
// {base}/route/v1/{profile}/{lon},{lat};{lon},{lat} (OSRM wants lon,lat
// order). TravelTransit is approximated as driving * transitFactor.
func (c *Client) TravelTime(ctx context.Context, fromLat, fromLon, toLat, toLon float64, mode domain.TravelMode) (time.Duration, error) {
	if c.osrmBase == "" {
		return 0, fmt.Errorf("osrm: no base URL configured")
	}
	profile := "driving"
	factor := 1.0
	switch mode {
	case domain.TravelWalking:
		profile = "walking"
	case domain.TravelTransit:
		factor = transitFactor
	case domain.TravelDriving, "":
		// driving is the default profile
	default:
		return 0, fmt.Errorf("%w: unknown travel mode %q", domain.ErrValidation, mode)
	}
	coord := func(lat, lon float64) string {
		return strconv.FormatFloat(lon, 'f', -1, 64) + "," + strconv.FormatFloat(lat, 'f', -1, 64)
	}
	reqURL := fmt.Sprintf("%s/route/v1/%s/%s;%s?overview=false",
		c.osrmBase, profile, coord(fromLat, fromLon), coord(toLat, toLon))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return 0, fmt.Errorf("osrm: build request: %w", err)
	}
	req.Header.Set("User-Agent", UserAgent)
	res, err := c.hc.Do(req)
	if err != nil {
		return 0, fmt.Errorf("osrm: route: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("osrm: route returned status %d", res.StatusCode)
	}
	var body osrmRouteResponse
	if err := json.NewDecoder(io.LimitReader(res.Body, maxResponseBytes)).Decode(&body); err != nil {
		return 0, fmt.Errorf("osrm: decode route response: %w", err)
	}
	if body.Code != "Ok" || len(body.Routes) == 0 {
		return 0, fmt.Errorf("osrm: no route found (code %q)", body.Code)
	}
	return time.Duration(body.Routes[0].Duration * factor * float64(time.Second)), nil
}
