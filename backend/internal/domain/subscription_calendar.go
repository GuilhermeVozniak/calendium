package domain

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// DefaultSubscriptionColor is the color assigned to a calendar subscription
// when the user does not pick one (violet, matching the migration default).
const DefaultSubscriptionColor = "#8b5cf6"

// CalendarSubscription is a user-added "interesting calendar" ICS feed
// (holidays, team schedules, ...). Its events are read-only mirrors owned
// by the feed: they are replaced wholesale on refresh, never written back
// to any provider, and never counted as busy time for Availability.
type CalendarSubscription struct {
	ID     string `json:"id"`
	UserID string `json:"-"`
	URL    string `json:"url"`  // https only
	Name   string `json:"name"` // user label, else feed X-WR-CALNAME
	Color  string `json:"color"`
	IsVisible bool `json:"isVisible"`
	// Etag is the HTTP cache validator from the last successful fetch;
	// internal fetch state, never part of the API payload.
	Etag          string     `json:"-"`
	LastFetchedAt *time.Time `json:"lastFetchedAt"`
	LastError     *string    `json:"lastError"`
	CreatedAt     time.Time  `json:"createdAt"`
}

// NewCalendarSubscription validates and normalizes a subscription request.
// Subscription URLs are untrusted user input: only absolute https URLs with
// a host are accepted (SSRF hygiene — plain http, webcal, file, data, and
// scheme-relative forms are all rejected). Name is optional here; it is
// resolved against the feed's X-WR-CALNAME after the first fetch (see
// ResolveName). An empty color falls back to DefaultSubscriptionColor.
func NewCalendarSubscription(userID, rawURL, name, color string) (CalendarSubscription, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return CalendarSubscription{}, fmt.Errorf("%w: url is required", ErrValidation)
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return CalendarSubscription{}, fmt.Errorf("%w: url must be an absolute https URL", ErrValidation)
	}
	if color == "" {
		color = DefaultSubscriptionColor
	}
	return CalendarSubscription{
		UserID:    userID,
		URL:       u.String(),
		Name:      strings.TrimSpace(name),
		Color:     color,
		IsVisible: true,
	}, nil
}

// ResolveName fills an empty Name from the feed's calendar name
// (X-WR-CALNAME), falling back to the feed URL's host so a subscription is
// never displayed unlabeled. A user-provided name always wins.
func (s *CalendarSubscription) ResolveName(feedName string) {
	if s.Name != "" {
		return
	}
	if feedName = strings.TrimSpace(feedName); feedName != "" {
		s.Name = feedName
		return
	}
	if u, err := url.Parse(s.URL); err == nil && u.Host != "" {
		s.Name = u.Host
		return
	}
	s.Name = "Subscribed calendar"
}
