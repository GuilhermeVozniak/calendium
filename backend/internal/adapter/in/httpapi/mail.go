package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- threads -------------------------------------------------------------

func (s *server) handleListThreads(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	q := port.ThreadQuery{
		LabelID: qs.Get("labelId"),
		Query:   qs.Get("q"),
		Cursor:  qs.Get("cursor"),
	}
	if v := qs.Get("split"); v != "" {
		split, err := domain.ParseInboxSplit(v)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		q.Split = split
	}
	if v := qs.Get("view"); v != "" {
		view, err := domain.ParseThreadView(v)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		q.View = view
	}
	if v := qs.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			s.writeError(w, r, fmt.Errorf("%w: limit must be a positive integer", domain.ErrValidation))
			return
		}
		q.Limit = n
	}
	page, err := s.deps.Mail.ListThreads(r.Context(), userFrom(r).ID, q)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *server) handleGetThread(w http.ResponseWriter, r *http.Request) {
	thread, messages, err := s.deps.Mail.GetThread(r.Context(), userFrom(r).ID, r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"thread": thread, "messages": messages})
}

func (s *server) handleThreadAction(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Action string `json:"action"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	action, err := domain.ParseThreadAction(in.Action)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	thread, err := s.deps.Mail.ActOnThread(r.Context(), userFrom(r).ID, r.PathValue("id"), action)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, thread)
}

func (s *server) handleMarkThreadOpened(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Mail.MarkThreadOpened(r.Context(), userFrom(r).ID, r.PathValue("id")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleSnoozeThread(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Until time.Time `json:"until"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	if in.Until.IsZero() {
		s.writeError(w, r, fmt.Errorf("%w: until is required (RFC 3339)", domain.ErrValidation))
		return
	}
	thread, err := s.deps.Mail.SnoozeThread(r.Context(), userFrom(r).ID, r.PathValue("id"), in.Until)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, thread)
}

func (s *server) handleThreadReminder(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RemindAt *time.Time `json:"remindAt"` // null clears the reminder
	}
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	thread, err := s.deps.Mail.SetReminder(r.Context(), userFrom(r).ID, r.PathValue("id"), in.RemindAt)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, thread)
}

// --- drafts --------------------------------------------------------------

func (s *server) handleListDrafts(w http.ResponseWriter, r *http.Request) {
	drafts, err := s.deps.Mail.ListDrafts(r.Context(), userFrom(r).ID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, drafts)
}

func (s *server) handleCreateDraft(w http.ResponseWriter, r *http.Request) {
	var in port.DraftInput
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	draft, err := s.deps.Mail.CreateDraft(r.Context(), userFrom(r).ID, in)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, draft)
}

func (s *server) handleGetDraft(w http.ResponseWriter, r *http.Request) {
	draft, err := s.deps.Mail.GetDraft(r.Context(), userFrom(r).ID, r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, draft)
}

func (s *server) handleUpdateDraft(w http.ResponseWriter, r *http.Request) {
	var in port.DraftInput
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	draft, err := s.deps.Mail.UpdateDraft(r.Context(), userFrom(r).ID, r.PathValue("id"), in)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, draft)
}

func (s *server) handleDeleteDraft(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Mail.DeleteDraft(r.Context(), userFrom(r).ID, r.PathValue("id")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleSendDraft(w http.ResponseWriter, r *http.Request) {
	msg, err := s.deps.Mail.SendDraft(r.Context(), userFrom(r).ID, r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, msg)
}

func (s *server) handleUnsendDraft(w http.ResponseWriter, r *http.Request) {
	draft, err := s.deps.Mail.UnsendDraft(r.Context(), userFrom(r).ID, r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, draft)
}

// --- snippets ------------------------------------------------------------

func (s *server) handleListSnippets(w http.ResponseWriter, r *http.Request) {
	snippets, err := s.deps.Mail.ListSnippets(r.Context(), userFrom(r).ID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, snippets)
}

func (s *server) handleCreateSnippet(w http.ResponseWriter, r *http.Request) {
	var in port.SnippetInput
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	snippet, err := s.deps.Mail.CreateSnippet(r.Context(), userFrom(r).ID, in)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, snippet)
}

func (s *server) handleUpdateSnippet(w http.ResponseWriter, r *http.Request) {
	var in port.SnippetInput
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	snippet, err := s.deps.Mail.UpdateSnippet(r.Context(), userFrom(r).ID, r.PathValue("id"), in)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, snippet)
}

func (s *server) handleDeleteSnippet(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Mail.DeleteSnippet(r.Context(), userFrom(r).ID, r.PathValue("id")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
