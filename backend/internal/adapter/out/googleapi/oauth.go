package googleapi

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
	authEndpoint  = "https://accounts.google.com/o/oauth2/v2/auth"
	tokenEndpoint = "https://oauth2.googleapis.com/token"
)

// oauthScopes grants offline mail + calendar access plus the account email.
var oauthScopes = []string{
	"https://www.googleapis.com/auth/gmail.modify",
	"https://www.googleapis.com/auth/calendar",
	"openid",
	"email",
}

// AuthURL builds the Google consent URL. access_type=offline plus
// prompt=consent guarantees a refresh token on every grant; codeChallenge
// carries the PKCE S256 challenge.
func (c *Client) AuthURL(state, redirectURI, codeChallenge string) string {
	q := url.Values{
		"client_id":             {c.clientID},
		"redirect_uri":          {redirectURI},
		"response_type":         {"code"},
		"scope":                 {strings.Join(oauthScopes, " ")},
		"access_type":           {"offline"},
		"prompt":                {"consent"},
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
		"code_verifier": {codeVerifier},
	}, "")
}

// Refresh mints a new access token. Google does not rotate refresh tokens,
// so the input token is carried through in the result.
func (c *Client) Refresh(ctx context.Context, refreshToken string) (port.OAuthToken, error) {
	return c.token(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
	}, refreshToken)
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	IDToken      string `json:"id_token"`
}

func (c *Client) token(ctx context.Context, form url.Values, fallbackRefresh string) (port.OAuthToken, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return port.OAuthToken{}, fmt.Errorf("googleapi: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	res, err := c.hc.Do(req)
	if err != nil {
		return port.OAuthToken{}, fmt.Errorf("googleapi: token request: %w", err)
	}
	defer res.Body.Close()

	var tr tokenResponse
	if res.StatusCode != http.StatusOK {
		var oe struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		_ = json.NewDecoder(res.Body).Decode(&oe)
		return port.OAuthToken{}, &httpError{StatusCode: res.StatusCode,
			Message: strings.TrimSpace(oe.Error + " " + oe.ErrorDescription)}
	}
	if err := json.NewDecoder(res.Body).Decode(&tr); err != nil {
		return port.OAuthToken{}, fmt.Errorf("googleapi: decode token response: %w", err)
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

// emailFromIDToken pulls the email claim out of an OpenID Connect id_token
// without verifying it — it arrived over TLS directly from Google.
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
		Email string `json:"email"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	return claims.Email
}
