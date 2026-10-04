package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

var testInternalSecret = bytes.Repeat([]byte{0xab}, 32)

func withSecret(h *harness) *harness {
	h.deps.InternalSecret = testInternalSecret
	return h
}

func internalDelete(h *harness, id, header string) *httptest.ResponseRecorder {
	h.t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/v1/internal/users/"+id, nil)
	if header != "" {
		req.Header.Set("X-Internal-Secret", header)
	}
	rec := httptest.NewRecorder()
	h.handler().ServeHTTP(rec, req)
	return rec
}

func TestInternalPurgeWithoutConfiguredSecretIsUnknownRoute(t *testing.T) {
	h := newHarness(t) // InternalSecret empty
	unknown := h.anon(http.MethodDelete, "/v1/internal/nope", nil)
	rec := internalDelete(h, "u1", hex.EncodeToString(testInternalSecret))
	if rec.Code != http.StatusNotFound || rec.Body.String() != unknown.Body.String() {
		t.Fatalf("status=%d body=%q, want the mux's own 404 (%q)", rec.Code, rec.Body.String(), unknown.Body.String())
	}
	if h.lifecycle.purgeCalls != 0 {
		t.Fatal("Purge must not run without a configured secret")
	}
}

func TestInternalPurgeWrongOrMissingHeaderIsUnknownRoute(t *testing.T) {
	h := withSecret(newHarness(t))
	unknown := h.anon(http.MethodDelete, "/v1/internal/nope", nil)
	for name, header := range map[string]string{
		"missing":     "",
		"wrong bytes": strings.Repeat("cd", 32),
		"not hex":     strings.Repeat("zz", 32),
		"prefix only": strings.Repeat("ab", 31),
	} {
		t.Run(name, func(t *testing.T) {
			rec := internalDelete(h, "u1", header)
			if rec.Code != http.StatusNotFound || rec.Body.String() != unknown.Body.String() {
				t.Fatalf("status=%d body=%q, want the mux's own 404", rec.Code, rec.Body.String())
			}
		})
	}
	if h.lifecycle.purgeCalls != 0 {
		t.Fatal("Purge must not run on a bad header")
	}
}

// Review Focus 2: uppercase hex and surrounding whitespace still match; a
// wrong-length value never does.
func TestInternalPurgeHeaderNormalization(t *testing.T) {
	h := withSecret(newHarness(t))
	upper := strings.ToUpper(hex.EncodeToString(testInternalSecret))
	if rec := internalDelete(h, "u1", upper); rec.Code != http.StatusNoContent {
		t.Fatalf("uppercase hex: status=%d body=%s, want 204", rec.Code, rec.Body.String())
	}
	if rec := internalDelete(h, "u1", "  "+hex.EncodeToString(testInternalSecret)+"\t"); rec.Code != http.StatusNoContent {
		t.Fatalf("padded hex: status=%d, want 204", rec.Code)
	}
	if rec := internalDelete(h, "u1", hex.EncodeToString(testInternalSecret)+"ab"); rec.Code != http.StatusNotFound {
		t.Fatalf("over-long hex: status=%d, want 404", rec.Code)
	}
}

func TestInternalPurgeSuccess204(t *testing.T) {
	h := withSecret(newHarness(t))
	rec := internalDelete(h, "user_42", hex.EncodeToString(testInternalSecret))
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("status=%d body=%q, want 204 empty", rec.Code, rec.Body.String())
	}
	if h.lifecycle.purgeCalls != 1 || h.lifecycle.gotUserID != "user_42" {
		t.Fatalf("Purge calls=%d id=%q", h.lifecycle.purgeCalls, h.lifecycle.gotUserID)
	}
	if h.verifier.calls != 0 {
		t.Fatal("the internal route must never consult the JWT verifier")
	}
}

func TestInternalPurgeOwnsTeams409WithDetails(t *testing.T) {
	h := withSecret(newHarness(t))
	h.lifecycle.purgeErr = &domain.OwnsTeamsError{Teams: []domain.TeamRef{{ID: "t1", Name: "Design"}, {ID: "t2", Name: "Ops"}}}
	rec := internalDelete(h, "u1", hex.EncodeToString(testInternalSecret))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s, want 409", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"code":"owns_teams"`, `"details":{"teams":[`, `"name":"Design"`, `"id":"t2"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body %s missing %s", body, want)
		}
	}
}

