package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"calendium/backend/internal/domain"
)

func jsonLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// jsonLogLines parses every JSON line the harness logger wrote.
func jsonLogLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line is not JSON: %q", line)
		}
		out = append(out, m)
	}
	return out
}

func httpLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	for _, m := range jsonLogLines(t, buf) {
		if m["msg"] == "http" {
			return m
		}
	}
	t.Fatal("no msg=http line logged")
	return nil
}

func TestLogLineFields(t *testing.T) {
	var buf bytes.Buffer
	h := newHarness(t)
	h.deps.Logger = jsonLogger(&buf)
	req := httptest.NewRequest(http.MethodGet, "/v1/me?token=should-not-be-logged", nil)
	req.RemoteAddr = "203.0.113.9:4321"
	req.Header.Set("Authorization", "Bearer "+defaultToken)
	rec := httptest.NewRecorder()
	h.handler().ServeHTTP(rec, req)

	line := httpLine(t, &buf)
	for _, key := range []string{"request_id", "method", "route", "status", "duration_ms", "bytes", "client_ip", "user_id", "actor_id"} {
		if _, ok := line[key]; !ok {
			t.Errorf("log line missing %q: %v", key, line)
		}
	}
	if line["route"] != "/v1/me" {
		t.Errorf("route = %v, want /v1/me (pattern, not raw path)", line["route"])
	}
	if line["user_id"] != defaultUserID {
		t.Errorf("user_id = %v, want %s", line["user_id"], defaultUserID)
	}
	if line["client_ip"] != "203.0.113.9" {
		t.Errorf("client_ip = %v, want 203.0.113.9", line["client_ip"])
	}
	if line["request_id"] != rec.Header().Get("X-Request-Id") {
		t.Errorf("request_id = %v, want the echoed header %q", line["request_id"], rec.Header().Get("X-Request-Id"))
	}
	if b, _ := line["bytes"].(float64); int(b) != rec.Body.Len() {
		t.Errorf("bytes = %v, want %d", line["bytes"], rec.Body.Len())
	}
	if strings.Contains(buf.String(), "should-not-be-logged") {
		t.Fatalf("query string leaked into logs: %s", buf.String())
	}
	if _, ok := line["path"]; ok {
		t.Fatalf("raw path must not be logged: %v", line)
	}
}

func TestLogActorUnderActAs(t *testing.T) {
	var buf bytes.Buffer
	h, _ := delegHarness(t)
	h.deps.Logger = jsonLogger(&buf)
	rec := actAs(h, "principal_1", http.MethodGet, "/v1/mail/threads", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body=%s)", rec.Code, rec.Body.String())
	}
	line := httpLine(t, &buf)
	if line["user_id"] != "principal_1" || line["actor_id"] != defaultUserID {
		t.Fatalf("user_id=%v actor_id=%v, want principal_1 / %s", line["user_id"], line["actor_id"], defaultUserID)
	}
}

func TestRedactPath(t *testing.T) {
	tests := map[string]string{
		"/v1/shared/threads/tok123":         "/v1/shared/threads/[redacted]",
		"/v1/shared/threads/tok123/stream":  "/v1/shared/threads/[redacted]/stream",
		"/v1/public/polls/tok456":           "/v1/public/polls/[redacted]",
		"/v1/public/polls/tok456/votes":     "/v1/public/polls/[redacted]/votes",
		"/v1/mail/contacts/ada@example.com": "/v1/mail/contacts/[redacted]",
		"/v1/invitations/accept":            "/v1/invitations/accept",
		"/v1/mail/threads/t1":               "/v1/mail/threads/t1",
		"/nope":                             "/nope",
	}
	for in, want := range tests {
		if got := redactPath(in); got != want {
			t.Errorf("redactPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLogRedactsTokensOn404(t *testing.T) {
	var buf bytes.Buffer
	h := newHarness(t)
	h.deps.Logger = jsonLogger(&buf)
	rec := h.anon(http.MethodGet, "/v1/shared/threads/secret-share-token/unknown", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if strings.Contains(buf.String(), "secret-share-token") {
		t.Fatalf("share token leaked into logs: %s", buf.String())
	}
	if line := httpLine(t, &buf); line["route"] != "/v1/shared/threads/[redacted]/unknown" {
		t.Fatalf("route = %v", line["route"])
	}
}

func TestInvitationTokenNeverLogged(t *testing.T) {
	var buf bytes.Buffer
	h := newHarness(t)
	h.deps.Logger = jsonLogger(&buf)
	rec := h.authed(http.MethodPost, "/v1/invitations/accept", strings.NewReader(`{"token":"invite-secret-token"}`))
	if rec.Code == http.StatusInternalServerError {
		t.Fatalf("unexpected 500: %s", rec.Body.String())
	}
	if strings.Contains(buf.String(), "invite-secret-token") {
		t.Fatalf("invitation token leaked into logs: %s", buf.String())
	}
}

func TestWriteErrorLogsRouteAndRequestID(t *testing.T) {
	var buf bytes.Buffer
	h := newHarness(t)
	h.deps.Logger = jsonLogger(&buf)
	h.mail.getThreadErr = domainNotFound()
	rec := h.authed(http.MethodGet, "/v1/mail/threads/t-123", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	var found bool
	for _, m := range jsonLogLines(t, &buf) {
		if m["msg"] == "request rejected" {
			found = true
			if m["route"] != "/v1/mail/threads/{id}" || m["request_id"] != rec.Header().Get("X-Request-Id") {
				t.Fatalf("rejection line = %v", m)
			}
			if _, ok := m["path"]; ok {
				t.Fatalf("rejection line still logs the raw path: %v", m)
			}
		}
	}
	if !found {
		t.Fatal("no 'request rejected' line")
	}
}

func TestResponseWriterFirstStatusWins(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rec, status: http.StatusOK}
	rw.WriteHeader(http.StatusTeapot)
	rw.WriteHeader(http.StatusInternalServerError)
	_, _ = rw.Write([]byte("abc"))
	if rw.status != http.StatusTeapot || rec.Code != http.StatusTeapot || rw.bytes != 3 || !rw.wrote {
		t.Fatalf("status=%d rec=%d bytes=%d wrote=%v", rw.status, rec.Code, rw.bytes, rw.wrote)
	}
	if rw.Unwrap() != rec {
		t.Fatal("Unwrap must return the wrapped writer")
	}
}

func domainNotFound() error { return fmt.Errorf("thread: %w", domain.ErrNotFound) }
