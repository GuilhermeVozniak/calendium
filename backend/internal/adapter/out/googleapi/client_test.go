package googleapi

import (
	"net/http"
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
