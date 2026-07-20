// Package hubspot implements port.CrmProvider against the HubSpot CRM v3
// REST API using only the standard library: contact context (contact search
// by email plus associated deals) and explicit email-engagement logging.
package hubspot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

const (
	defaultAPIBase = "https://api.hubapi.com"
	defaultAppBase = "https://app.hubspot.com"
)

// Client is a stateless HubSpot v3 API client; per-user access tokens are
// passed on every call (token storage belongs to the integration-connection
// repo, never to this adapter).
type Client struct {
	hc      *http.Client
	apiBase string // API origin; overridden in tests (httptest)
	appBase string // deep-link origin for vendor URLs
}

var _ port.CrmProvider = (*Client)(nil)

// NewClient builds the adapter. When hc is nil a stdlib client with an
// explicit timeout is used so a wedged vendor can never hang a request.
func NewClient(hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{hc: hc, apiBase: defaultAPIBase, appBase: defaultAppBase}
}

func (c *Client) Vendor() domain.IntegrationVendor { return domain.IntegrationHubSpot }

// do performs one authorized JSON round-trip. A vendor 401 is wrapped in
// domain.ErrUnauthorized so the service layer can refresh the token and
// retry exactly once.
func (c *Client) do(ctx context.Context, accessToken, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("hubspot: encode %s %s: %w", method, path, err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.apiBase+path, rdr)
	if err != nil {
		return fmt.Errorf("hubspot: build %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("hubspot: %s %s: %w", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("hubspot: %s %s: %w", method, path, domain.ErrUnauthorized)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return fmt.Errorf("hubspot: %s %s returned status %d: %s", method, path, res.StatusCode, snippet)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("hubspot: decode %s %s response: %w", method, path, err)
	}
	return nil
}

type searchResult struct {
	Total   int `json:"total"`
	Results []struct {
		ID         string            `json:"id"`
		Properties map[string]string `json:"properties"`
	} `json:"results"`
}

// searchContact resolves a contact id (and properties) by exact email match.
func (c *Client) searchContact(ctx context.Context, accessToken, email string) (id string, props map[string]string, found bool, err error) {
	body := map[string]any{
		"filterGroups": []any{map[string]any{
			"filters": []any{map[string]string{
				"propertyName": "email",
				"operator":     "EQ",
				"value":        email,
			}},
		}},
		"properties": []string{"firstname", "lastname", "email", "company", "jobtitle", "phone", "hubspot_owner_id"},
		"limit":      1,
	}
	var res searchResult
	if err := c.do(ctx, accessToken, http.MethodPost, "/crm/v3/objects/contacts/search", body, &res); err != nil {
		return "", nil, false, err
	}
	if res.Total == 0 || len(res.Results) == 0 {
		return "", nil, false, nil
	}
	return res.Results[0].ID, res.Results[0].Properties, true, nil
}

// portalID fetches the account's portal id, needed to build deep links
// (https://app.hubspot.com/contacts/{portal}/record/...).
func (c *Client) portalID(ctx context.Context, accessToken string) (int64, error) {
	var res struct {
		PortalID int64 `json:"portalId"`
	}
	if err := c.do(ctx, accessToken, http.MethodGet, "/account-info/v3/details", nil, &res); err != nil {
		return 0, err
	}
	return res.PortalID, nil
}

// parseHubSpotTime accepts either RFC 3339 or a millisecond epoch string —
// HubSpot emits both for datetime properties depending on the pipeline.
func parseHubSpotTime(s string) *time.Time {
	if s == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		t = t.UTC()
		return &t
	}
	if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
		t := time.UnixMilli(ms).UTC()
		return &t
	}
	return nil
}

