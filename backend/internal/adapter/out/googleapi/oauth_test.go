package googleapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fakeIDToken builds a JWT-shaped (but unsigned/unverified) id_token whose
// payload segment base64url-decodes to claimsJSON — matching the shape
// emailFromIDToken expects (header.payload.signature).
func fakeIDToken(claimsJSON string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(claimsJSON))
	return header + "." + payload + ".sig"
}

func TestClient_AuthURL(t *testing.T) {
	c := NewClient("cid-123", "secret-xyz", nil)

	got := c.AuthURL("state-abc", "https://app.example.com/callback", "chal-456")

	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse AuthURL result: %v", err)
	}
	if want := "https://accounts.google.com/o/oauth2/v2/auth"; got[:len(want)] != want {
		t.Errorf("AuthURL prefix = %q, want %q", got[:len(want)], want)
	}
	q := u.Query()
	cases := map[string]string{
		"client_id":             "cid-123",
		"redirect_uri":          "https://app.example.com/callback",
		"response_type":         "code",
		"scope":                 "https://www.googleapis.com/auth/gmail.modify https://www.googleapis.com/auth/calendar openid email",
		"access_type":           "offline",
		"prompt":                "consent",
		"state":                 "state-abc",
		"code_challenge":        "chal-456",
		"code_challenge_method": "S256",
	}
	for k, want := range cases {
		if got := q.Get(k); got != want {
			t.Errorf("query[%q] = %q, want %q", k, got, want)
		}
	}
}

func TestClient_Exchange_Success(t *testing.T) {
	idToken := fakeIDToken(`{"email":"person@example.com"}`)
	var gotPath, gotMethod, gotContentType string
	var gotForm url.Values

	srv, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		gotForm, _ = url.ParseQuery(string(raw))
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{
			"access_token":"at-1",
			"refresh_token":"rt-1",
			"expires_in":3600,
			"scope":"openid email",
			"id_token":"`+idToken+`"
		}`)
	})
	_ = srv

	before := time.Now()
	tok, err := c.Exchange(context.Background(), "auth-code-1", "https://app.example.com/callback", "verifier-1")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if !strings.HasSuffix(gotPath, "/token") {
		t.Errorf("path = %q, want suffix /token", gotPath)
	}
	if gotContentType != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %q, want application/x-www-form-urlencoded", gotContentType)
	}
	wantForm := map[string]string{
		"grant_type":    "authorization_code",
		"code":          "auth-code-1",
		"redirect_uri":  "https://app.example.com/callback",
		"client_id":     "cid",
		"client_secret": "secret",
		"code_verifier": "verifier-1",
	}
	for k, want := range wantForm {
		if got := gotForm.Get(k); got != want {
			t.Errorf("form[%q] = %q, want %q", k, got, want)
		}
	}

	if tok.AccessToken != "at-1" {
		t.Errorf("AccessToken = %q, want at-1", tok.AccessToken)
	}
	if tok.RefreshToken != "rt-1" {
		t.Errorf("RefreshToken = %q, want rt-1", tok.RefreshToken)
	}
	if tok.ExpiresAt.Before(before.Add(3599*time.Second)) || tok.ExpiresAt.After(time.Now().Add(3601*time.Second)) {
		t.Errorf("ExpiresAt = %v, want ~1h from now", tok.ExpiresAt)
	}
	wantScopes := []string{"openid", "email"}
	if len(tok.Scopes) != len(wantScopes) || tok.Scopes[0] != wantScopes[0] || tok.Scopes[1] != wantScopes[1] {
		t.Errorf("Scopes = %v, want %v", tok.Scopes, wantScopes)
	}
	if tok.Email != "person@example.com" {
		t.Errorf("Email = %q, want person@example.com", tok.Email)
	}
}

func TestClient_Exchange_ErrorResponse(t *testing.T) {
	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":"invalid_grant","error_description":"Bad code"}`)
	})

	tok, err := c.Exchange(context.Background(), "bad-code", "https://app.example.com/callback", "verifier-1")
	if err == nil {
		t.Fatal("Exchange: want error, got nil")
	}
	var he *httpError
	if !errors.As(err, &he) {
		t.Fatalf("err = %v (%T), want *httpError", err, err)
	}
	if he.StatusCode != http.StatusBadRequest {
		t.Errorf("StatusCode = %d, want 400", he.StatusCode)
	}
	if he.Message != "invalid_grant Bad code" {
		t.Errorf("Message = %q, want %q", he.Message, "invalid_grant Bad code")
	}
	if tok.AccessToken != "" || tok.RefreshToken != "" {
		t.Errorf("tok = %+v, want zero value on error", tok)
	}
}