func TestInternalPurgeBillingUnavailable502(t *testing.T) {
	h := withSecret(newHarness(t))
	h.lifecycle.purgeErr = fmt.Errorf("%w: cancel subscription: paddle 503", domain.ErrBillingUnavailable)
	rec := internalDelete(h, "u1", hex.EncodeToString(testInternalSecret))
	if rec.Code != http.StatusBadGateway || decodeErr(t, rec).Code != "billing_unavailable" {
		t.Fatalf("status=%d body=%s, want 502 billing_unavailable", rec.Code, rec.Body.String())
	}
}

func TestInternalPurgeIsNotCORSReflected(t *testing.T) {
	h := withSecret(newHarness(t))
	req := httptest.NewRequest(http.MethodOptions, "/v1/internal/users/u1", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", "DELETE")
	rec := httptest.NewRecorder()
	h.handler().ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("internal routes must not reflect CORS origins: %v", rec.Header())
	}
}

// A wrong method on the internal path must be the mux's own 404 too — a 405
// with an Allow header would reveal that the route exists.
func TestInternalPurgeWrongMethodIsUnknownRoute(t *testing.T) {
	h := withSecret(newHarness(t))
	unknown := h.anon(http.MethodDelete, "/v1/internal/nope", nil)
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut} {
		req := httptest.NewRequest(method, "/v1/internal/users/u1", nil)
		req.Header.Set("X-Internal-Secret", hex.EncodeToString(testInternalSecret))
		rec := httptest.NewRecorder()
		h.handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound || rec.Body.String() != unknown.Body.String() || rec.Header().Get("Allow") != "" {
			t.Fatalf("%s: status=%d allow=%q body=%q, want the mux's own 404", method, rec.Code, rec.Header().Get("Allow"), rec.Body.String())
		}
	}
	if h.lifecycle.purgeCalls != 0 {
		t.Fatal("Purge must only run on DELETE")
	}
}

func TestInternalPurgeBodyCap(t *testing.T) {
	h := withSecret(newHarness(t))
	req := httptest.NewRequest(http.MethodDelete, "/v1/internal/users/u1", strings.NewReader(strings.Repeat("x", internalBodyLimit+1)))
	req.Header.Set("X-Internal-Secret", hex.EncodeToString(testInternalSecret))
	rec := httptest.NewRecorder()
	h.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge || h.lifecycle.purgeCalls != 0 {
		t.Fatalf("status=%d purgeCalls=%d, want 413 and no purge", rec.Code, h.lifecycle.purgeCalls)
	}
}

// The internal route is outside every rate limiter: the web app's server
// (one IP) may purge many users in a row.
func TestInternalPurgeIsNotRateLimited(t *testing.T) {
	h := withSecret(newHarness(t))
	h.deps.RateLimits = RateLimits{PublicReadPerMin: 1, PublicWritePerMin: 1, UserPerMin: 1, MutateHeavyPerMin: 1, SearchPerMin: 1}
	handler := h.handler()
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodDelete, "/v1/internal/users/u1", nil)
		req.Header.Set("X-Internal-Secret", hex.EncodeToString(testInternalSecret))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("call %d: status=%d, want 204", i, rec.Code)
		}
	}
}

// --- export -----------------------------------------------------------------

func TestExportRouteIsHeavyClass(t *testing.T) {
	s, _ := build(newHarness(t).deps)
	if got := s.routeClasses()["GET /v1/me/export"]; got != classMutateHeavy {
		t.Fatalf("export class = %q, want %q", got, classMutateHeavy)
	}
}

// Like attachments and SSE, the export outlives the 60 s default deadline.
func TestExportExemptFromDefaultDeadline(t *testing.T) {
	shrinkDeadlines(t, 20*time.Millisecond, time.Second)
	h := newHarness(t)
	h.export.files = map[string]string{"profile.json": `{}`}
	h.export.delay = 80 * time.Millisecond
	if rec := h.authed(http.MethodGet, "/v1/me/export", nil); rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200 past the default deadline", rec.Code, rec.Body.String())
	}
}

