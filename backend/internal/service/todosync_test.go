package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- fake todo provider ------------------------------------------------------

// todoProviderCall records one vendor call: the access token used plus the
// call's argument (cursor for SyncTasks, external id for complete/reopen).
type todoProviderCall struct{ token, arg string }

// fakeTodoProvider serves programmable sync pages keyed by request cursor and
// per-token errors, and records every vendor call.
type fakeTodoProvider struct {
	source domain.TaskSource

	// programmable
	pages       map[string]port.TodoSyncPage // keyed by request cursor
	errByToken  map[string]error             // SyncTasks error per access token
	completeErr error
	reopenErr   error

	// recording
	syncCalls []todoProviderCall
	syncLocs  []*time.Location // loc passed per SyncTasks call
	completed []todoProviderCall
	reopened  []todoProviderCall
}

func newTodoProvider() *fakeTodoProvider {
	return &fakeTodoProvider{
		source:     domain.TaskSourceTodoist,
		pages:      map[string]port.TodoSyncPage{},
		errByToken: map[string]error{},
	}
}

func (p *fakeTodoProvider) Source() domain.TaskSource { return p.source }

func (p *fakeTodoProvider) SyncTasks(_ context.Context, accessToken, cursor string, loc *time.Location) (port.TodoSyncPage, error) {
	p.syncCalls = append(p.syncCalls, todoProviderCall{accessToken, cursor})
	p.syncLocs = append(p.syncLocs, loc)
	if err := p.errByToken[accessToken]; err != nil {
		return port.TodoSyncPage{}, err
	}
	return p.pages[cursor], nil
}

func (p *fakeTodoProvider) CompleteTask(_ context.Context, accessToken, externalID string) error {
	p.completed = append(p.completed, todoProviderCall{accessToken, externalID})
	return p.completeErr
}

func (p *fakeTodoProvider) ReopenTask(_ context.Context, accessToken, externalID string) error {
	p.reopened = append(p.reopened, todoProviderCall{accessToken, externalID})
	return p.reopenErr
}

var _ port.TodoProvider = (*fakeTodoProvider)(nil)

// --- harness -----------------------------------------------------------------

type todoSyncHarness struct {
	svc          *TodoSyncService
	tasks        *fakeTaskRepo
	integrations *fakeIntegrationRepo
	syncState    *fakeSyncStateRepo
	provider     *fakeTodoProvider
	oauth        *fakeOAuthGateway
	prefs        *fakeCalendarPrefsRepo
	clock        *fakeClock
}

func newTodoSyncHarness(t *testing.T) *todoSyncHarness {
	t.Helper()
	h := &todoSyncHarness{
		tasks:        newTaskRepo(),
		integrations: newIntegrationRepo(),
		syncState:    newSyncStateRepo(),
		provider:     newTodoProvider(),
		oauth:        newOAuthGateway(),
		prefs:        newCalendarPrefsRepo(),
		clock:        newClock(time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)),
	}
	h.svc = NewTodoSyncService(TodoSyncDeps{
		Integrations: h.integrations,
		Tasks:        h.tasks,
		SyncState:    h.syncState,
		Prefs:        h.prefs,
		Providers:    map[domain.TaskSource]port.TodoProvider{domain.TaskSourceTodoist: h.provider},
		OAuth:        map[domain.IntegrationVendor]port.OAuthGateway{domain.IntegrationTodoist: h.oauth},
		Clock:        h.clock,
	})
	return h
}

