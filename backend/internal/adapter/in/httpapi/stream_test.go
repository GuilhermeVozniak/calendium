package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// waitFor polls cond until it holds or the deadline expires.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// readSSEEvent scans frames until it finds one carrying data, returning the
// decoded CollabEvent and the event-name line (empty when absent).
func readSSEEvent(t *testing.T, r *bufio.Reader) (port.CollabEvent, string) {
	t.Helper()
	name, data := "", ""
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("stream ended while waiting for a data frame: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "event:"):
			name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data += strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		case line == "" && data != "":
			var ev port.CollabEvent
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				t.Fatalf("invalid data frame %q: %v", data, err)
			}
			return ev, name
		}
	}
}

func TestCollabStreamRequiresAuth(t *testing.T) {
	h := newHarness(t)
	rec := h.anon(http.MethodGet, "/v1/collab/stream", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if got := decodeErr(t, rec).Code; got != "unauthorized" {
		t.Fatalf("error code = %q, want unauthorized", got)
	}
}

// TestCollabStreamDeliversScopedEvents drives the full middleware stack over
// a real HTTP connection: content type, topic scoping (user + member teams,
// never a foreign team), SSE framing, and context-driven teardown with no
// leaked subscription on client disconnect.
func TestCollabStreamDeliversScopedEvents(t *testing.T) {
	h := newHarness(t)
	h.teams.setTeams([]domain.Team{{ID: "team_1", Name: "Ops"}})
	srv := httptest.NewServer(h.handler())
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/collab/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+defaultToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	waitFor(t, "subscription", func() bool { return h.events.activeSubscribers() == 1 })
	if got := h.teams.listedUserID(); got != defaultUserID {
		t.Fatalf("teams listed for %q, want %q", got, defaultUserID)
	}
	want := []string{"user:" + defaultUserID, "team:team_1"}
	if topics := h.events.subscribedTopics(); len(topics) != 1 || !reflect.DeepEqual(topics[0], want) {
		t.Fatalf("subscribed topics = %v, want %v", topics, want)
	}

	// Cross-tenant scoping: a foreign team's event published first must
	// never reach this subscriber; the marker published second arrives
	// first (and only).
	h.events.Publish(port.CollabEvent{Topic: "team:foreign", Type: "comment.created", Payload: json.RawMessage(`{"secret":true}`)})
	h.events.Publish(port.CollabEvent{Topic: "team:team_1", Type: "comment.created", Payload: json.RawMessage(`{"id":"c1"}`)})

	reader := bufio.NewReader(resp.Body)
	ev, name := readSSEEvent(t, reader)
	if ev.Topic != "team:team_1" || string(ev.Payload) != `{"id":"c1"}` {
		t.Fatalf("received wrong event (cross-tenant leak?): %+v", ev)
	}
	if name != "comment.created" {
		t.Fatalf("event name = %q, want comment.created", name)
	}

	// user-topic events arrive too.
	h.events.Publish(port.CollabEvent{Topic: "user:" + defaultUserID, Type: "mention", Payload: json.RawMessage(`{}`)})
	if ev, _ := readSSEEvent(t, reader); ev.Topic != "user:"+defaultUserID {
		t.Fatalf("want user-topic event, got %+v", ev)
	}

	// Client disconnect tears the subscription down (no goroutine leak;
	// srv.Close would hang below if the handler never returned).
	cancel()
	waitFor(t, "teardown", func() bool { return h.events.activeSubscribers() == 0 })
}

func TestCollabStreamTeamListErrorMapsThroughCodec(t *testing.T) {
	h := newHarness(t)
	h.teams.setErr(errors.New("boom"))
	rec := h.authed(http.MethodGet, "/v1/collab/stream", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if got := decodeErr(t, rec).Code; got != "internal" {
		t.Fatalf("error code = %q, want internal", got)
	}
	if h.events.activeSubscribers() != 0 {
		t.Fatal("subscription leaked after a pre-stream failure")
	}
}

// TestCollabStreamNilTeamsSubscribesUserTopicOnly proves the endpoint works
// before the team service is wired (Deps.Teams == nil): only user:<id>.
func TestCollabStreamNilTeamsSubscribesUserTopicOnly(t *testing.T) {
	h := newHarness(t)
	h.deps.Teams = nil
	srv := h.server()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled: the handler subscribes, writes headers, exits
	req := httptest.NewRequest(http.MethodGet, "/v1/collab/stream", nil).WithContext(
		context.WithValue(ctx, userCtxKey{}, domain.User{ID: defaultUserID}))
	rec := httptest.NewRecorder()
	srv.handleCollabStream(rec, req)

	want := [][]string{{"user:" + defaultUserID}}
	if topics := h.events.subscribedTopics(); !reflect.DeepEqual(topics, want) {
		t.Fatalf("subscribed topics = %v, want %v", topics, want)
	}
	if h.events.activeSubscribers() != 0 {
		t.Fatal("subscription not cancelled on handler exit")
	}
	if !strings.Contains(rec.Body.String(), ": connected") {
		t.Fatalf("missing initial comment, body = %q", rec.Body.String())
	}
}

// TestCollabStreamDropsRemovedTeamOnKeepaliveTick proves stream
// authorization is not frozen at connect: once the caller is removed from a
// team, the next keepalive tick re-resolves memberships, re-subscribes with
// the narrowed topic set, and that team's events stop arriving.
func TestCollabStreamDropsRemovedTeamOnKeepaliveTick(t *testing.T) {
	old := keepaliveInterval
	keepaliveInterval = 10 * time.Millisecond
	defer func() { keepaliveInterval = old }()

	h := newHarness(t)
	h.teams.setTeams([]domain.Team{{ID: "team_1", Name: "Ops"}})
	srv := httptest.NewServer(h.handler())
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/collab/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+defaultToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	reader := bufio.NewReader(resp.Body)

	// Team events flow while the caller is a member.
	h.events.Publish(port.CollabEvent{Topic: "team:team_1", Type: "comment.created", Payload: json.RawMessage(`{"id":"c1"}`)})
	if ev, _ := readSSEEvent(t, reader); ev.Topic != "team:team_1" {
		t.Fatalf("member did not receive team event: %+v", ev)
	}

	// Remove the caller from the team; a keepalive tick re-resolves and
	// re-subscribes without the team topic.
	h.teams.setTeams(nil)
	waitFor(t, "narrowed resubscription", func() bool {
		tops := h.events.subscribedTopics()
		last := tops[len(tops)-1]
		return len(last) == 1 && last[0] == "user:"+defaultUserID
	})

	// A team event published after removal must never arrive; the user
	// event published after it is the next delivered frame.
	h.events.Publish(port.CollabEvent{Topic: "team:team_1", Type: "comment.created", Payload: json.RawMessage(`{"secret":true}`)})
	h.events.Publish(port.CollabEvent{Topic: "user:" + defaultUserID, Type: "mention", Payload: json.RawMessage(`{}`)})
	ev, _ := readSSEEvent(t, reader)
	if ev.Topic != "user:"+defaultUserID {
		t.Fatalf("removed member still received team event: %+v", ev)
	}
	if got := h.events.activeSubscribers(); got != 1 {
		t.Fatalf("active subscriptions = %d, want 1 (old subscription dropped)", got)
	}
}

func TestCollabStreamKeepaliveTicks(t *testing.T) {
	old := keepaliveInterval
	keepaliveInterval = 10 * time.Millisecond
	defer func() { keepaliveInterval = old }()

	h := newHarness(t)
	srv := h.server()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/v1/collab/stream", nil).WithContext(
		context.WithValue(ctx, userCtxKey{}, domain.User{ID: defaultUserID}))
	rec := httptest.NewRecorder()
	srv.handleCollabStream(rec, req)

	if !strings.Contains(rec.Body.String(), ": keepalive\n\n") {
		t.Fatalf("no keepalive tick in body %q", rec.Body.String())
	}
}

// TestCollabStreamEndsOnDrain: closing Deps.Drain ends an open SSE stream
// so srv.Shutdown can complete (the client reconnects on its own).
func TestCollabStreamEndsOnDrain(t *testing.T) {
	h := newHarness(t)
	drain := make(chan struct{})
	h.deps.Drain = drain
	srv := httptest.NewServer(h.handler())
	defer srv.Close()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/v1/collab/stream", nil)
	req.Header.Set("Authorization", "Bearer "+defaultToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	waitFor(t, "subscription", func() bool { return h.events.activeSubscribers() == 1 })
	close(drain)
	done := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(resp.Body)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("stream ended with error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not end after drain")
	}
	waitFor(t, "unsubscribe", func() bool { return h.events.activeSubscribers() == 0 })
}