func TestClient_Refresh_NoNewRefreshTokenFallsBackToInput(t *testing.T) {
	var gotForm url.Values

	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		gotForm, _ = url.ParseQuery(string(raw))
		io.WriteString(w, `{"access_token":"at-2","expires_in":1800,"scope":"openid"}`)
	})

	tok, err := c.Refresh(context.Background(), "refresh-tok-orig")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got := gotForm.Get("grant_type"); got != "refresh_token" {
		t.Errorf("grant_type = %q, want refresh_token", got)
	}
	if got := gotForm.Get("refresh_token"); got != "refresh-tok-orig" {
		t.Errorf("refresh_token form field = %q, want refresh-tok-orig", got)
	}
	if got := gotForm.Get("client_id"); got != "cid" {
		t.Errorf("client_id = %q, want cid", got)
	}
	if got := gotForm.Get("client_secret"); got != "secret" {
		t.Errorf("client_secret = %q, want secret", got)
	}
	// Google's token response carries no refresh_token on refresh grants;
	// the adapter documents carrying the input token through unchanged.
	if tok.RefreshToken != "refresh-tok-orig" {
		t.Errorf("RefreshToken = %q, want refresh-tok-orig (fallback)", tok.RefreshToken)
	}
	if tok.AccessToken != "at-2" {
		t.Errorf("AccessToken = %q, want at-2", tok.AccessToken)
	}
}

func TestClient_Refresh_ErrorResponse(t *testing.T) {
	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`)
	})

	_, err := c.Refresh(context.Background(), "stale-refresh-tok")
	var he *httpError
	if !errors.As(err, &he) {
		t.Fatalf("err = %v (%T), want *httpError", err, err)
	}
	if he.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, want 401", he.StatusCode)
	}
}

// erroringRoundTripper simulates a transport-level failure (e.g. DNS/conn
// refused) so the token() method's c.hc.Do error branch is exercised
// without a real server.
type erroringRoundTripper struct{}

func (erroringRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("boom: connection refused")
}

func TestClient_Exchange_TransportError(t *testing.T) {
	c := NewClient("cid", "secret", &http.Client{Transport: erroringRoundTripper{}})

	_, err := c.Exchange(context.Background(), "code", "https://app.example.com/callback", "verifier")
	if err == nil {
		t.Fatal("Exchange: want error, got nil")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v, want wrapped transport error", err)
	}
}

func TestClient_Refresh_DecodeError(t *testing.T) {
	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `not valid json`)
	})

	_, err := c.Refresh(context.Background(), "refresh-tok")
	if err == nil {
		t.Fatal("Refresh: want decode error, got nil")
	}
	if !strings.Contains(err.Error(), "decode token response") {
		t.Errorf("err = %v, want decode error", err)
	}
}

func TestEmailFromIDToken(t *testing.T) {
	tests := []struct {
		name    string
		idToken string
		want    string
	}{
		{
			name:    "valid token with email claim",
			idToken: fakeIDToken(`{"email":"user@example.com","email_verified":true}`),
			want:    "user@example.com",
		},
		{
			name:    "wrong number of segments",
			idToken: "not-a-jwt",
			want:    "",
		},
		{
			name:    "payload segment not valid base64url",
			idToken: "header.not!base64url.sig",
			want:    "",
		},
		{
			name:    "payload not valid JSON",
			idToken: base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." + base64.RawURLEncoding.EncodeToString([]byte("not-json")) + ".sig",
			want:    "",
		},
		{
			name:    "empty string",
			idToken: "",
			want:    "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := emailFromIDToken(tt.idToken); got != tt.want {
				t.Errorf("emailFromIDToken(%q) = %q, want %q", tt.idToken, got, tt.want)
			}
		})
	}
}

// sanity check that fakeIDToken actually round-trips through the same
// decode path emailFromIDToken uses, independent of the Client.
func TestFakeIDTokenHelper_RoundTrips(t *testing.T) {
	tok := fakeIDToken(`{"email":"round@trip.com"}`)
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("parts = %d, want 3", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var claims struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if claims.Email != "round@trip.com" {
		t.Errorf("Email = %q, want round@trip.com", claims.Email)
	}
}
