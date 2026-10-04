package httpapi

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func shrinkDeadlines(t *testing.T, handler, attachment time.Duration) {
	t.Helper()
	prevH, prevA := defaultHandlerDeadline, attachmentHandlerDeadline
	defaultHandlerDeadline, attachmentHandlerDeadline = handler, attachment
	t.Cleanup(func() { defaultHandlerDeadline, attachmentHandlerDeadline = prevH, prevA })
}

func TestDeadlineReturns504WhenHandlerExceeds(t *testing.T) {
	shrinkDeadlines(t, 50*time.Millisecond, time.Second)
	h := newHarness(t)
	h.users.prefsBlock = func(ctx context.Context) { <-ctx.Done() }
	rec := h.authed(http.MethodGet, "/v1/me/preferences", nil)
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504 (body=%s)", rec.Code, rec.Body.String())
	}
	e := decodeErr(t, rec)
	if e.Code != "timeout" || e.RequestID == "" {
		t.Fatalf("envelope = %+v", e)
	}
}

// A handler that returns without writing anything after the deadline gets
// the 504 envelope from the wrapper itself.
func TestDeadlineWrapperWrites504WhenNothingWritten(t *testing.T) {
	s := newHarness(t).server()
	wrapped := s.withDeadline(20*time.Millisecond, func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	rec := httptest.NewRecorder()
	wrapped(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusGatewayTimeout || decodeErr(t, rec).Code != "timeout" {
		t.Fatalf("status=%d body=%s, want 504 timeout", rec.Code, rec.Body.String())
	}
}

func TestStatusForDeadlineExceededIs504(t *testing.T) {
	status, code := statusFor(fmt.Errorf("provider call: %w", context.DeadlineExceeded))
	if status != http.StatusGatewayTimeout || code != "timeout" {
		t.Fatalf("statusFor(DeadlineExceeded) = %d %q", status, code)
	}
}

// Review Focus 2: a handler that already streamed part of a 200 is left alone.
func TestDeadlineDoesNotOverwritePartialResponse(t *testing.T) {
	h := newHarness(t)
	s := h.server()
	wrapped := s.withDeadline(30*time.Millisecond, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
		<-r.Context().Done()
		_, _ = w.Write([]byte("-tail"))
	})
	rec := httptest.NewRecorder()
	wrapped(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "partial-tail" {
		t.Fatalf("status=%d body=%q, want 200 partial-tail", rec.Code, rec.Body.String())
	}
}

// A handler that wrote only its status line before the deadline is also left
// alone (WriteHeader counts as "written").
func TestDeadlineDoesNotOverwriteHeaderOnlyResponse(t *testing.T) {
	s := newHarness(t).server()
	wrapped := s.withDeadline(20*time.Millisecond, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
		<-r.Context().Done()
	})
	rec := httptest.NewRecorder()
	wrapped(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("status=%d body=%q, want 204 empty", rec.Code, rec.Body.String())
	}
}

func TestAttachmentRouteUsesLongDeadline(t *testing.T) {
	shrinkDeadlines(t, 20*time.Millisecond, 500*time.Millisecond)
	h := newHarness(t)
	h.mail.attachmentBlock = func(ctx context.Context) {
		select {
		case <-ctx.Done():
		case <-time.After(100 * time.Millisecond): // longer than the general deadline, shorter than the attachment one
		}
	}
	h.mail.getAttachmentContentData = []byte("%PDF")
	rec := h.authed(http.MethodGet, "/v1/mail/attachments/a1/content", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (attachment deadline is the long one)", rec.Code)
	}
}

func TestStreamRouteHasNoDeadline(t *testing.T) {
	shrinkDeadlines(t, 20*time.Millisecond, 20*time.Millisecond)
	prev := keepaliveInterval
	keepaliveInterval = 40 * time.Millisecond
	t.Cleanup(func() { keepaliveInterval = prev })
	h := newHarness(t)
	srv := httptest.NewServer(h.handler())
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/collab/stream", nil)
	req.Header.Set("Authorization", "Bearer "+defaultToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	reader := bufio.NewReader(resp.Body)
	var keepalives int
	for keepalives < 3 {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("stream ended after %d keepalives (deadline applied?): %v", keepalives, err)
		}
		if strings.HasPrefix(line, ": keepalive") {
			keepalives++
		}
	}
}
