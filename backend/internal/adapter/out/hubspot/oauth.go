// Package hubspot holds the HubSpot outbound adapters (M2.8): the per-user
// OAuth gateway lives here (Task 9); the CRM client (contact context + email
// logging) lands in Task 16.
package hubspot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"calendium/backend/internal/port"
)

const (
	defaultAuthEndpoint      = "https://app.hubspot.com/oauth/authorize"
	defaultTokenEndpoint     = "https://api.hubapi.com/oauth/v1/token"
	defaultTokenInfoEndpoint = "https://api.hubapi.com/oauth/v1/access-tokens"
)

// oauthScopes covers the M2.8 CRM surface: contact context, associated
// deals, and email logging (Task 16 logs sent mail to the CRM, which
// requires crm.objects.emails.write). No write scope on contacts — the
// integration never mutates them.
var oauthScopes = []string{
	"crm.objects.contacts.read",
	"crm.objects.deals.read",
	"crm.objects.emails.write",
}

// OAuth implements port.OAuthGateway for HubSpot: a confidential-client
// refresh-token flow with no PKCE (the challenge/verifier arguments are
// ignored).
type OAuth struct {
	clientID     string
	clientSecret string
	hc           *http.Client

	// Endpoints are overridable in tests (httptest).
	AuthEndpoint      string
	TokenEndpoint     string
	TokenInfoEndpoint string
}

var _ port.OAuthGateway = (*OAuth)(nil)

func NewOAuth(clientID, clientSecret string, hc *http.Client) *OAuth {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &OAuth{
		clientID:          clientID,
		clientSecret:      clientSecret,
		hc:                hc,
		AuthEndpoint:      defaultAuthEndpoint,
		TokenEndpoint:     defaultTokenEndpoint,
		TokenInfoEndpoint: defaultTokenInfoEndpoint,
	}
}

// AuthURL builds the HubSpot consent URL. codeChallenge is ignored (no PKCE).
func (o *OAuth) AuthURL(state, redirectURI, _ string) string {
	q := url.Values{
		"client_id":    {o.clientID},
		"redirect_uri": {redirectURI},
		"scope":        {strings.Join(oauthScopes, " ")},
		"state":        {state},
	}
	return o.AuthEndpoint + "?" + q.Encode()
}

// Exchange swaps the authorization code for tokens, then best-effort resolves
// the grant's user email / portal domain (token-info endpoint) as the
// connection label.
func (o *OAuth) Exchange(ctx context.Context, code, redirectURI, _ string) (port.OAuthToken, error) {
	tok, err := o.token(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {o.clientID},
		"client_secret": {o.clientSecret},
		"redirect_uri":  {redirectURI},
		"code":          {code},
	}, "")
	if err != nil {
		return port.OAuthToken{}, err
	}
	tok.Email = o.accountLabel(ctx, tok.AccessToken)
	return tok, nil
}

// Refresh mints a fresh access token from the stored refresh token. HubSpot
// may rotate the refresh token; when it does not, the input token carries
// through.
func (o *OAuth) Refresh(ctx context.Context, refreshToken string) (port.OAuthToken, error) {
	return o.token(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {o.clientID},
		"client_secret": {o.clientSecret},
		"refresh_token": {refreshToken},
	}, refreshToken)
}

func (o *OAuth) token(ctx context.Context, form url.Values, fallbackRefresh string) (port.OAuthToken, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return port.OAuthToken{}, fmt.Errorf("hubspot: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	res, err := o.hc.Do(req)
	if err != nil {
		return port.OAuthToken{}, fmt.Errorf("hubspot: token request: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusOK {
		var oe struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		}
		_ = json.NewDecoder(res.Body).Decode(&oe)
		return port.OAuthToken{}, fmt.Errorf("hubspot: token endpoint returned %d: %s", res.StatusCode, strings.TrimSpace(oe.Status+" "+oe.Message))
	}
	var tr struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.NewDecoder(res.Body).Decode(&tr); err != nil {
		return port.OAuthToken{}, fmt.Errorf("hubspot: decode token response: %w", err)
	}
	if tr.AccessToken == "" {
		return port.OAuthToken{}, fmt.Errorf("hubspot: token response missing access_token")
	}
	tok := port.OAuthToken{
		TokenSet: port.TokenSet{
			AccessToken:  tr.AccessToken,
			RefreshToken: tr.RefreshToken,
			ExpiresAt:    time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second),
		},
		Scopes: append([]string(nil), oauthScopes...),
	}
	if tok.RefreshToken == "" {
		tok.RefreshToken = fallbackRefresh
	}
	return tok, nil
}

// accountLabel resolves the grant's user email (falling back to the portal
// domain) via GET /oauth/v1/access-tokens/{token}. Best-effort: any failure
// yields an empty label, never an error — the connection still works.
func (o *OAuth) accountLabel(ctx context.Context, accessToken string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		o.TokenInfoEndpoint+"/"+url.PathEscape(accessToken), nil)
	if err != nil {
		return ""
	}
	res, err := o.hc.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return ""
	}
	var info struct {
		User      string `json:"user"`
		HubDomain string `json:"hub_domain"`
	}
	if err := json.NewDecoder(res.Body).Decode(&info); err != nil {
		return ""
	}
	if info.User != "" {
		return info.User
	}
	return info.HubDomain
}