// connect seeds an active Todoist connection with tokens (refresh token
// "refresh-<connID>").
func (h *todoSyncHarness) connect(t *testing.T, connID, userID, accessToken string) domain.IntegrationConnection {
	t.Helper()
	conn, err := h.integrations.Create(context.Background(), domain.IntegrationConnection{
		ID: connID, UserID: userID, Vendor: domain.IntegrationTodoist, Status: domain.IntegrationStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.integrations.SaveTokens(context.Background(), connID, port.TokenSet{
		AccessToken: accessToken, RefreshToken: "refresh-" + connID,
	}); err != nil {
		t.Fatal(err)
	}
	return conn
}

// mirrored builds a provider-shaped task (ExternalID+Source set, no ID/user).
func mirrored(title, externalID string) domain.Task {
	return domain.Task{Title: title, Source: domain.TaskSourceTodoist, ExternalID: externalID}
}

func userTasks(t *testing.T, repo *fakeTaskRepo, userID string) []domain.Task {
	t.Helper()
	list, err := repo.List(context.Background(), port.TaskQuery{UserID: userID, IncludeCompleted: true})
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func TestTodoSync(t *testing.T) {
	ctx := context.Background()

	t.Run("first pass mirrors tasks and persists the cursor", func(t *testing.T) {
		h := newTodoSyncHarness(t)
		h.connect(t, "c1", "u1", "tok1")
		due := time.Date(2026, 7, 21, 17, 0, 0, 0, time.UTC)
		withDue := mirrored("A", "e1")
		withDue.Due = &due
		h.provider.pages[""] = port.TodoSyncPage{Tasks: []domain.Task{withDue, mirrored("B", "e2")}, NextCursor: "cur-1"}

		if err := h.svc.SyncTodos(ctx); err != nil {
			t.Fatalf("SyncTodos() error = %v", err)
		}
		list := userTasks(t, h.tasks, "u1")
		if len(list) != 2 {
			t.Fatalf("len(tasks) = %d, want 2", len(list))
		}
		for _, task := range list {
			if task.UserID != "u1" || task.Source != domain.TaskSourceTodoist {
				t.Errorf("task %+v not scoped to u1/todoist", task)
			}
		}
		if list[0].Position == list[1].Position || list[0].Position <= 0 {
			t.Errorf("positions = %v/%v, want distinct appended positions", list[0].Position, list[1].Position)
		}
		if list[0].Title != "A" || list[0].Due == nil || !list[0].Due.Equal(due) {
			t.Errorf("task[0] = %+v, want A with due %v", list[0], due)
		}
		st, err := h.syncState.Get(ctx, "c1", "todoist")
		if err != nil || st.Cursor != "cur-1" {
			t.Errorf("cursor = %q (err %v), want cur-1 under key (c1, todoist)", st.Cursor, err)
		}
		if len(h.provider.syncCalls) != 1 || h.provider.syncCalls[0] != (todoProviderCall{"tok1", ""}) {
			t.Errorf("syncCalls = %v, want one full sync with tok1", h.provider.syncCalls)
		}
	})

	t.Run("updates refresh vendor fields, preserving local schedule and position", func(t *testing.T) {
		h := newTodoSyncHarness(t)
		h.connect(t, "c1", "u1", "tok1")
		start := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
		end := start.Add(time.Hour)
		seeded, err := h.tasks.Create(ctx, domain.Task{
			UserID: "u1", Title: "old title", Source: domain.TaskSourceTodoist, ExternalID: "e1",
			ScheduledStart: &start, ScheduledEnd: &end, Position: 42,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := h.syncState.Save(ctx, port.SyncState{AccountID: "c1", Resource: "todoist", Cursor: "cur-1"}); err != nil {
			t.Fatal(err)
		}
		due := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
		updated := mirrored("renamed", "e1")
		updated.Due = &due
		h.provider.pages["cur-1"] = port.TodoSyncPage{Tasks: []domain.Task{updated}, NextCursor: "cur-2"}

		if err := h.svc.SyncTodos(ctx); err != nil {
			t.Fatalf("SyncTodos() error = %v", err)
		}
		if got := h.provider.syncCalls[0].arg; got != "cur-1" {
			t.Errorf("sync cursor = %q, want cur-1 (incremental)", got)
		}
		stored, err := h.tasks.GetByID(ctx, seeded.ID)
		if err != nil {
			t.Fatalf("updated row replaced instead of updated: %v", err)
		}
		if stored.Title != "renamed" || stored.Due == nil || !stored.Due.Equal(due) {
			t.Errorf("vendor fields = %q/%v, want renamed/%v", stored.Title, stored.Due, due)
		}
		if stored.Position != 42 {
			t.Errorf("Position = %v, want 42 preserved", stored.Position)
		}
		if stored.ScheduledStart == nil || !stored.ScheduledStart.Equal(start) || stored.ScheduledEnd == nil {
			t.Errorf("schedule = %v..%v, want preserved %v..%v", stored.ScheduledStart, stored.ScheduledEnd, start, end)
		}
		if st, _ := h.syncState.Get(ctx, "c1", "todoist"); st.Cursor != "cur-2" {
			t.Errorf("cursor = %q, want cur-2", st.Cursor)
		}
	})

	t.Run("upstream deletion removes the mirror row but never a foreign user's row", func(t *testing.T) {
		h := newTodoSyncHarness(t)
		h.connect(t, "c1", "u1", "tok1")
		mine, err := h.tasks.Create(ctx, domain.Task{UserID: "u1", Title: "mine", Source: domain.TaskSourceTodoist, ExternalID: "e1"})
		if err != nil {
			t.Fatal(err)
		}
		theirs, err := h.tasks.Create(ctx, domain.Task{UserID: "u2", Title: "theirs", Source: domain.TaskSourceTodoist, ExternalID: "e1"})
		if err != nil {
			t.Fatal(err)
		}
		h.provider.pages[""] = port.TodoSyncPage{DeletedIDs: []string{"e1", "never-mirrored"}, NextCursor: "cur-1"}

		if err := h.svc.SyncTodos(ctx); err != nil {
			t.Fatalf("SyncTodos() error = %v", err)
		}
		if _, err := h.tasks.GetByID(ctx, mine.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("u1's mirror row survives deletion: err = %v", err)
		}
		if _, err := h.tasks.GetByID(ctx, theirs.ID); err != nil {
			t.Errorf("u2's row was deleted cross-tenant: err = %v", err)
		}
	})

	t.Run("HasMore drives follow-up pages before the pass ends", func(t *testing.T) {
		h := newTodoSyncHarness(t)
		h.connect(t, "c1", "u1", "tok1")
		h.provider.pages[""] = port.TodoSyncPage{Tasks: []domain.Task{mirrored("A", "e1")}, NextCursor: "p1", HasMore: true}
		h.provider.pages["p1"] = port.TodoSyncPage{Tasks: []domain.Task{mirrored("B", "e2")}, NextCursor: "p2"}

		if err := h.svc.SyncTodos(ctx); err != nil {
			t.Fatalf("SyncTodos() error = %v", err)
		}
		if len(h.provider.syncCalls) != 2 || h.provider.syncCalls[1].arg != "p1" {
			t.Fatalf("syncCalls = %v, want follow-up call at p1", h.provider.syncCalls)
		}
		if got := userTasks(t, h.tasks, "u1"); len(got) != 2 {
			t.Errorf("len(tasks) = %d, want 2 across pages", len(got))
		}
		if st, _ := h.syncState.Get(ctx, "c1", "todoist"); st.Cursor != "p2" {
			t.Errorf("final cursor = %q, want p2", st.Cursor)
		}
	})

	t.Run("a broken connection is marked errored without stalling others", func(t *testing.T) {
		h := newTodoSyncHarness(t)
		h.connect(t, "c1", "u1", "bad")
		h.connect(t, "c2", "u2", "good")
		h.provider.errByToken["bad"] = errors.New("todoist exploded")
		h.provider.pages[""] = port.TodoSyncPage{Tasks: []domain.Task{mirrored("A", "e1")}, NextCursor: "n1"}

		if err := h.svc.SyncTodos(ctx); err != nil {
			t.Fatalf("SyncTodos() error = %v, want nil (per-connection isolation)", err)
		}
		broken, err := h.integrations.GetByID(ctx, "c1")
		if err != nil {
			t.Fatal(err)
		}
		if broken.Status != domain.IntegrationStatusError || broken.LastError == nil {
			t.Errorf("broken conn = %q/%v, want error status with LastError", broken.Status, broken.LastError)
		}
		if got := userTasks(t, h.tasks, "u2"); len(got) != 1 {
			t.Errorf("u2 tasks = %d, want 1 (not stalled by u1's failure)", len(got))
		}
	})

	t.Run("a previously errored connection heals on a clean pass", func(t *testing.T) {
		h := newTodoSyncHarness(t)
		conn := h.connect(t, "c1", "u1", "tok1")
		msg := "old failure"
		conn.Status = domain.IntegrationStatusError
		conn.LastError = &msg
		if err := h.integrations.Update(ctx, conn); err != nil {
			t.Fatal(err)
		}
		h.provider.pages[""] = port.TodoSyncPage{NextCursor: "n1"}

		if err := h.svc.SyncTodos(ctx); err != nil {
			t.Fatalf("SyncTodos() error = %v", err)
		}
		healed, err := h.integrations.GetByID(ctx, "c1")
		if err != nil {
			t.Fatal(err)
		}
		if healed.Status != domain.IntegrationStatusActive || healed.LastError != nil {
			t.Errorf("healed conn = %q/%v, want active with nil LastError", healed.Status, healed.LastError)
		}
	})

	t.Run("401 refreshes through the gateway once and retries", func(t *testing.T) {
		h := newTodoSyncHarness(t)
		h.connect(t, "c1", "u1", "stale")
		h.provider.errByToken["stale"] = fmt.Errorf("%w: token rejected", domain.ErrUnauthorized)
		h.oauth.refreshToken = port.OAuthToken{TokenSet: port.TokenSet{AccessToken: "fresh"}}
		h.provider.pages[""] = port.TodoSyncPage{Tasks: []domain.Task{mirrored("A", "e1")}, NextCursor: "n1"}

		if err := h.svc.SyncTodos(ctx); err != nil {
			t.Fatalf("SyncTodos() error = %v", err)
		}
		if h.oauth.refreshCalls != 1 || h.oauth.lastRefreshToken != "refresh-c1" {
			t.Errorf("refresh calls = %d with %q, want 1 with refresh-c1", h.oauth.refreshCalls, h.oauth.lastRefreshToken)
		}
		saved, err := h.integrations.GetTokens(ctx, "c1")
		if err != nil {
			t.Fatal(err)
		}
		if saved.AccessToken != "fresh" || saved.RefreshToken != "refresh-c1" {
			t.Errorf("saved tokens = %+v, want fresh access + preserved refresh", saved)
		}
		if len(h.provider.syncCalls) != 2 || h.provider.syncCalls[1].token != "fresh" {
			t.Errorf("syncCalls = %v, want retry with fresh token", h.provider.syncCalls)
		}
		if got := userTasks(t, h.tasks, "u1"); len(got) != 1 {
			t.Errorf("tasks after refresh = %d, want 1", len(got))
		}
		if conn, _ := h.integrations.GetByID(ctx, "c1"); conn.Status != domain.IntegrationStatusActive {
			t.Errorf("conn status = %q, want active after successful retry", conn.Status)
		}
	})

	t.Run("failed refresh marks the connection errored", func(t *testing.T) {
		h := newTodoSyncHarness(t)
		h.connect(t, "c1", "u1", "stale")
		h.provider.errByToken["stale"] = fmt.Errorf("%w: token rejected", domain.ErrUnauthorized)
		h.oauth.refreshErr = errors.New("grant revoked")

		if err := h.svc.SyncTodos(ctx); err != nil {
			t.Fatalf("SyncTodos() error = %v, want nil (recorded on the connection)", err)
		}
		conn, err := h.integrations.GetByID(ctx, "c1")
		if err != nil {
			t.Fatal(err)
		}
		if conn.Status != domain.IntegrationStatusError || conn.LastError == nil {
			t.Errorf("conn = %q/%v, want errored with LastError", conn.Status, conn.LastError)
		}
	})

	t.Run("disconnect mid-sync is tolerated silently", func(t *testing.T) {
		h := newTodoSyncHarness(t)
		h.connect(t, "c1", "u1", "tok1")
		delete(h.integrations.tokens, "c1") // tokens purged between list and sync

		if err := h.svc.SyncTodos(ctx); err != nil {
			t.Fatalf("SyncTodos() error = %v, want nil", err)
		}
		if len(h.provider.syncCalls) != 0 {
			t.Errorf("syncCalls = %v, want none", h.provider.syncCalls)
		}
		if conn, _ := h.integrations.GetByID(ctx, "c1"); conn.Status != domain.IntegrationStatusActive {
			t.Errorf("conn status = %q, want active (not an error)", conn.Status)
		}
	})

	t.Run("unconnected users cost no vendor calls", func(t *testing.T) {
		h := newTodoSyncHarness(t)
		if err := h.svc.SyncTodos(ctx); err != nil {
			t.Fatalf("SyncTodos() error = %v", err)
		}
		if len(h.provider.syncCalls) != 0 {
			t.Errorf("syncCalls = %v, want none without connections", h.provider.syncCalls)
		}
	})

	t.Run("floating dues: the owner's prefs timezone is threaded to the provider", func(t *testing.T) {
		h := newTodoSyncHarness(t)
		h.connect(t, "c1", "u1", "tok1")
		p := domain.DefaultCalendarPrefs("u1")
		p.TimeZone = "Europe/Amsterdam"
		h.prefs.byUser["u1"] = p
		h.provider.pages[""] = port.TodoSyncPage{NextCursor: "n1"}

		if err := h.svc.SyncTodos(ctx); err != nil {
			t.Fatalf("SyncTodos() error = %v", err)
		}
		if len(h.provider.syncLocs) != 1 || h.provider.syncLocs[0] == nil {
			t.Fatalf("syncLocs = %v, want one non-nil location", h.provider.syncLocs)
		}
		if got := h.provider.syncLocs[0].String(); got != "Europe/Amsterdam" {
			t.Errorf("loc = %q, want Europe/Amsterdam (prefs timezone)", got)
		}
	})

	t.Run("floating dues: default and broken prefs timezones fall back to UTC", func(t *testing.T) {
		h := newTodoSyncHarness(t)
		h.connect(t, "c1", "u1", "tok1") // no prefs row → defaults (UTC)
		h.connect(t, "c2", "u2", "tok2")
		broken := domain.DefaultCalendarPrefs("u2")
		broken.TimeZone = "Not/AZone" // repo-level corruption must not fail the pass
		h.prefs.byUser["u2"] = broken
		h.provider.pages[""] = port.TodoSyncPage{NextCursor: "n1"}

		if err := h.svc.SyncTodos(ctx); err != nil {
			t.Fatalf("SyncTodos() error = %v", err)
		}
		if len(h.provider.syncLocs) != 2 {
			t.Fatalf("syncLocs = %v, want 2", h.provider.syncLocs)
		}
		for i, loc := range h.provider.syncLocs {
			if loc != time.UTC {
				t.Errorf("syncLocs[%d] = %v, want UTC fallback", i, loc)
			}
		}
	})

	t.Run("new mirror positions append after existing local tasks", func(t *testing.T) {
		h := newTodoSyncHarness(t)
		h.connect(t, "c1", "u1", "tok1")
		if _, err := h.tasks.Create(ctx, domain.Task{UserID: "u1", Title: "local", Position: 5000}); err != nil {
			t.Fatal(err)
		}
		h.provider.pages[""] = port.TodoSyncPage{Tasks: []domain.Task{mirrored("A", "e1")}, NextCursor: "n1"}

		if err := h.svc.SyncTodos(ctx); err != nil {
			t.Fatalf("SyncTodos() error = %v", err)
		}
		task, err := h.tasks.GetByExternalID(ctx, "u1", domain.TaskSourceTodoist, "e1")
		if err != nil {
			t.Fatal(err)
		}
		if task.Position != 5000+positionStep {
			t.Errorf("Position = %v, want %v (appended after the rail)", task.Position, 5000+positionStep)
		}
	})
}
