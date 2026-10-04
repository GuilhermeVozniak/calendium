package httpapi

import (
	"fmt"
	"net/http"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// handleGetPreferences serves the cross-device preference document (named
// theme, M2.6 Task 13).
func (s *server) handleGetPreferences(w http.ResponseWriter, r *http.Request) {
	prefs, err := s.deps.Users.GetPreferences(r.Context(), userFrom(r).ID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, prefs)
}

// handleUpdatePreferences validates the theme against the allowed set (400
// otherwise) and upserts the preference document.
func (s *server) handleUpdatePreferences(w http.ResponseWriter, r *http.Request) {
	var in port.UserPreferences
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	if !port.ValidTheme(in.Theme) {
		s.writeError(w, r, fmt.Errorf("%w: unknown theme %q", domain.ErrValidation, in.Theme))
		return
	}
	prefs, err := s.deps.Users.UpdatePreferences(r.Context(), userFrom(r).ID, in)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, prefs)
}

func (s *server) handleSearch(w http.ResponseWriter, r *http.Request) {
	result, err := s.deps.Search.Search(r.Context(), userFrom(r).ID, r.URL.Query().Get("q"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) handleAiCompose(w http.ResponseWriter, r *http.Request) {
	var req domain.AiComposeRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	var fc fieldCheck
	fc.text("prompt", req.Prompt)
	fc.title("tone", req.Tone)
	if err := fc.err(); err != nil {
		s.writeError(w, r, err)
		return
	}
	res, err := s.deps.AI.Compose(r.Context(), userFrom(r).ID, req)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *server) handleAiAsk(w http.ResponseWriter, r *http.Request) {
	var req domain.AiAskRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	var fc fieldCheck
	fc.text("question", req.Question)
	if err := fc.err(); err != nil {
		s.writeError(w, r, err)
		return
	}
	res, err := s.deps.AI.Ask(r.Context(), userFrom(r).ID, req)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *server) handleAiEventProposal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ThreadID string `json:"threadId"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	proposal, err := s.deps.AI.ProposeEvent(r.Context(), userFrom(r).ID, req.ThreadID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, proposal)
}

func (s *server) handleInstantReplies(w http.ResponseWriter, r *http.Request) {
	replies, err := s.deps.AI.InstantReplies(r.Context(), userFrom(r).ID, r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string][]string{"replies": replies})
}

func (s *server) handleRegisterDevice(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Platform string `json:"platform"`
		Token    string `json:"token"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	var fc fieldCheck
	fc.title("platform", in.Platform)
	// A Web Push token is the whole PushSubscription JSON, whose endpoint is
	// an opaque push-service URL (Edge/WNS channel URIs run to several
	// hundred %-escaped characters): opaque text, not a title.
	fc.text("token", in.Token)
	if err := fc.err(); err != nil {
		s.writeError(w, r, err)
		return
	}
	device, err := s.deps.Devices.Register(r.Context(), userFrom(r).ID,
		domain.DevicePlatform(in.Platform), in.Token)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, device)
}

func (s *server) handleUnregisterDevice(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Devices.Unregister(r.Context(), userFrom(r).ID, r.PathValue("id")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
