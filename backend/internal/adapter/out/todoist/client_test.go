package todoist

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

// recordedRequest captures one request the fake Todoist saw.
type recordedRequest struct {
	auth string
	form url.Values
}

// newTestClient points a Client at an httptest fake Todoist. Every request's
// auth header and form body are recorded before handler runs.
func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *[]recordedRequest) {
	t.Helper()
	reqs := &[]recordedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		*reqs = append(*reqs, recordedRequest{auth: r.Header.Get("Authorization"), form: r.PostForm})
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.Client())
	c.BaseURL = srv.URL
	return c, reqs
}

// syncCommand is the decoded shape of one posted Sync v9 command.
type syncCommand struct {
	Type string `json:"type"`
	UUID string `json:"uuid"`
	Args struct {
		ID string `json:"id"`
	} `json:"args"`
}

func decodeCommands(t *testing.T, form url.Values) []syncCommand {
	t.Helper()
	var cmds []syncCommand
	if err := json.Unmarshal([]byte(form.Get("commands")), &cmds); err != nil {
		t.Fatalf("decode commands %q: %v", form.Get("commands"), err)
	}
	return cmds
}

// ackCommands answers every posted command with sync_status "ok".
func ackCommands(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var cmds []syncCommand
		if err := json.Unmarshal([]byte(r.PostForm.Get("commands")), &cmds); err != nil {
			t.Errorf("decode commands: %v", err)
		}
		statuses := map[string]string{}
		for _, cmd := range cmds {
			statuses[cmd.UUID] = "ok"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"sync_token": "tok", "sync_status": statuses})
	}
}

func TestSyncTasksFullSync(t *testing.T) {
	c, reqs := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"sync_token": "tok-1",
			"full_sync": true,
			"items": [
				{"id": "100", "content": "Ship the brief", "description": "with appendix", "checked": false, "is_deleted": false, "due": {"date": "2026-07-21T17:00:00Z"}},
				{"id": "101", "content": "Buy milk", "description": "", "checked": false, "is_deleted": false, "due": {"date": "2026-07-22"}},
				{"id": "102", "content": "No due", "checked": false, "is_deleted": false},
				{"id": "103", "content": "Done upstream", "checked": true, "is_deleted": false},
				{"id": "104", "content": "Deleted upstream", "checked": false, "is_deleted": true}
			]
		}`))
	})

	page, err := c.SyncTasks(context.Background(), "access-token", "")
	if err != nil {
		t.Fatalf("SyncTasks() error = %v", err)
	}

	req := (*reqs)[0]
	if req.auth != "Bearer access-token" {
		t.Errorf("Authorization = %q, want Bearer access-token", req.auth)
	}
	if got := req.form.Get("sync_token"); got != "*" {
		t.Errorf("sync_token = %q, want * (full sync)", got)
	}
	if got := req.form.Get("resource_types"); got != `["items"]` {
		t.Errorf("resource_types = %q, want [\"items\"]", got)
	}

	if page.NextCursor != "tok-1" {
		t.Errorf("NextCursor = %q, want tok-1", page.NextCursor)
	}
	if page.HasMore {
		t.Error("HasMore = true, want false (Sync v9 is single-page)")
	}
	if len(page.Tasks) != 3 {
		t.Fatalf("len(Tasks) = %d, want 3", len(page.Tasks))
	}

	first := page.Tasks[0]
	if first.Title != "Ship the brief" || first.ExternalID != "100" || first.Source != domain.TaskSourceTodoist {
		t.Errorf("task[0] = %+v, want content/id/source mapped", first)
	}
	if first.Notes == nil || *first.Notes != "with appendix" {
		t.Errorf("task[0].Notes = %v, want with appendix", first.Notes)
	}
	wantDue := time.Date(2026, 7, 21, 17, 0, 0, 0, time.UTC)
	if first.Due == nil || !first.Due.Equal(wantDue) || first.AllDayDue {
		t.Errorf("task[0] due = %v allDay=%v, want %v allDay=false", first.Due, first.AllDayDue, wantDue)
	}
	if first.SourceURL == nil || *first.SourceURL != "https://todoist.com/showTask?id=100" {
		t.Errorf("task[0].SourceURL = %v, want item URL", first.SourceURL)
	}

	second := page.Tasks[1]
	wantDay := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	if second.Due == nil || !second.Due.Equal(wantDay) || !second.AllDayDue {
		t.Errorf("task[1] due = %v allDay=%v, want %v allDay=true (date-only)", second.Due, second.AllDayDue, wantDay)
	}
	if second.Notes != nil {
		t.Errorf("task[1].Notes = %v, want nil (empty description)", second.Notes)
	}

	if third := page.Tasks[2]; third.Due != nil || third.AllDayDue {
		t.Errorf("task[2] due = %v allDay=%v, want nil/false", third.Due, third.AllDayDue)
	}

	if len(page.DeletedIDs) != 2 || page.DeletedIDs[0] != "103" || page.DeletedIDs[1] != "104" {
		t.Errorf("DeletedIDs = %v, want [103 104] (checked + is_deleted)", page.DeletedIDs)
	}
}

func TestSyncTasksCursorRoundTrip(t *testing.T) {
	c, reqs := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"sync_token": "cur-2", "items": []}`))
	})
	page, err := c.SyncTasks(context.Background(), "tok", "cur-1")
	if err != nil {
		t.Fatalf("SyncTasks() error = %v", err)
	}
	if got := (*reqs)[0].form.Get("sync_token"); got != "cur-1" {
		t.Errorf("sync_token sent = %q, want cur-1", got)
	}
	if page.NextCursor != "cur-2" {
		t.Errorf("NextCursor = %q, want cur-2", page.NextCursor)
	}
	if len(page.Tasks) != 0 || len(page.DeletedIDs) != 0 {
		t.Errorf("empty delta returned tasks=%v deleted=%v", page.Tasks, page.DeletedIDs)
	}
}

