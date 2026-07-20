package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

type fakeInsightsService struct {
	ret     domain.TimeInsights
	err     error
	calls   int
	gotUser string
	gotFrom time.Time
	gotTo   time.Time
}

func (f *fakeInsightsService) TimeInsights(_ context.Context, userID string, from, to time.Time) (domain.TimeInsights, error) {
	f.calls++
	f.gotUser = userID
	f.gotFrom = from
	f.gotTo = to
	return f.ret, f.err
}

var _ port.InsightsService = (*fakeInsightsService)(nil)

const insightsPath = "/v1/insights/time?from=2026-07-06T00%3A00%3A00Z&to=2026-07-13T00%3A00%3A00Z"

func TestTimeInsightsAnswers501WhenUnwired(t *testing.T) {
	h := newHarness(t) // Insights left nil
	rec := h.authed(http.MethodGet, insightsPath, nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501 (body=%s)", rec.Code, rec.Body.String())
	}
	if got := decodeErr(t, rec).Code; got != "not_implemented" {
		t.Fatalf("error code = %q, want not_implemented", got)
	}
}

func TestTimeInsightsRequiresAuth(t *testing.T) {
	h := newHarness(t)
	h.deps.Insights = &fakeInsightsService{}
	rec := h.anon(http.MethodGet, insightsPath, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestTimeInsightsValidatesTimestamps(t *testing.T) {
	h := newHarness(t)
	fake := &fakeInsightsService{}
	h.deps.Insights = fake
	for _, target := range []string{
		"/v1/insights/time",
		"/v1/insights/time?from=2026-07-06T00:00:00Z",
		"/v1/insights/time?from=yesterday&to=2026-07-13T00:00:00Z",
	} {
		rec := h.authed(http.MethodGet, target, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400 (body=%s)", target, rec.Code, rec.Body.String())
		}
	}
	if fake.calls != 0 {
		t.Fatalf("service calls = %d, want 0 for malformed params", fake.calls)
	}
}

func TestTimeInsightsServesDocument(t *testing.T) {
	h := newHarness(t)
	from := time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	fake := &fakeInsightsService{ret: domain.TimeInsights{
		From: from, To: to,
		MeetingMinutes: 150, FocusMinutes: 180, TaskMinutes: 60, MeetingCount: 2, FocusGoalMinutes: 600,
		TopPeople: []domain.PersonStat{{Email: "alice@example.com", Name: "Alice", Meetings: 2, Minutes: 150}},
		ByDay:     []domain.DayStat{{Date: "2026-07-06", MeetingMinutes: 60}},
	}}
	h.deps.Insights = fake

	rec := h.authed(http.MethodGet, insightsPath, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var got domain.TimeInsights
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.MeetingMinutes != 150 || got.FocusMinutes != 180 || got.TaskMinutes != 60 || got.MeetingCount != 2 {
		t.Fatalf("got = %+v", got)
	}
	if len(got.TopPeople) != 1 || got.TopPeople[0].Email != "alice@example.com" {
		t.Fatalf("TopPeople = %+v", got.TopPeople)
	}
	if fake.gotUser != defaultUserID {
		t.Fatalf("gotUser = %q, want %q", fake.gotUser, defaultUserID)
	}
	if !fake.gotFrom.Equal(from) || !fake.gotTo.Equal(to) {
		t.Fatalf("range = %v..%v, want %v..%v", fake.gotFrom, fake.gotTo, from, to)
	}
}

func TestTimeInsightsMapsServiceErrors(t *testing.T) {
	h := newHarness(t)
	h.deps.Insights = &fakeInsightsService{err: fmt.Errorf("%w: range must be 92 days or less", domain.ErrValidation)}
	rec := h.authed(http.MethodGet, insightsPath, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}

	h.deps.Insights = &fakeInsightsService{err: fmt.Errorf("%w: no subscription", domain.ErrPaymentRequired)}
	rec = h.authed(http.MethodGet, insightsPath, nil)
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402 (body=%s)", rec.Code, rec.Body.String())
	}
}
