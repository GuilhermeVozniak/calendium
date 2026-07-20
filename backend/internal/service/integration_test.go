package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

var integrationTestBase = time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

type integrationHarness struct {
	svc    *IntegrationService
	repo   *fakeIntegrationRepo
	states *fakeOAuthStateRepo
	gw     *fakeOAuthGateway
	clock  *fakeClock
	purged []string // userIDs the todoist purge hook was called with
}

func newIntegrationHarness(vendors ...domain.IntegrationVendor) *integrationHarness {
	h := &integrationHarness{
		repo:   newIntegrationRepo(),
		states: newOAuthStateRepo(),
		gw:     newOAuthGateway(),
		clock:  newClock(integrationTestBase),
	}
	oauth := map[domain.IntegrationVendor]port.OAuthGateway{}
	for _, v := range vendors {
		oauth[v] = h.gw
	}
	h.svc = NewIntegrationService(
		h.repo, h.states, oauth,
		[]string{"http://localhost", "calendium://"},
		"https://api.example.com",
		func(_ context.Context, userID string) error {
			h.purged = append(h.purged, userID)
			return nil
		},
		h.clock,
	)
	return h
}

func TestIntegrationBeginConnect(t *testing.T) {
	ctx := context.Background()

	t.Run("returns the vendor auth URL carrying a stored state", func(t *testing.T) {
		h := newIntegrationHarness(domain.IntegrationTodoist)
		url, err := h.svc.BeginConnect(ctx, "u1", domain.IntegrationTodoist, "http://localhost:3000/settings", "")
		if err != nil {
			t.Fatalf("BeginConnect: %v", err)
		}
		if h.states.created == nil {
			t.Fatal("no oauth state was created")
		}
		st := *h.states.created
		if !strings.Contains(url, st.State) {
			t.Fatalf("auth URL %q does not carry the state %q", url, st.State)
		}
		if st.UserID != "u1" {
			t.Fatalf("state.UserID = %q, want u1", st.UserID)
		}
		if st.Provider != domain.Provider("integration:todoist") {
			t.Fatalf("state.Provider = %q, want integration:todoist (vendor-prefixed)", st.Provider)
		}
		if st.RedirectURL != "http://localhost:3000/settings" {
			t.Fatalf("state.RedirectURL = %q", st.RedirectURL)
		}
		if want := integrationTestBase.Add(10 * time.Minute); !st.ExpiresAt.Equal(want) {
			t.Fatalf("state.ExpiresAt = %v, want %v", st.ExpiresAt, want)
		}
		if h.gw.authChallenge != "" {
			t.Fatalf("vendor OAuth must pass an empty PKCE challenge, got %q", h.gw.authChallenge)
		}
		if want := "https://api.example.com/v1/integrations/callback/todoist"; h.gw.authRedirect != want {
			t.Fatalf("redirect_uri = %q, want %q", h.gw.authRedirect, want)
		}
	})

	t.Run("disallowed redirect rejected", func(t *testing.T) {
		h := newIntegrationHarness(domain.IntegrationTodoist)
		_, err := h.svc.BeginConnect(ctx, "u1", domain.IntegrationTodoist, "https://evil.example.com/phish", "")
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("error = %v, want ErrValidation", err)
		}
	})

	t.Run("missing redirect rejected", func(t *testing.T) {
		h := newIntegrationHarness(domain.IntegrationTodoist)
		if _, err := h.svc.BeginConnect(ctx, "u1", domain.IntegrationTodoist, "", ""); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("error = %v, want ErrValidation", err)
		}
	})

	t.Run("unconfigured vendor is 501 not-implemented", func(t *testing.T) {
		h := newIntegrationHarness(domain.IntegrationTodoist) // hubspot NOT wired
		_, err := h.svc.BeginConnect(ctx, "u1", domain.IntegrationHubSpot, "http://localhost:3000/settings", "")
		if !errors.Is(err, domain.ErrNotImplemented) {
			t.Fatalf("error = %v, want ErrNotImplemented", err)
		}
	})
}

