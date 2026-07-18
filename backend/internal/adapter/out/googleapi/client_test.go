package googleapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"calendium/backend/internal/domain"
)

func TestHTTPError_Error(t *testing.T) {
	err := &httpError{StatusCode: 429, Message: "rate limited"}
	got := err.Error()
	want := "google api: http 429: rate limited"
	if got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestHTTPError_Unwrap(t *testing.T) {
	cases := []struct {
		name       string
		statusCode int
		want       error
	}{
		{"unauthorized maps to ErrUnauthorized", http.StatusUnauthorized, domain.ErrUnauthorized},
		{"forbidden maps to ErrUnauthorized", http.StatusForbidden, domain.ErrUnauthorized},
		{"not found maps to ErrNotFound", http.StatusNotFound, domain.ErrNotFound},
		{"server error has no sentinel", http.StatusInternalServerError, nil},
		{"bad request has no sentinel", http.StatusBadRequest, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := &httpError{StatusCode: tc.statusCode, Message: "x"}
			if got := err.Unwrap(); got != tc.want {
				t.Errorf("Unwrap() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestClient_DoJSONLimit_ExceedsCapReturnsValidationError exercises the
// size-limit-exceeded path cheaply (a tiny maxBytes rather than the real
// 64MB attachment cap) to confirm an oversized 2xx body is reported as a
// clean domain.ErrValidation-wrapped error instead of a truncated,
// opaque JSON decode failure.
func TestClient_DoJSONLimit_ExceedsCapReturnsValidationError(t *testing.T) {
	const maxBytes = 16

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Well over maxBytes; a legitimate small response would never
		// trip this.
		io.WriteString(w, `{"data":"`+string(make([]byte, maxBytes*4))+`"}`)
	}))
	t.Cleanup(srv.Close)
	c := NewClient("cid", "secret", rewriteClient(t, srv.URL))

	var out struct {
		Data string `json:"data"`
	}
	err := c.doJSONLimit(context.Background(), http.MethodGet, srv.URL, "tok", nil, &out, maxBytes)
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v, want wrapped domain.ErrValidation", err)
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		name string
		s    string
		n    int
		want string
	}{
		{"shorter than limit unchanged", "hello", 10, "hello"},
		{"exactly at limit unchanged", "hello", 5, "hello"},
		{"longer than limit gets ellipsis", "hello world", 5, "hello..."},
		{"empty string unchanged", "", 5, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := truncate(tc.s, tc.n); got != tc.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tc.s, tc.n, got, tc.want)
			}
		})
	}
}
