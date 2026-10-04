package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- threads -------------------------------------------------------------

func (s *server) handleListThreads(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	q := port.ThreadQuery{
		AccountID: qs.Get("accountId"),
		LabelID:   qs.Get("labelId"),
		Query:     qs.Get("q"),
		Cursor:    qs.Get("cursor"),
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

func (s *server) handleBulkThreadActions(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ThreadIDs []string `json:"threadIds"`
		Action    string   `json:"action"`
		LabelID   string   `json:"labelId"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	var fc fieldCheck
	fc.list("threadIds", len(in.ThreadIDs))
	fc.title("labelId", in.LabelID)
	if err := fc.err(); err != nil {
		s.writeError(w, r, err)
		return
	}
	if in.Action == "label" || in.Action == "unlabel" {
		if in.LabelID == "" {
			s.writeError(w, r, fmt.Errorf("%w: labelId is required for label actions", domain.ErrValidation))
			return
		}
		res, err := s.deps.Mail.BulkSetLabel(r.Context(), userFrom(r).ID, in.ThreadIDs, in.LabelID, in.Action == "label")
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
		return
	}
	action, err := domain.ParseThreadAction(in.Action)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	res, err := s.deps.Mail.BulkActOnThreads(r.Context(), userFrom(r).ID, in.ThreadIDs, action)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// --- labels ----------------------------------------------------------------

func (s *server) handleListLabels(w http.ResponseWriter, r *http.Request) {
	labels, err := s.deps.Mail.ListLabels(r.Context(), userFrom(r).ID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, labels)
}

func (s *server) handleSetThreadLabel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		LabelID string `json:"labelId"`
		Add     bool   `json:"add"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	var fc fieldCheck
	fc.title("labelId", in.LabelID)
	if err := fc.err(); err != nil {
		s.writeError(w, r, err)
		return
	}
	if in.LabelID == "" {
		s.writeError(w, r, fmt.Errorf("%w: labelId is required", domain.ErrValidation))
		return
	}
	thread, err := s.deps.Mail.SetThreadLabel(r.Context(), userFrom(r).ID, r.PathValue("id"), in.LabelID, in.Add)
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

func (s *server) handleUnsnoozeThread(w http.ResponseWriter, r *http.Request) {
	thread, err := s.deps.Mail.UnsnoozeThread(r.Context(), userFrom(r).ID, r.PathValue("id"))
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

func (s *server) handleUnsubscribeThread(w http.ResponseWriter, r *http.Request) {
	res, err := s.deps.Mail.UnsubscribeThread(r.Context(), userFrom(r).ID, r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *server) handleGetMeToZero(w http.ResponseWriter, r *http.Request) {
	var in struct {
		OlderThan time.Time `json:"olderThan"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	if in.OlderThan.IsZero() {
		s.writeError(w, r, fmt.Errorf("%w: olderThan is required (RFC 3339)", domain.ErrValidation))
		return
	}
	count, err := s.deps.Mail.ArchiveOlderThan(r.Context(), userFrom(r).ID, in.OlderThan)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"archivedCount": count})
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

// checkDraftInput applies the field limits to a draft payload. bodyHtml is
// deliberately exempt: it carries inline images and is bounded by the
// 10 MiB route cap instead of the 64 KiB text limit.
func checkDraftInput(in port.DraftInput) error {
	var fc fieldCheck
	fc.title("subject", in.Subject)
	for _, f := range []struct {
		name string
		list []domain.EmailAddress
	}{{"to", in.To}, {"cc", in.Cc}, {"bcc", in.Bcc}} {
		fc.list(f.name, len(f.list))
		for _, a := range f.list {
			fc.email(f.name, a.Email)
		}
	}
	return fc.err()
}

func (s *server) handleCreateDraft(w http.ResponseWriter, r *http.Request) {
	var in port.DraftInput
	if err := decodeJSONLimit(w, r, &in, maxDraftBodyBytes); err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := checkDraftInput(in); err != nil {
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
	if err := decodeJSONLimit(w, r, &in, maxDraftBodyBytes); err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := checkDraftInput(in); err != nil {
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
	var fc fieldCheck
	fc.title("name", in.Name)
	fc.optTitle("shortcut", in.Shortcut)
	fc.text("bodyHtml", in.BodyHTML)
	if err := fc.err(); err != nil {
		s.writeError(w, r, err)
		return
	}
	// Snippet bodies are stored HTML rendered in composers — for TEAM
	// snippets (M2.7) in OTHER members' composers — so they are scrubbed at
	// the HTTP boundary like signatures (sanitize.go precedent).
	in.BodyHTML = sanitizeSignatureHTML(in.BodyHTML)
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
	var fc fieldCheck
	fc.title("name", in.Name)
	fc.optTitle("shortcut", in.Shortcut)
	fc.text("bodyHtml", in.BodyHTML)
	if err := fc.err(); err != nil {
		s.writeError(w, r, err)
		return
	}
	// Same sanitization boundary as handleCreateSnippet.
	in.BodyHTML = sanitizeSignatureHTML(in.BodyHTML)
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

// --- opens, smart send, attachments, contacts, reactions (M2.5) ----------

func (s *server) handleListOpens(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	cursor := qs.Get("cursor")
	limit := 0
	if v := qs.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			s.writeError(w, r, fmt.Errorf("%w: limit must be a positive integer", domain.ErrValidation))
			return
		}
		limit = n
	}
	page, err := s.deps.Mail.ListOpens(r.Context(), userFrom(r).ID, cursor, limit)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// handleSendSuggestion returns the Smart Send recommendation for a
// recipient. A 404 means "no suggestion yet" (thin history) — the client
// treats it as absence, not an error toast.
func (s *server) handleSendSuggestion(w http.ResponseWriter, r *http.Request) {
	email := r.URL.Query().Get("email")
	if email == "" {
		s.writeError(w, r, fmt.Errorf("%w: email is required", domain.ErrValidation))
		return
	}
	suggestion, err := s.deps.Mail.SuggestSendTime(r.Context(), userFrom(r).ID, email)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, suggestion)
}

func (s *server) handleSearchAttachments(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	q := port.AttachmentQuery{
		Query:    qs.Get("q"),
		Contact:  qs.Get("contact"),
		ThreadID: qs.Get("threadId"),
		Cursor:   qs.Get("cursor"),
	}
	if v := qs.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			s.writeError(w, r, fmt.Errorf("%w: limit must be a positive integer", domain.ErrValidation))
			return
		}
		q.Limit = n
	}
	page, err := s.deps.Mail.SearchAttachments(r.Context(), userFrom(r).ID, q)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// handleGetAttachmentContent streams the raw attachment bytes (no JSON
// envelope): Content-Type from the provider's reported mime type and an
// inline Content-Disposition so browsers preview rather than force-download.
// X-Content-Type-Options: nosniff stops the browser from MIME-sniffing an
// attachment's actual bytes into something more dangerous than the reported
// Content-Type (e.g. sniffing a mislabeled upload as text/html and executing
// it) — this endpoint serves arbitrary user-supplied attachment content.
func (s *server) handleGetAttachmentContent(w http.ResponseWriter, r *http.Request) {
	data, mimeType, filename, err := s.deps.Mail.GetAttachmentContent(r.Context(), userFrom(r).ID, r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Disposition", `inline; filename="`+escapeQuotedString(filename)+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// escapeQuotedString escapes backslashes and double quotes so filename can
// be safely embedded in a quoted-string HTTP header parameter (RFC 6266).
func escapeQuotedString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

func (s *server) handleGetContact(w http.ResponseWriter, r *http.Request) {
	contact, err := s.deps.Mail.GetContact(r.Context(), userFrom(r).ID, r.PathValue("email"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, contact)
}

func (s *server) handleReactToMessage(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Emoji     string `json:"emoji"`
		SendReply bool   `json:"sendReply"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	var fc fieldCheck
	fc.title("emoji", in.Emoji)
	if err := fc.err(); err != nil {
		s.writeError(w, r, err)
		return
	}
	if in.Emoji == "" {
		s.writeError(w, r, fmt.Errorf("%w: emoji is required", domain.ErrValidation))
		return
	}
	res, err := s.deps.Mail.ReactToMessage(r.Context(), userFrom(r).ID, r.PathValue("id"), in.Emoji, in.SendReply)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *server) handleRemoveReaction(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Mail.RemoveReaction(r.Context(), userFrom(r).ID, r.PathValue("id"), r.PathValue("emoji")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
