package httpapi

import "net/http"

// Version is the running API build, surfaced to clients via GET /v1/instance.
const Version = "0.1.0"

// InstanceInfo is the public self-configuration document served at
// GET /v1/instance (unauthenticated). A client that only knows the server base
// URL fetches this to discover the Better Auth base URL to authenticate
// against and which capabilities the deployment has enabled. It mirrors
// InstanceInfo in packages/shared/src/types.ts — keep the two in sync.
type InstanceInfo struct {
	Name string `json:"name"`
	// Mode is "self_host" for a free self-hosted deployment or "cloud" for
	// the managed offering.
	Mode    string `json:"mode"`
	Version string `json:"version"`
	// AuthBaseURL is the Better Auth base URL clients build their auth client
	// against, ${PublicWebURL}/api/auth.
	AuthBaseURL string `json:"authBaseUrl"`
	// AuthProviders lists enabled sign-in methods, always including "email"
	// plus "google"/"apple" when their credentials are configured.
	AuthProviders []string `json:"authProviders"`
	// UndoSendSeconds is the undo-send grace window (UNDO_SEND_SECONDS,
	// default 15): clients show a post-send Undo affordance for this long.
	UndoSendSeconds int `json:"undoSendSeconds"`
	// VapidPublicKey is the Web Push application server key, present only when
	// web push is configured; clients subscribe the service worker with it.
	VapidPublicKey string           `json:"vapidPublicKey,omitempty"`
	Features       InstanceFeatures `json:"features"`
	// Capabilities lists optional third-party integrations (M2.8) so
	// clients can hide unavailable surfaces without probing endpoints.
	Capabilities InstanceCapabilities `json:"capabilities"`
}

// InstanceFeatures reports which optional capabilities are wired on this
// deployment so clients can hide unavailable surfaces.
type InstanceFeatures struct {
	Billing   bool `json:"billing"`
	Google    bool `json:"google"`
	Microsoft bool `json:"microsoft"`
	AI        bool `json:"ai"`
	Push      bool `json:"push"`
}

// InstanceCapabilities reports which optional third-party integrations
// (M2.8) are wired on this deployment. Mirrors the `capabilities` block of
// InstanceInfo in packages/shared/src/types.ts — keep the two in sync.
type InstanceCapabilities struct {
	// Weather is true when an Open-Meteo base URL is configured and
	// GET /v1/weather is served (M2.8 Task 13).
	Weather bool `json:"weather"`
}

// Instance mode string values.
const (
	ModeSelfHost = "self_host"
	ModeCloud    = "cloud"
)

// handleInstance serves the composition-root-built discovery document verbatim.
func (s *server) handleInstance(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.deps.Instance)
}
