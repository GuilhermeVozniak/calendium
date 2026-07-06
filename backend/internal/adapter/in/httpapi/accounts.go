package httpapi

import (
	"fmt"
	"html"
	"net/http"

	"calendium/backend/internal/domain"
)

func (s *server) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := s.deps.Accounts.List(r.Context(), userFrom(r).ID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, accounts)
}

func (s *server) handleConnectAccount(w http.ResponseWriter, r *http.Request) {
	provider, err := domain.ParseProvider(r.PathValue("provider"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var in struct {
		RedirectURL string `json:"redirectUrl"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	url, err := s.deps.Accounts.BeginConnect(r.Context(), userFrom(r).ID, provider, in.RedirectURL)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

// handleAccountCallback is the browser-facing OAuth redirect target; it is
// unauthenticated (the one-time state parameter authenticates the flow) and
// renders a small human-readable page instead of JSON.
func (s *server) handleAccountCallback(w http.ResponseWriter, r *http.Request) {
	provider, err := domain.ParseProvider(r.PathValue("provider"))
	if err != nil {
		writeCallbackPage(w, http.StatusBadRequest, "Connection failed", "Unknown provider.")
		return
	}
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		detail := e
		if d := q.Get("error_description"); d != "" {
			detail += ": " + d
		}
		writeCallbackPage(w, http.StatusBadRequest, "Connection failed", detail)
		return
	}
	account, err := s.deps.Accounts.CompleteConnect(r.Context(), provider, q.Get("state"), q.Get("code"))
	if err != nil {
		status, code := statusFor(err)
		detail := safeMessage(code)
		if status == http.StatusInternalServerError {
			s.deps.Logger.Error("oauth callback failed", "provider", provider, "error", err)
			detail = "Something went wrong while connecting the account. Please try again."
		} else {
			s.deps.Logger.Info("oauth callback rejected", "provider", provider, "code", code, "error", err)
		}
		writeCallbackPage(w, status, "Connection failed", detail)
		return
	}
	writeCallbackPage(w, http.StatusOK, "Account connected",
		fmt.Sprintf("%s is connected and syncing. You can close this window and return to Calendium.", account.Email))
}

func (s *server) handleDisconnectAccount(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Accounts.Disconnect(r.Context(), userFrom(r).ID, r.PathValue("id")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeCallbackPage renders the minimal OAuth-callback page, themed to the
// shadcn new-york neutral palette in light and dark.
func writeCallbackPage(w http.ResponseWriter, status int, title, detail string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%[1]s · Calendium</title>
<style>
  :root { --background: hsl(0 0%% 100%%); --foreground: hsl(0 0%% 3.9%%); --muted: hsl(0 0%% 45.1%%); --border: hsl(0 0%% 89.8%%); }
  @media (prefers-color-scheme: dark) {
    :root { --background: hsl(0 0%% 3.9%%); --foreground: hsl(0 0%% 98%%); --muted: hsl(0 0%% 63.9%%); --border: hsl(0 0%% 14.9%%); }
  }
  body { margin: 0; min-height: 100vh; display: grid; place-items: center;
         background: var(--background); color: var(--foreground);
         font: 15px/1.6 -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
  main { max-width: 22rem; padding: 2rem; border: 1px solid var(--border);
         border-radius: 0.625rem; text-align: center; }
  h1 { font-size: 1.125rem; margin: 0 0 0.5rem; }
  p  { margin: 0; color: var(--muted); }
</style>
</head>
<body><main><h1>%[1]s</h1><p>%[2]s</p></main></body>
</html>`, html.EscapeString(title), html.EscapeString(detail))
}
