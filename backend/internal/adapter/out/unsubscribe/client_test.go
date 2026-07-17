package unsubscribe

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPostOneClickSendsRFC8058Body(t *testing.T) {
	var gotBody, gotContentType, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody, gotContentType, gotMethod = string(b), r.Header.Get("Content-Type"), r.Method
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := New().PostOneClick(context.Background(), srv.URL+"/u?id=1"); err != nil {
		t.Fatalf("PostOneClick: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if gotContentType != "application/x-www-form-urlencoded" {
		t.Fatalf("content-type = %q", gotContentType)
	}
	if gotBody != "List-Unsubscribe=One-Click" {
		t.Fatalf("body = %q", gotBody)
	}
}

func TestPostOneClickRejectsBadTargets(t *testing.T) {
	if err := New().PostOneClick(context.Background(), "mailto:x@y.z"); err == nil {
		t.Fatal("non-http url accepted")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	if err := New().PostOneClick(context.Background(), srv.URL); err == nil {
		t.Fatal("non-2xx status accepted")
	}
}
