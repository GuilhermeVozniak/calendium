package httpapi

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"

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
	authURL, err := s.deps.Accounts.BeginConnect(
		r.Context(), userFrom(r).ID, provider, in.RedirectURL, requestBaseURL(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": authURL})
}

// handleSetVipSenders replaces the account's VIP-sender list.
func (s *server) handleSetVipSenders(w http.ResponseWriter, r *http.Request) {
	var in struct {
		VipSenders []string `json:"vipSenders"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	account, err := s.deps.Accounts.SetVipSenders(r.Context(), userFrom(r).ID, r.PathValue("id"), in.VipSenders)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, account)
}

// handleAccountCallback is the browser-facing OAuth redirect target the
// provider sends the code+state to (redirect_uri = this route). It is
// unauthenticated (the one-time state authenticates the flow); after the
// token exchange it 302s the browser back to the client's stored redirectUrl
// with ?status=connected|error. Only when the state itself is invalid — so no
// client redirect is known — does it render a static page.
func (s *server) handleAccountCallback(w http.ResponseWriter, r *http.Request) {
	provider, err := domain.ParseProvider(r.PathValue("provider"))
	if err != nil {
		writeCallbackPage(w, http.StatusBadRequest, "Connection failed", "Unknown provider.")
		return
	}
	q := r.URL.Query()
	_, redirect, cerr := s.deps.Accounts.CompleteConnect(
		r.Context(), provider, q.Get("state"), q.Get("code"), requestBaseURL(r))
	if cerr != nil {
		status, code := statusFor(cerr)
		if status == http.StatusInternalServerError {
			s.deps.Logger.Error("oauth callback failed", "provider", provider, "error", cerr)
		} else {
			s.deps.Logger.Info("oauth callback rejected", "provider", provider, "code", code, "error", cerr)
		}
		if redirect != "" {
			http.Redirect(w, r, withStatusParam(redirect, "error"), http.StatusFound)
			return
		}
		detail := safeMessage(code)
		if status == http.StatusInternalServerError {
			detail = "Something went wrong while connecting the account. Please try again."
		}
		writeCallbackPage(w, status, "Connection failed", detail)
		return
	}
	http.Redirect(w, r, withStatusParam(redirect, "connected"), http.StatusFound)
}

// requestBaseURL derives the API's public origin (scheme://host) from the
// incoming request, honoring the X-Forwarded-Proto/Host set by a reverse
// proxy. Used to build the provider redirect_uri when PUBLIC_API_URL is unset.
func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = strings.TrimSpace(strings.Split(p, ",")[0])
	}
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = strings.TrimSpace(strings.Split(h, ",")[0])
	}
	if host == "" {
		return ""
	}
	return scheme + "://" + host
}

// withStatusParam appends ?status=<status> to the client redirect URL.
func withStatusParam(redirect, status string) string {
	u, err := url.Parse(redirect)
	if err != nil {
		return redirect
	}
	qq := u.Query()
	qq.Set("status", status)
	u.RawQuery = qq.Encode()
	return u.String()
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
