package msgraph

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"calendium/backend/internal/port"
)

const (
	authEndpoint  = "https://login.microsoftonline.com/common/oauth2/v2.0/authorize"
	tokenEndpoint = "https://login.microsoftonline.com/common/oauth2/v2.0/token"
)

// oauthScopes grants offline mail + calendar access plus the account email.
var oauthScopes = []string{
	"offline_access",
	"openid",
	"email",
	"https://graph.microsoft.com/Mail.ReadWrite",
	"https://graph.microsoft.com/Mail.Send",
	"https://graph.microsoft.com/Calendars.ReadWrite",
}

// AuthURL builds the Microsoft identity platform consent URL. codeChallenge
// carries the PKCE S256 challenge.
func (c *Client) AuthURL(state, redirectURI, codeChallenge string) string {
	q := url.Values{
		"client_id":             {c.clientID},
		"redirect_uri":          {redirectURI},
		"response_type":         {"code"},
		"response_mode":         {"query"},
		"scope":                 {strings.Join(oauthScopes, " ")},
		"state":                 {state},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
	}
	return authEndpoint + "?" + q.Encode()
}

// Exchange swaps an authorization code for tokens, replaying the PKCE
// verifier.
func (c *Client) Exchange(ctx context.Context, code, redirectURI, codeVerifier string) (port.OAuthToken, error) {
	return c.token(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
		"scope":         {strings.Join(oauthScopes, " ")},
		"code_verifier": {codeVerifier},
	}, "")
}

// Refresh mints a new access token. Microsoft rotates refresh tokens; the
// new one is returned when present, otherwise the input is carried through.
func (c *Client) Refresh(ctx context.Context, refreshToken string) (port.OAuthToken, error) {
	return c.token(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
		"scope":         {strings.Join(oauthScopes, " ")},
	}, refreshToken)
}

func (c *Client) token(ctx context.Context, form url.Values, fallbackRefresh string) (port.OAuthToken, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return port.OAuthToken{}, fmt.Errorf("msgraph: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	res, err := c.hc.Do(req)
	if err != nil {
		return port.OAuthToken{}, fmt.Errorf("msgraph: token request: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusOK {
		var oe struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		_ = json.NewDecoder(res.Body).Decode(&oe)
		return port.OAuthToken{}, &httpError{StatusCode: res.StatusCode, Code: oe.Error,
			Message: oe.ErrorDescription}
	}
	var tr struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
		IDToken      string `json:"id_token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&tr); err != nil {
		return port.OAuthToken{}, fmt.Errorf("msgraph: decode token response: %w", err)
	}

	tok := port.OAuthToken{
		TokenSet: port.TokenSet{
			AccessToken:  tr.AccessToken,
			RefreshToken: tr.RefreshToken,
			ExpiresAt:    time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second),
		},
		Scopes: strings.Fields(tr.Scope),
		Email:  emailFromIDToken(tr.IDToken),
	}
	if tok.RefreshToken == "" {
		tok.RefreshToken = fallbackRefresh
	}
	return tok, nil
}

// emailFromIDToken pulls the email (or preferred_username fallback) claim
// out of an id_token without verifying it — it arrived over TLS directly
// from Microsoft.
func emailFromIDToken(idToken string) string {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Email             string `json:"email"`
		PreferredUsername string `json:"preferred_username"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	if claims.Email != "" {
		return claims.Email
	}
	if strings.Contains(claims.PreferredUsername, "@") {
		return claims.PreferredUsername
	}
	return ""
}
