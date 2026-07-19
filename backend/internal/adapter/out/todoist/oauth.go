// Package todoist holds the Todoist outbound adapters (M2.8): the per-user
// OAuth gateway lives here (Task 9); the Sync-API TodoProvider client lands
// in Task 10.
package todoist

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"calendium/backend/internal/port"
)

const (
	defaultAuthEndpoint  = "https://todoist.com/oauth/authorize"
	defaultTokenEndpoint = "https://todoist.com/oauth/access_token"

	// oauthScope grants task read/write — what the M2.8 todo mirror needs.
	oauthScope = "data:read_write"
)

// OAuth implements port.OAuthGateway for Todoist. Todoist uses a plain
// confidential-client authorization-code flow: no PKCE (the challenge/
// verifier arguments are ignored), and access tokens neither expire nor
// refresh.
type OAuth struct {
	clientID     string
	clientSecret string
	hc           *http.Client

	// AuthEndpoint/TokenEndpoint are overridable in tests (httptest).
	AuthEndpoint  string
	TokenEndpoint string
}

var _ port.OAuthGateway = (*OAuth)(nil)

func NewOAuth(clientID, clientSecret string, hc *http.Client) *OAuth {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &OAuth{
		clientID:      clientID,
		clientSecret:  clientSecret,
		hc:            hc,
		AuthEndpoint:  defaultAuthEndpoint,
		TokenEndpoint: defaultTokenEndpoint,
	}
}

// AuthURL builds the Todoist consent URL. codeChallenge is ignored (no PKCE).
func (o *OAuth) AuthURL(state, redirectURI, _ string) string {
	q := url.Values{
		"client_id":    {o.clientID},
		"scope":        {oauthScope},
		"state":        {state},
		"redirect_uri": {redirectURI},
	}
	return o.AuthEndpoint + "?" + q.Encode()
}

// Exchange swaps the authorization code for an access token. Todoist tokens
// do not expire and there is no refresh token, so ExpiresAt stays zero.
func (o *OAuth) Exchange(ctx context.Context, code, redirectURI, _ string) (port.OAuthToken, error) {
	form := url.Values{
		"client_id":     {o.clientID},
		"client_secret": {o.clientSecret},
		"code":          {code},
		"redirect_uri":  {redirectURI},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return port.OAuthToken{}, fmt.Errorf("todoist: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	res, err := o.hc.Do(req)
	if err != nil {
		return port.OAuthToken{}, fmt.Errorf("todoist: token request: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	var tr struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if res.StatusCode != http.StatusOK {
		_ = json.NewDecoder(res.Body).Decode(&tr)
		return port.OAuthToken{}, fmt.Errorf("todoist: token endpoint returned %d: %s", res.StatusCode, tr.Error)
	}
	if err := json.NewDecoder(res.Body).Decode(&tr); err != nil {
		return port.OAuthToken{}, fmt.Errorf("todoist: decode token response: %w", err)
	}
	if tr.AccessToken == "" {
		return port.OAuthToken{}, fmt.Errorf("todoist: token response missing access_token")
	}
	return port.OAuthToken{
		TokenSet: port.TokenSet{AccessToken: tr.AccessToken},
		Scopes:   []string{oauthScope},
	}, nil
}

// Refresh is unsupported: Todoist access tokens do not expire. Consumers see
// a zero ExpiresAt on the stored TokenSet and must use the access token
// directly instead of refreshing.
func (o *OAuth) Refresh(context.Context, string) (port.OAuthToken, error) {
	return port.OAuthToken{}, fmt.Errorf("todoist: token refresh is not supported (tokens do not expire)")
}