func TestExportStreamsZipWithHeaders(t *testing.T) {
	h := newHarness(t)
	h.export.files = map[string]string{"profile.json": `{"id":"user_1"}`}
	rec := h.authed(http.MethodGet, "/v1/me/export", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Fatalf("Content-Type = %q", ct)
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, `attachment; filename="calendium-export-`) || !strings.HasSuffix(cd, `.zip"`) {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("response is not a readable zip: %v", err)
	}
	if len(zr.File) != 1 || zr.File[0].Name != "profile.json" {
		t.Fatalf("zip entries = %v", zr.File)
	}
	if h.export.gotUserID != defaultUserID {
		t.Fatalf("Export userID = %q", h.export.gotUserID)
	}
}

func TestExportThrottled409RetryAfter(t *testing.T) {
	h := newHarness(t)
	h.export.err = &domain.ExportThrottledError{RetryAfter: 1800 * time.Second}
	rec := h.authed(http.MethodGet, "/v1/me/export", nil)
	if rec.Code != http.StatusConflict || decodeErr(t, rec).Code != "export_throttled" {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") != "1800" {
		t.Fatalf("Retry-After = %q", rec.Header().Get("Retry-After"))
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("an error before the first file must be a JSON envelope, got %q", ct)
	}
}

func TestExportUnauthenticatedIs401(t *testing.T) {
	h := newHarness(t)
	if rec := h.anon(http.MethodGet, "/v1/me/export", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestExportUnderActAsIsForbidden(t *testing.T) {
	h, fd := delegHarness(t)
	rec := actAs(h, "principal_1", http.MethodGet, "/v1/me/export", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d, want 403 (export is never delegable)", rec.Code)
	}
	if fd.authorizeCalls != 0 || h.export.exportCalls != 0 {
		t.Fatal("rejected before any grant lookup or export")
	}
}

func TestExportMidStreamFailureDropsConnection(t *testing.T) {
	h := newHarness(t)
	h.export.files = map[string]string{"profile.json": `{"id":"user_1"}`}
	h.export.errAfterFirstFile = errors.New("db went away")
	srv := httptest.NewServer(h.handler())
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/me/export", nil)
	req.Header.Set("Authorization", "Bearer "+defaultToken)
	res, err := srv.Client().Do(req)
	if err != nil {
		return // connection reset before headers: acceptable
	}
	defer res.Body.Close()
	body, readErr := io.ReadAll(res.Body)
	if readErr == nil {
		if _, zerr := zip.NewReader(bytes.NewReader(body), int64(len(body))); zerr == nil {
			t.Fatal("a mid-stream failure must not produce a complete zip")
		}
	}
}

func TestExportNotWiredIs501(t *testing.T) {
	h := newHarness(t)
	h.deps.Export = nil
	if rec := h.authed(http.MethodGet, "/v1/me/export", nil); rec.Code != http.StatusNotImplemented {
		t.Fatalf("status=%d, want 501", rec.Code)
	}
}

// --- fakes ------------------------------------------------------------------

type fakeLifecycleService struct {
	purgeCalls int
	gotUserID  string
	purgeErr   error
}

func (f *fakeLifecycleService) Purge(_ context.Context, userID string) (port.PurgeReport, error) {
	f.purgeCalls++
	f.gotUserID = userID
	return port.PurgeReport{}, f.purgeErr
}

// fakeExportService writes `files` (in map order; tests use one file) and
// may fail before the first file (err) or after it (errAfterFirstFile).
type fakeExportService struct {
	files             map[string]string
	delay             time.Duration // slept before writing (deadline tests)
	err               error
	errAfterFirstFile error
	exportCalls       int
	gotUserID         string
}

func (f *fakeExportService) Export(ctx context.Context, userID string, sink port.ExportSink) error {
	f.exportCalls++
	f.gotUserID = userID
	if f.err != nil {
		return f.err
	}
	if f.delay > 0 {
		time.Sleep(f.delay)
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	for name, body := range f.files {
		w, err := sink.Create(name)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(w, body); err != nil {
			return err
		}
	}
	return f.errAfterFirstFile
}

var (
	_ port.UserLifecycleService = (*fakeLifecycleService)(nil)
	_ port.ExportService        = (*fakeExportService)(nil)
)
