package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// Task handlers (M2.8): thin JSON ↔ port.TaskService translation. All
// authorization (entitlement + user-scoped ownership) lives in the service;
// errors pass straight through the codec.

func (s *server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	var q port.TaskQuery

	// ?from&to — calendar-grid scheduled-overlap window (both required
	// together, RFC 3339).
	if qs.Get("from") != "" || qs.Get("to") != "" {
		from, to, err := timeRange(qs.Get("from"), qs.Get("to"))
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		q.ScheduledFrom, q.ScheduledTo = from, to
	}
	// ?dueFrom&dueTo — due-date range; each bound is independently optional.
	for _, bound := range []struct {
		key string
		dst *time.Time
	}{{"dueFrom", &q.DueFrom}, {"dueTo", &q.DueTo}} {
		if v := qs.Get(bound.key); v != "" {
			ts, err := time.Parse(time.RFC3339, v)
			if err != nil {
				s.writeError(w, r, fmt.Errorf("%w: `%s` must be an RFC 3339 timestamp", domain.ErrValidation, bound.key))
				return
			}
			*bound.dst = ts
		}
	}
	q.UnscheduledOnly = boolParam(qs.Get("unscheduled"))
	q.IncludeCompleted = boolParam(qs.Get("includeCompleted"))

	tasks, err := s.deps.Tasks.ListTasks(r.Context(), userFrom(r).ID, q)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, tasks)
}

// boolParam accepts the "1" and "true" spellings for flag query params.
func boolParam(v string) bool { return v == "1" || v == "true" }

func (s *server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var in domain.TaskInput
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	var fc fieldCheck
	fc.title("title", in.Title)
	fc.text("notes", in.Notes)
	if err := fc.err(); err != nil {
		s.writeError(w, r, err)
		return
	}
	task, err := s.deps.Tasks.CreateTask(r.Context(), userFrom(r).ID, in)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

// decodeTaskPatch decodes a PATCH /v1/tasks/{id} body preserving the
// absent-vs-null distinction: absent keys stay nil, explicit `null` becomes
// a non-nil outer pointer with nil inner (clear) via optionalField.
func decodeTaskPatch(w http.ResponseWriter, r *http.Request) (domain.TaskPatch, error) {
	var raw map[string]json.RawMessage
	if err := decodeJSON(w, r, &raw); err != nil {
		return domain.TaskPatch{}, err
	}
	var p domain.TaskPatch
	// Single-pointer fields: null and absent both mean "leave unchanged".
	simple := func(key string, dst any) error {
		msg, ok := raw[key]
		if !ok {
			return nil
		}
		if err := json.Unmarshal(msg, dst); err != nil {
			return fmt.Errorf("%w: invalid %q: %v", domain.ErrValidation, key, err)
		}
		return nil
	}
	if err := simple("title", &p.Title); err != nil {
		return domain.TaskPatch{}, err
	}
	if err := simple("allDayDue", &p.AllDayDue); err != nil {
		return domain.TaskPatch{}, err
	}
	if err := simple("position", &p.Position); err != nil {
		return domain.TaskPatch{}, err
	}
	var err error
	if p.Notes, err = optionalField[string](raw, "notes"); err != nil {
		return domain.TaskPatch{}, err
	}
	if p.Due, err = optionalField[time.Time](raw, "due"); err != nil {
		return domain.TaskPatch{}, err
	}
	if p.ScheduledStart, err = optionalField[time.Time](raw, "scheduledStart"); err != nil {
		return domain.TaskPatch{}, err
	}
	if p.ScheduledEnd, err = optionalField[time.Time](raw, "scheduledEnd"); err != nil {
		return domain.TaskPatch{}, err
	}
	return p, nil
}

func (s *server) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	patch, err := decodeTaskPatch(w, r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var fc fieldCheck
	fc.optTitle("title", patch.Title)
	if patch.Notes != nil {
		fc.optText("notes", *patch.Notes)
	}
	if err := fc.err(); err != nil {
		s.writeError(w, r, err)
		return
	}
	task, err := s.deps.Tasks.UpdateTask(r.Context(), userFrom(r).ID, r.PathValue("id"), patch)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *server) handleCompleteTask(w http.ResponseWriter, r *http.Request) {
	task, err := s.deps.Tasks.CompleteTask(r.Context(), userFrom(r).ID, r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *server) handleReopenTask(w http.ResponseWriter, r *http.Request) {
	task, err := s.deps.Tasks.ReopenTask(r.Context(), userFrom(r).ID, r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *server) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Tasks.DeleteTask(r.Context(), userFrom(r).ID, r.PathValue("id")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
