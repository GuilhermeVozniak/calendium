package httpapi

import "net/http"

// Version is the running API build, surfaced to clients via GET /v1/instance.
const Version = "0.1.0"

// InstanceInfo is the public self-configuration document served at
// GET /v1/instance (unauthenticated). A client that only knows the server base
// URL fetches this to discover the Supabase credentials to authenticate with
// and which capabilities the deployment has enabled. It mirrors InstanceInfo in
// packages/shared/src/types.ts — keep the two in sync.
type InstanceInfo struct {
	Name string `json:"name"`
	// Mode is "self_host" for a free self-hosted deployment or "cloud" for
	// the managed offering.
	Mode    string `json:"mode"`
	Version string `json:"version"`
	// SupabaseURL / SupabaseAnonKey are the PUBLIC Supabase project URL and
	// anon key; either may be "" when not configured.
	SupabaseURL     string           `json:"supabaseUrl"`
	SupabaseAnonKey string           `json:"supabaseAnonKey"`
	Features        InstanceFeatures `json:"features"`
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

// Instance mode string values.
const (
	ModeSelfHost = "self_host"
	ModeCloud    = "cloud"
)

// handleInstance serves the composition-root-built discovery document verbatim.
func (s *server) handleInstance(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.deps.Instance)
}
