package todoist

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

const (
	// defaultSyncBaseURL is the Todoist Sync API v9 root.
	defaultSyncBaseURL = "https://api.todoist.com/sync/v9"
	// maxErrorBody bounds how much vendor error body is echoed into errors.
	maxErrorBody = 512
	// maxResponseBody bounds how much of a 200 response is read.
	maxResponseBody = 8 << 20
)

// Client implements port.TodoProvider over the Todoist Sync API v9. It is
// stateless: the per-user access token is passed on every call (matching the
// googleapi/msgraph adapters), so one Client serves every connected user.
type Client struct {
	hc *http.Client
	// BaseURL is the Sync API root, overridable in tests (httptest).
	BaseURL string
}

var _ port.TodoProvider = (*Client)(nil)

func NewClient(hc *http.Client) *Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{hc: hc, BaseURL: defaultSyncBaseURL}
}

func (c *Client) Source() domain.TaskSource { return domain.TaskSourceTodoist }

// syncItem is the wire shape of a Sync v9 item (only the fields we consume).
type syncItem struct {
	ID          string `json:"id"`
	Content     string `json:"content"`
	Description string `json:"description"`
	Checked     bool   `json:"checked"`
	IsDeleted   bool   `json:"is_deleted"`
	Due         *struct {
		Date string `json:"date"`
	} `json:"due"`
}

// SyncTasks performs one incremental item sync. cursor is the Sync v9
// sync_token; "" requests a full sync ("*"). Todoist returns the complete
// delta in a single response, so HasMore is always false. loc resolves
// floating (zone-less) due datetimes (nil = UTC).
func (c *Client) SyncTasks(ctx context.Context, accessToken, cursor string, loc *time.Location) (port.TodoSyncPage, error) {
	syncToken := cursor
	if syncToken == "" {
		syncToken = "*"
	}
	body, err := c.post(ctx, accessToken, url.Values{
		"sync_token":     {syncToken},
		"resource_types": {`["items"]`},
	})
	if err != nil {
		return port.TodoSyncPage{}, err
	}
	var resp struct {
		SyncToken string     `json:"sync_token"`
		Items     []syncItem `json:"items"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return port.TodoSyncPage{}, fmt.Errorf("todoist sync: decode response: %w", err)
	}
	page := port.TodoSyncPage{NextCursor: resp.SyncToken}
	for _, item := range resp.Items {
		if item.Checked || item.IsDeleted {
			// Completed or deleted upstream: the mirror row disappears from
			// the rail (our own write-through completions come back through
			// this same path).
			page.DeletedIDs = append(page.DeletedIDs, item.ID)
			continue
		}
		page.Tasks = append(page.Tasks, itemToTask(item, loc))
	}
	return page, nil
}

// itemToTask maps a live Sync item to a mirror task. ID/UserID are left for
// the sync service to fill — the adapter never sees Calendium users.
func itemToTask(item syncItem, loc *time.Location) domain.Task {
	t := domain.Task{
		Title:      item.Content,
		Source:     domain.TaskSourceTodoist,
		ExternalID: item.ID,
	}
	if item.Description != "" {
		notes := item.Description
		t.Notes = &notes
	}
	itemURL := "https://todoist.com/showTask?id=" + url.QueryEscape(item.ID)
	t.SourceURL = &itemURL
	if item.Due != nil {
		if due, allDay, ok := parseDue(item.Due.Date, loc); ok {
			t.Due = &due
			t.AllDayDue = allDay
		}
	}
	return t
}

// parseDue parses a Sync v9 due.date. A date-only value ("2026-07-22") is an
// all-day due; datetimes arrive with ("2026-07-21T17:00:00Z") or without a
// zone suffix (floating local time — interpreted in loc, the connection
// owner's CalendarPrefs timezone, since Todoist means "17:00 on the user's
// wall clock"; nil loc falls back to UTC).
func parseDue(raw string, loc *time.Location) (t time.Time, allDay, ok bool) {
	if loc == nil {
		loc = time.UTC
	}
	if ts, err := time.Parse(time.RFC3339, raw); err == nil {
		return ts.UTC(), false, true
	}
	if ts, err := time.ParseInLocation("2006-01-02T15:04:05", raw, loc); err == nil {
		return ts.UTC(), false, true
	}
	if ts, err := time.Parse("2006-01-02", raw); err == nil {
		return ts.UTC(), true, true
	}
	return time.Time{}, false, false
}

func (c *Client) CompleteTask(ctx context.Context, accessToken, externalID string) error {
	return c.execCommand(ctx, accessToken, "item_complete", externalID)
}

func (c *Client) ReopenTask(ctx context.Context, accessToken, externalID string) error {
	return c.execCommand(ctx, accessToken, "item_uncomplete", externalID)
}

// execCommand posts one Sync v9 command and verifies its per-command status —
// HTTP 200 alone does not mean the item mutation was accepted.
func (c *Client) execCommand(ctx context.Context, accessToken, commandType, externalID string) error {
	id := commandUUID()
	commands, err := json.Marshal([]map[string]any{{
		"type": commandType,
		"uuid": id,
		"args": map[string]string{"id": externalID},
	}})
	if err != nil {
		return err
	}
	body, err := c.post(ctx, accessToken, url.Values{"commands": {string(commands)}})
	if err != nil {
		return err
	}
	var resp struct {
		SyncStatus map[string]json.RawMessage `json:"sync_status"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("todoist %s: decode response: %w", commandType, err)
	}
	status, ok := resp.SyncStatus[id]
	if !ok {
		return fmt.Errorf("todoist %s: response carries no sync_status for the command", commandType)
	}
	if string(status) != `"ok"` {
		return fmt.Errorf("todoist %s failed: %s", commandType, truncate(status))
	}
	return nil
}

// post sends one authenticated form POST to the /sync endpoint.
func (c *Client) post(ctx context.Context, accessToken string, form url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/sync", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBody))
	if err != nil {
		return nil, err
	}
	switch res.StatusCode {
	case http.StatusOK:
		return body, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, fmt.Errorf("%w: todoist rejected the access token (HTTP %d)", domain.ErrUnauthorized, res.StatusCode)
	case http.StatusTooManyRequests:
		return nil, fmt.Errorf("todoist rate limited (HTTP 429): %s", truncate(body))
	default:
		return nil, fmt.Errorf("todoist sync: HTTP %d: %s", res.StatusCode, truncate(body))
	}
}

func truncate(b []byte) string {
	if len(b) > maxErrorBody {
		return string(b[:maxErrorBody]) + "…"
	}
	return string(b)
}

// commandUUID mints the per-command idempotency uuid the Sync API requires.
func commandUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is unrecoverable process state; fall back to a
		// time-based id rather than panicking a worker loop.
		return fmt.Sprintf("cmd-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
