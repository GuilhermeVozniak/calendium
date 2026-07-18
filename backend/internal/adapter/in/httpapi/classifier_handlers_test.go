package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

func TestClassifierHandlersRequireAuth(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"list", http.MethodGet, "/v1/classifiers"},
		{"create", http.MethodPost, "/v1/classifiers"},
		{"update", http.MethodPatch, "/v1/classifiers/c1"},
		{"delete", http.MethodDelete, "/v1/classifiers/c1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			rec := h.anon(tt.method, tt.path, nil)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
		})
	}
}

func TestHandleListClassifiers(t *testing.T) {
	h := newHarness(t)
	h.ai.listClassifiersRet = []domain.AiClassifier{
		{ID: "c1", Name: "Invoices", Prompt: "invoice emails", TargetSplit: domain.SplitImportant, Enabled: true},
	}
	rec := h.authed(http.MethodGet, "/v1/classifiers", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestHandleListClassifiersPropagatesError(t *testing.T) {
	h := newHarness(t)
	h.ai.listClassifiersErr = fmt.Errorf("%w: boom", domain.ErrPaymentRequired)
	rec := h.authed(http.MethodGet, "/v1/classifiers", nil)
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402", rec.Code)
	}
}

func TestHandleCreateClassifier(t *testing.T) {
	h := newHarness(t)
	h.ai.createClassifierRet = domain.AiClassifier{ID: "c1", Name: "Invoices", Prompt: "invoice emails", TargetSplit: domain.SplitImportant, Enabled: true}
	in := port.ClassifierInput{Name: "Invoices", Prompt: "invoice emails", TargetSplit: domain.SplitImportant, Enabled: true}
	rec := h.authed(http.MethodPost, "/v1/classifiers", jsonBody(t, in))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if h.ai.gotCreateClassifier.Name != "Invoices" {
		t.Fatalf("gotCreateClassifier = %+v, want Name=Invoices", h.ai.gotCreateClassifier)
	}
	if h.ai.gotCreateClassifierUID != defaultUserID {
		t.Fatalf("gotCreateClassifierUID = %q, want %q", h.ai.gotCreateClassifierUID, defaultUserID)
	}
}

func TestHandleCreateClassifierBadJSON(t *testing.T) {
	h := newHarness(t)
	rec := h.authed(http.MethodPost, "/v1/classifiers", strings.NewReader("{not json"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if got := decodeErr(t, rec); got.Code != "validation_failed" {
		t.Fatalf("code = %q, want validation_failed", got.Code)
	}
}

func TestHandleCreateClassifierValidationError(t *testing.T) {
	h := newHarness(t)
	h.ai.createClassifierErr = fmt.Errorf("%w: name is required", domain.ErrValidation)
	rec := h.authed(http.MethodPost, "/v1/classifiers", jsonBody(t, port.ClassifierInput{}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestHandleUpdateClassifier(t *testing.T) {
	h := newHarness(t)
	h.ai.updateClassifierRet = domain.AiClassifier{ID: "c1", Name: "Renamed", Prompt: "p", LabelName: "L", Enabled: true}
	in := port.ClassifierInput{Name: "Renamed", Prompt: "p", LabelName: "L", Enabled: true}
	rec := h.authed(http.MethodPatch, "/v1/classifiers/c1", jsonBody(t, in))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if h.ai.gotUpdateClassifierID != "c1" {
		t.Fatalf("gotUpdateClassifierID = %q, want c1", h.ai.gotUpdateClassifierID)
	}
	if h.ai.gotUpdateClassifier.Name != "Renamed" {
		t.Fatalf("gotUpdateClassifier = %+v, want Name=Renamed", h.ai.gotUpdateClassifier)
	}
}

func TestHandleUpdateClassifierNotFound(t *testing.T) {
	h := newHarness(t)
	h.ai.updateClassifierErr = domain.ErrNotFound
	rec := h.authed(http.MethodPatch, "/v1/classifiers/does-not-exist", jsonBody(t, port.ClassifierInput{Name: "n", Prompt: "p", LabelName: "L"}))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestHandleDeleteClassifier(t *testing.T) {
	h := newHarness(t)
	rec := h.authed(http.MethodDelete, "/v1/classifiers/c1", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body=%s)", rec.Code, rec.Body.String())
	}
	if h.ai.gotDeleteClassifierID != "c1" {
		t.Fatalf("gotDeleteClassifierID = %q, want c1", h.ai.gotDeleteClassifierID)
	}
}

func TestHandleDeleteClassifierNotFound(t *testing.T) {
	h := newHarness(t)
	h.ai.deleteClassifierErr = domain.ErrNotFound
	rec := h.authed(http.MethodDelete, "/v1/classifiers/does-not-exist", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if !errors.Is(h.ai.deleteClassifierErr, domain.ErrNotFound) {
		t.Fatal("sanity: deleteClassifierErr should wrap ErrNotFound")
	}
}
