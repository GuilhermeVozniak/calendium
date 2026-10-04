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
	// WebURL is the public web app origin (PUBLIC_WEB_URL, no trailing
	// slash). Desktop builds its billing link from it
	// (<webUrl>/settings?tab=billing) instead of a hardcoded domain; the
	// mobile apps show no billing links (store rules).
	WebURL string `json:"webUrl"`
	// UndoSendSeconds is the undo-send grace window (UNDO_SEND_SECONDS,
	// default 15): clients show a post-send Undo affordance for this long.
	UndoSendSeconds int `json:"undoSendSeconds"`
	// VapidPublicKey is the Web Push application server key, present only when
	// web push is configured; clients subscribe the service worker with it.
	VapidPublicKey string           `json:"vapidPublicKey,omitempty"`
	Features       InstanceFeatures `json:"features"`
	// Capabilities advertises optional vendor integrations (M2.8): a vendor
	// flag is true only when its OAuth/API config is present in the
	// deployment, so clients never show connect UI that can only 501.
	Capabilities InstanceCapabilities `json:"capabilities"`
}

// InstanceFeatures reports which optional capabilities are wired on this
// deployment so clients can hide unavailable surfaces.
type InstanceFeatures struct {
	// Billing is true exactly when the deployment runs in cloud mode (Paddle wired).
	Billing   bool `json:"billing"`
	Google    bool `json:"google"`
	Microsoft bool `json:"microsoft"`
	AI        bool `json:"ai"`
	Push      bool `json:"push"`
	// Maps reports a configured maps provider (location autocomplete +
	// travel times, M2.8); false hides the affordances client-side.
	Maps bool `json:"maps"`
	// Email reports a configured SMTP sender (SMTP_HOST). false means the
	// web forgot-password page shows the administrator reset instructions
	// and team invitations fall back to copyable links.
	Email bool `json:"email"`
}

// InstanceCapabilities reports which optional vendor integrations are
// configured on this deployment (M2.8): todoist/hubspot per-user OAuth
// (Task 9), maps geocoding (Task 11), and weather (Task 13). Only vendors
// whose config is present are advertised; unwired vendors' endpoints
// answer 501.
type InstanceCapabilities struct {
	Todoist bool `json:"todoist"`
	HubSpot bool `json:"hubspot"`
	Maps    bool `json:"maps"`
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