func TestCompleteAndReopenCommands(t *testing.T) {
	c, reqs := newTestClient(t, ackCommands(t))
	ctx := context.Background()

	if err := c.CompleteTask(ctx, "tok", "42"); err != nil {
		t.Fatalf("CompleteTask() error = %v", err)
	}
	cmds := decodeCommands(t, (*reqs)[0].form)
	if len(cmds) != 1 || cmds[0].Type != "item_complete" || cmds[0].Args.ID != "42" {
		t.Errorf("complete commands = %+v, want one item_complete for 42", cmds)
	}
	if cmds[0].UUID == "" {
		t.Error("complete command has no uuid")
	}
	if (*reqs)[0].auth != "Bearer tok" {
		t.Errorf("Authorization = %q, want Bearer tok", (*reqs)[0].auth)
	}

	if err := c.ReopenTask(ctx, "tok", "42"); err != nil {
		t.Fatalf("ReopenTask() error = %v", err)
	}
	cmds = decodeCommands(t, (*reqs)[1].form)
	if len(cmds) != 1 || cmds[0].Type != "item_uncomplete" || cmds[0].Args.ID != "42" {
		t.Errorf("reopen commands = %+v, want one item_uncomplete for 42", cmds)
	}
}

func TestCommandRejectedByVendor(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var cmds []syncCommand
		_ = json.Unmarshal([]byte(r.PostForm.Get("commands")), &cmds)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sync_token": "tok",
			"sync_status": map[string]any{
				cmds[0].UUID: map[string]any{"error": "ITEM_NOT_FOUND", "error_code": 20},
			},
		})
	})
	err := c.CompleteTask(context.Background(), "tok", "gone")
	if err == nil || !strings.Contains(err.Error(), "ITEM_NOT_FOUND") {
		t.Fatalf("CompleteTask() error = %v, want vendor error surfaced", err)
	}
}

func TestCommandMissingSyncStatus(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"sync_token": "tok"}`))
	})
	if err := c.CompleteTask(context.Background(), "tok", "42"); err == nil {
		t.Fatal("CompleteTask() = nil, want error when sync_status is absent")
	}
}

func TestVendorErrorStatuses(t *testing.T) {
	ctx := context.Background()

	t.Run("401 maps to ErrUnauthorized", func(t *testing.T) {
		c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error": "Unauthorized"}`))
		})
		if _, err := c.SyncTasks(ctx, "stale", ""); !errors.Is(err, domain.ErrUnauthorized) {
			t.Errorf("SyncTasks() err = %v, want ErrUnauthorized", err)
		}
		if err := c.CompleteTask(ctx, "stale", "1"); !errors.Is(err, domain.ErrUnauthorized) {
			t.Errorf("CompleteTask() err = %v, want ErrUnauthorized", err)
		}
	})

	t.Run("429 is an error but not ErrUnauthorized", func(t *testing.T) {
		c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error": "Too many requests"}`))
		})
		_, err := c.SyncTasks(ctx, "tok", "")
		if err == nil || errors.Is(err, domain.ErrUnauthorized) {
			t.Errorf("SyncTasks() err = %v, want plain rate-limit error", err)
		}
		if !strings.Contains(err.Error(), "429") {
			t.Errorf("error %q does not mention 429", err)
		}
	})

	t.Run("500 surfaces status and body", func(t *testing.T) {
		c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`upstream exploded`))
		})
		_, err := c.SyncTasks(ctx, "tok", "")
		if err == nil || !strings.Contains(err.Error(), "500") {
			t.Errorf("SyncTasks() err = %v, want HTTP 500 error", err)
		}
	})

	t.Run("malformed 200 body is an error", func(t *testing.T) {
		c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{not json`))
		})
		if _, err := c.SyncTasks(ctx, "tok", ""); err == nil {
			t.Error("SyncTasks() = nil, want decode error")
		}
		if err := c.CompleteTask(ctx, "tok", "1"); err == nil {
			t.Error("CompleteTask() = nil, want decode error")
		}
	})
}