// ContactContext implements port.CrmProvider: contact search by email, then
// associated deals (association list + batch read). A missing contact is
// CrmContext{Contact: nil}, not an error.
func (c *Client) ContactContext(ctx context.Context, accessToken, email string) (domain.CrmContext, error) {
	out := domain.CrmContext{Vendor: domain.IntegrationHubSpot, Deals: []domain.CrmDeal{}}

	id, props, found, err := c.searchContact(ctx, accessToken, email)
	if err != nil {
		return domain.CrmContext{}, err
	}
	if !found {
		return out, nil
	}

	portal, err := c.portalID(ctx, accessToken)
	if err != nil {
		return domain.CrmContext{}, err
	}

	contactEmail := props["email"]
	if contactEmail == "" {
		contactEmail = email
	}
	out.Contact = &domain.CrmContact{
		ID:        id,
		Email:     contactEmail,
		Name:      strings.TrimSpace(props["firstname"] + " " + props["lastname"]),
		Company:   props["company"],
		Title:     props["jobtitle"],
		Phone:     props["phone"],
		Owner:     props["hubspot_owner_id"],
		VendorURL: fmt.Sprintf("%s/contacts/%d/record/0-1/%s", c.appBase, portal, id),
	}

	deals, err := c.dealsForContact(ctx, accessToken, id, portal)
	if err != nil {
		return domain.CrmContext{}, err
	}
	out.Deals = deals
	return out, nil
}

func (c *Client) dealsForContact(ctx context.Context, accessToken, contactID string, portal int64) ([]domain.CrmDeal, error) {
	var assoc struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	path := fmt.Sprintf("/crm/v3/objects/contacts/%s/associations/deals", contactID)
	if err := c.do(ctx, accessToken, http.MethodGet, path, nil, &assoc); err != nil {
		return nil, err
	}
	if len(assoc.Results) == 0 {
		return []domain.CrmDeal{}, nil
	}

	inputs := make([]map[string]string, 0, len(assoc.Results))
	for _, a := range assoc.Results {
		inputs = append(inputs, map[string]string{"id": a.ID})
	}
	body := map[string]any{
		"properties": []string{"dealname", "dealstage", "amount", "closedate"},
		"inputs":     inputs,
	}
	var batch struct {
		Results []struct {
			ID         string            `json:"id"`
			Properties map[string]string `json:"properties"`
		} `json:"results"`
	}
	if err := c.do(ctx, accessToken, http.MethodPost, "/crm/v3/objects/deals/batch/read", body, &batch); err != nil {
		return nil, err
	}

	deals := make([]domain.CrmDeal, 0, len(batch.Results))
	for _, d := range batch.Results {
		deal := domain.CrmDeal{
			ID:        d.ID,
			Name:      d.Properties["dealname"],
			Stage:     d.Properties["dealstage"],
			CloseDate: parseHubSpotTime(d.Properties["closedate"]),
			VendorURL: fmt.Sprintf("%s/contacts/%d/record/0-3/%s", c.appBase, portal, d.ID),
		}
		if raw := d.Properties["amount"]; raw != "" {
			if amount, err := strconv.ParseFloat(raw, 64); err == nil {
				deal.Amount = &amount
			}
		}
		deals = append(deals, deal)
	}
	return deals, nil
}

// LogEmail implements port.CrmProvider: creates an email engagement object
// and associates it with the contact resolved by email. Logging an email is
// always an explicit user action — this adapter is never called in bulk.
func (c *Client) LogEmail(ctx context.Context, accessToken string, log domain.CrmEmailLog) error {
	contactID, _, found, err := c.searchContact(ctx, accessToken, log.ContactEmail)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: contact %q is not in HubSpot", domain.ErrNotFound, log.ContactEmail)
	}

	direction := "EMAIL" // HubSpot's value for a logged outbound email
	if log.Direction == "inbound" {
		direction = "INCOMING_EMAIL"
	}
	body := map[string]any{
		"properties": map[string]string{
			"hs_timestamp":       log.SentAt.UTC().Format(time.RFC3339),
			"hs_email_subject":   log.Subject,
			"hs_email_text":      log.BodyText,
			"hs_email_direction": direction,
		},
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, accessToken, http.MethodPost, "/crm/v3/objects/emails", body, &created); err != nil {
		return err
	}

	assocPath := fmt.Sprintf("/crm/v3/objects/emails/%s/associations/contacts/%s/email_to_contact", created.ID, contactID)
	return c.do(ctx, accessToken, http.MethodPut, assocPath, nil, nil)
}
