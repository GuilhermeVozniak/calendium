package httpapi

import (
	"net/http"

	"calendium/backend/internal/domain"
)

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
	res, err := s.deps.AI.Compose(r.Context(), userFrom(r).ID, req)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
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