func TestIntegrationCompleteConnect(t *testing.T) {
	ctx := context.Background()

	begin := func(t *testing.T, h *integrationHarness, vendor domain.IntegrationVendor) string {
		t.Helper()
		if _, err := h.svc.BeginConnect(ctx, "u1", vendor, "http://localhost:3000/settings", ""); err != nil {
			t.Fatalf("BeginConnect: %v", err)
		}
		return h.states.created.State
	}

	t.Run("creates the connection and stores tokens", func(t *testing.T) {
		h := newIntegrationHarness(domain.IntegrationHubSpot)
		h.gw.token = port.OAuthToken{
			TokenSet: port.TokenSet{AccessToken: "at-1", RefreshToken: "rt-1", ExpiresAt: integrationTestBase.Add(time.Hour)},
			Email:    "portal-user@example.com",
		}
		state := begin(t, h, domain.IntegrationHubSpot)

		conn, redirect, err := h.svc.CompleteConnect(ctx, domain.IntegrationHubSpot, state, "code-1", "")
		if err != nil {
			t.Fatalf("CompleteConnect: %v", err)
		}
		if redirect != "http://localhost:3000/settings" {
			t.Fatalf("clientRedirect = %q", redirect)
		}
		if conn.UserID != "u1" || conn.Vendor != domain.IntegrationHubSpot {
			t.Fatalf("conn = %+v", conn)
		}
		if conn.Status != domain.IntegrationStatusActive {
			t.Fatalf("status = %q, want active", conn.Status)
		}
		if conn.ExternalAccount != "portal-user@example.com" {
			t.Fatalf("externalAccount = %q", conn.ExternalAccount)
		}
		got, err := h.repo.GetTokens(ctx, conn.ID)
		if err != nil {
			t.Fatalf("GetTokens: %v", err)
		}
		if got.AccessToken != "at-1" || got.RefreshToken != "rt-1" {
			t.Fatalf("stored tokens = %+v", got)
		}
		if h.gw.exchVerifier != "" {
			t.Fatalf("vendor exchange must replay an empty PKCE verifier, got %q", h.gw.exchVerifier)
		}
	})

	t.Run("reconnect upserts instead of duplicating", func(t *testing.T) {
		h := newIntegrationHarness(domain.IntegrationTodoist)
		h.gw.token = port.OAuthToken{TokenSet: port.TokenSet{AccessToken: "at-1"}}
		state := begin(t, h, domain.IntegrationTodoist)
		first, _, err := h.svc.CompleteConnect(ctx, domain.IntegrationTodoist, state, "code-1", "")
		if err != nil {
			t.Fatalf("first CompleteConnect: %v", err)
		}
		// Simulate a broken connection, then reconnect.
		broken := first
		broken.Status = domain.IntegrationStatusError
		msg := "sync exploded"
		broken.LastError = &msg
		if err := h.repo.Update(ctx, broken); err != nil {
			t.Fatal(err)
		}

		h.gw.token = port.OAuthToken{TokenSet: port.TokenSet{AccessToken: "at-2"}}
		state = begin(t, h, domain.IntegrationTodoist)
		second, _, err := h.svc.CompleteConnect(ctx, domain.IntegrationTodoist, state, "code-2", "")
		if err != nil {
			t.Fatalf("second CompleteConnect: %v", err)
		}
		if second.ID != first.ID {
			t.Fatalf("reconnect created a new row: %q != %q", second.ID, first.ID)
		}
		if len(h.repo.created) != 1 {
			t.Fatalf("created %d rows, want 1", len(h.repo.created))
		}
		if second.Status != domain.IntegrationStatusActive || second.LastError != nil {
			t.Fatalf("reconnect did not reset health: %+v", second)
		}
		if tok, _ := h.repo.GetTokens(ctx, first.ID); tok.AccessToken != "at-2" {
			t.Fatalf("tokens not refreshed: %+v", tok)
		}
	})

	t.Run("forged state fails closed with ErrNotFound and no redirect", func(t *testing.T) {
		h := newIntegrationHarness(domain.IntegrationTodoist)
		_, redirect, err := h.svc.CompleteConnect(ctx, domain.IntegrationTodoist, "forged-state", "code", "")
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("error = %v, want ErrNotFound", err)
		}
		if redirect != "" {
			t.Fatalf("forged state must not yield a client redirect, got %q", redirect)
		}
		if len(h.repo.created) != 0 {
			t.Fatal("forged state must not create a connection")
		}
	})

	t.Run("state minted for another vendor fails closed", func(t *testing.T) {
		h := newIntegrationHarness(domain.IntegrationTodoist, domain.IntegrationHubSpot)
		state := begin(t, h, domain.IntegrationTodoist)
		_, _, err := h.svc.CompleteConnect(ctx, domain.IntegrationHubSpot, state, "code", "")
		if !errors.Is(err, domain.ErrUnauthorized) {
			t.Fatalf("error = %v, want ErrUnauthorized", err)
		}
		if len(h.repo.created) != 0 {
			t.Fatal("vendor-mismatched state must not create a connection")
		}
	})

	t.Run("account-connect state can never complete an integration callback", func(t *testing.T) {
		h := newIntegrationHarness(domain.IntegrationTodoist)
		// A state minted by the ACCOUNT flow (Provider "google", no prefix).
		if err := h.states.Create(ctx, port.OAuthState{
			State:       "acct-state",
			UserID:      "u1",
			Provider:    domain.ProviderGoogle,
			RedirectURL: "http://localhost:3000/settings",
			ExpiresAt:   integrationTestBase.Add(10 * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
		_, _, err := h.svc.CompleteConnect(ctx, domain.IntegrationTodoist, "acct-state", "code", "")
		if !errors.Is(err, domain.ErrUnauthorized) {
			t.Fatalf("error = %v, want ErrUnauthorized (fail closed)", err)
		}
	})

	t.Run("expired state fails closed", func(t *testing.T) {
		h := newIntegrationHarness(domain.IntegrationTodoist)
		state := begin(t, h, domain.IntegrationTodoist)
		h.clock.Advance(11 * time.Minute)
		_, redirect, err := h.svc.CompleteConnect(ctx, domain.IntegrationTodoist, state, "code", "")
		if !errors.Is(err, domain.ErrUnauthorized) {
			t.Fatalf("error = %v, want ErrUnauthorized", err)
		}
		if redirect != "http://localhost:3000/settings" {
			t.Fatalf("redirect = %q (consumed states still return the client target)", redirect)
		}
	})

	t.Run("state is one-time: replay fails", func(t *testing.T) {
		h := newIntegrationHarness(domain.IntegrationTodoist)
		h.gw.token = port.OAuthToken{TokenSet: port.TokenSet{AccessToken: "at"}}
		state := begin(t, h, domain.IntegrationTodoist)
		if _, _, err := h.svc.CompleteConnect(ctx, domain.IntegrationTodoist, state, "code", ""); err != nil {
			t.Fatalf("first CompleteConnect: %v", err)
		}
		if _, _, err := h.svc.CompleteConnect(ctx, domain.IntegrationTodoist, state, "code", ""); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("replayed state error = %v, want ErrNotFound", err)
		}
	})

	t.Run("failed exchange maps to unauthorized", func(t *testing.T) {
		h := newIntegrationHarness(domain.IntegrationTodoist)
		h.gw.exchangeErr = errors.New("vendor said no")
		state := begin(t, h, domain.IntegrationTodoist)
		_, _, err := h.svc.CompleteConnect(ctx, domain.IntegrationTodoist, state, "code", "")
		if !errors.Is(err, domain.ErrUnauthorized) {
			t.Fatalf("error = %v, want ErrUnauthorized", err)
		}
	})
}

func TestIntegrationDisconnect(t *testing.T) {
	ctx := context.Background()

	seed := func(t *testing.T, h *integrationHarness, userID string, vendor domain.IntegrationVendor) domain.IntegrationConnection {
		t.Helper()
		conn, err := h.repo.Create(ctx, domain.IntegrationConnection{
			ID: newID(), UserID: userID, Vendor: vendor,
			Status: domain.IntegrationStatusActive, CreatedAt: integrationTestBase,
		})
		if err != nil {
			t.Fatal(err)
		}
		return conn
	}

	t.Run("todoist disconnect purges mirrored tasks then deletes", func(t *testing.T) {
		h := newIntegrationHarness(domain.IntegrationTodoist)
		conn := seed(t, h, "u1", domain.IntegrationTodoist)
		if err := h.svc.Disconnect(ctx, "u1", conn.ID); err != nil {
			t.Fatalf("Disconnect: %v", err)
		}
		if len(h.purged) != 1 || h.purged[0] != "u1" {
			t.Fatalf("purge hook calls = %v, want [u1]", h.purged)
		}
		if _, err := h.repo.GetByID(ctx, conn.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("connection still present after disconnect: %v", err)
		}
	})

	t.Run("hubspot disconnect never touches the task purge", func(t *testing.T) {
		h := newIntegrationHarness(domain.IntegrationHubSpot)
		conn := seed(t, h, "u1", domain.IntegrationHubSpot)
		if err := h.svc.Disconnect(ctx, "u1", conn.ID); err != nil {
			t.Fatalf("Disconnect: %v", err)
		}
		if len(h.purged) != 0 {
			t.Fatalf("purge hook called for hubspot: %v", h.purged)
		}
	})

	t.Run("foreign connection is indistinguishable from missing", func(t *testing.T) {
		h := newIntegrationHarness(domain.IntegrationTodoist)
		conn := seed(t, h, "owner", domain.IntegrationTodoist)
		if err := h.svc.Disconnect(ctx, "intruder", conn.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("error = %v, want ErrNotFound", err)
		}
		if len(h.purged) != 0 {
			t.Fatal("purge hook must not run for a foreign disconnect")
		}
		if _, err := h.repo.GetByID(ctx, conn.ID); err != nil {
			t.Fatalf("foreign disconnect deleted the row: %v", err)
		}
	})

	t.Run("nil purge hook is safe", func(t *testing.T) {
		h := newIntegrationHarness(domain.IntegrationTodoist)
		h.svc.purgeTodoistTasks = nil
		conn := seed(t, h, "u1", domain.IntegrationTodoist)
		if err := h.svc.Disconnect(ctx, "u1", conn.ID); err != nil {
			t.Fatalf("Disconnect with nil purge hook: %v", err)
		}
	})
}

func TestIntegrationList(t *testing.T) {
	ctx := context.Background()
	h := newIntegrationHarness(domain.IntegrationTodoist)

	conns, err := h.svc.List(ctx, "u1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if conns == nil || len(conns) != 0 {
		t.Fatalf("List = %#v, want empty non-nil slice", conns)
	}

	if _, err := h.repo.Create(ctx, domain.IntegrationConnection{
		ID: "c1", UserID: "u1", Vendor: domain.IntegrationTodoist,
		Status: domain.IntegrationStatusActive, CreatedAt: integrationTestBase,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.repo.Create(ctx, domain.IntegrationConnection{
		ID: "c2", UserID: "someone-else", Vendor: domain.IntegrationTodoist,
		Status: domain.IntegrationStatusActive, CreatedAt: integrationTestBase,
	}); err != nil {
		t.Fatal(err)
	}
	conns, err = h.svc.List(ctx, "u1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(conns) != 1 || conns[0].ID != "c1" {
		t.Fatalf("List = %#v, want just c1", conns)
	}
}
