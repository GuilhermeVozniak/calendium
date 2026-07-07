package push

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"calendium/backend/internal/config"
)

const fcmScope = "https://www.googleapis.com/auth/firebase.messaging"

// fcmSender POSTs to the FCM v1 API authenticated with an OAuth2 access
// token minted from an RS256 service-account JWT assertion.
type fcmSender struct {
	hc *http.Client

	credsOnce sync.Once
	creds     serviceAccount
	credsErr  error

	mu          sync.Mutex
	accessToken string
	expiresAt   time.Time

	rawCreds string
}

type serviceAccount struct {
	ProjectID   string `json:"project_id"`
	PrivateKey  string `json:"private_key"`
	ClientEmail string `json:"client_email"`
	TokenURI    string `json:"token_uri"`
}

func newFCMSender(cfg config.FCM, hc *http.Client) *fcmSender {
	return &fcmSender{hc: hc, rawCreds: cfg.ServiceAccountJSON}
}

func (s *fcmSender) send(ctx context.Context, registrationToken, title, body string, data map[string]string) error {
	creds, err := s.serviceAccount()
	if err != nil {
		return err
	}
	accessToken, err := s.token(ctx)
	if err != nil {
		return err
	}

	message := map[string]any{
		"message": map[string]any{
			"token":        registrationToken,
			"notification": map[string]string{"title": title, "body": body},
			"data":         data,
		},
	}
	buf, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("push: encode fcm message: %w", err)
	}

	endpoint := "https://fcm.googleapis.com/v1/projects/" + url.PathEscape(creds.ProjectID) + "/messages:send"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("push: build fcm request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	res, err := s.hc.Do(req)
	if err != nil {
		return fmt.Errorf("push: fcm request: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode >= 200 && res.StatusCode <= 299 {
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	var fe struct {
		Error struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &fe)
	return fmt.Errorf("push: fcm http %d (%s): %s", res.StatusCode, fe.Error.Status, fe.Error.Message)
}

func (s *fcmSender) serviceAccount() (serviceAccount, error) {
	s.credsOnce.Do(func() {
		s.credsErr = json.Unmarshal([]byte(s.rawCreds), &s.creds)
		if s.credsErr == nil && (s.creds.ClientEmail == "" || s.creds.PrivateKey == "" || s.creds.ProjectID == "") {
			s.credsErr = fmt.Errorf("client_email, private_key, and project_id are required")
		}
		if s.creds.TokenURI == "" {
			s.creds.TokenURI = "https://oauth2.googleapis.com/token"
		}
	})
	if s.credsErr != nil {
		return serviceAccount{}, fmt.Errorf("push: parse FCM_SERVICE_ACCOUNT_JSON: %w", s.credsErr)
	}
	return s.creds, nil
}

// token returns a cached OAuth2 access token, exchanging a fresh RS256
// service-account assertion when the cached one nears expiry.
func (s *fcmSender) token(ctx context.Context) (string, error) {
	creds, err := s.serviceAccount()
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if s.accessToken != "" && now.Before(s.expiresAt.Add(-2*time.Minute)) {
		return s.accessToken, nil
	}

	key, err := parseRSAPrivateKeyPEM(creds.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("push: parse fcm service-account key: %w", err)
	}
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	claims := map[string]any{
		"iss":   creds.ClientEmail,
		"scope": fcmScope,
		"aud":   creds.TokenURI,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}
	assertion, err := signRS256(header, claims, key)
	if err != nil {
		return "", err
	}

	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, creds.TokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("push: build fcm token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	res, err := s.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("push: fcm token request: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("push: read fcm token response: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("push: fcm token exchange http %d: %s", res.StatusCode, truncate(string(raw), 200))
	}
	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &tr); err != nil {
		return "", fmt.Errorf("push: decode fcm token response: %w", err)
	}
	s.accessToken = tr.AccessToken
	s.expiresAt = now.Add(time.Duration(tr.ExpiresIn) * time.Second)
	return s.accessToken, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
