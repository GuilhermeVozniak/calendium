package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- TeamService fixture (Task 4) --------------------------------------------

// teamFixture wires a TeamService to the shared package-service fakes with a
// Google mail provider + oauth gateway registered and a frozen clock. Most
// tests run SelfHost=true so the paywall is bypassed (paywall is exercised in
// its own tests below).
type teamFixture struct {
	svc      *TeamService
	teams    *fakeTeamRepo
	invites  *fakeTeamInvitationRepo
	users    *fakeUserRepo
	accounts *fakeAccountRepo
	subs     *fakeSubscriptionRepo
	provider *fakeMailProvider
	mailer   *fakeMailer
	tx       *fakeTxRunner
	clock    *fakeClock
}

func newTeamFixture(t *testing.T, selfHost bool) *teamFixture {
	return newTeamFixtureWithMailer(t, selfHost, nil)
}

// newTeamFixtureWithMailer wires an optional instance Mailer (nil = SMTP
// unset, the default self-host shape). A nil *fakeMailer must become a nil
// port.Mailer, never a typed nil, so the service's nil check holds.
func newTeamFixtureWithMailer(t *testing.T, selfHost bool, mailer *fakeMailer) *teamFixture {
	t.Helper()
	f := &teamFixture{
		teams:    newTeamRepo(),
		invites:  newTeamInvitationRepo(),
		users:    newUserRepo(),
		accounts: newAccountRepo(),
		subs:     newSubscriptionRepo(),
		provider: newMailProvider(),
		mailer:   mailer,
		tx:       newTxRunner(),
		clock:    newClock(time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)),
	}
	var m port.Mailer
	if mailer != nil {
		m = mailer
	}
	f.svc = NewTeamService(TeamServiceDeps{
		Teams:       f.teams,
		Invitations: f.invites,
		Users:       f.users,
		Accounts:    f.accounts,
		Mail:        map[domain.Provider]port.MailProvider{domain.ProviderGoogle: f.provider},
		OAuth:       map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
		Mailer:      m,
		Subs:        f.subs,
		Tx:          f.tx,
		Clock:       f.clock,
		SelfHost:    selfHost,
		AppBaseURL:  "https://app.calendium.test",
	})
	return f
}

// seedTeam persists a team plus its owner membership directly in the fake
// repo (controlled IDs; the Create service path has its own test).
func (f *teamFixture) seedTeam(t *testing.T, id, owner string) domain.Team {
	t.Helper()
	team := domain.Team{ID: id, Name: "Team " + id, CreatedBy: owner, CreatedAt: f.clock.Now()}
	if _, err := f.teams.Create(context.Background(), team, domain.TeamMember{
		TeamID: id, UserID: owner, Role: domain.TeamRoleOwner, JoinedAt: f.clock.Now(),
	}); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	return team
}

func (f *teamFixture) addMember(t *testing.T, teamID, userID string, role domain.TeamRole) {
	t.Helper()
	if err := f.teams.UpsertMember(context.Background(), domain.TeamMember{
		TeamID: teamID, UserID: userID, Role: role, JoinedAt: f.clock.Now(),
	}); err != nil {
		t.Fatalf("seed member: %v", err)
	}
}

// seedSendAccount gives userID an active Google account with fresh tokens so
// the invite-email pipeline resolves an access token without a refresh.
func (f *teamFixture) seedSendAccount(t *testing.T, userID string) domain.ConnectedAccount {
	t.Helper()
	ctx := context.Background()
	a := domain.ConnectedAccount{
		ID:       "acct-" + userID,
		UserID:   userID,
		Email:    userID + "@acme.com",
		Provider: domain.ProviderGoogle,
		Status:   domain.AccountActive,
	}
	if _, err := f.accounts.Create(ctx, a); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if err := f.accounts.SaveTokens(ctx, a.ID, port.TokenSet{
		AccessToken: "tok-" + userID, RefreshToken: "r", ExpiresAt: f.clock.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("seed tokens: %v", err)
	}
	return a
}

// invite runs a happy-path Invite and returns the invitation plus the raw
// token parsed out of the emailed link — the only place it ever appears.
func (f *teamFixture) invite(t *testing.T, inviter, teamID, email string, role domain.TeamRole) (domain.TeamInvitation, string) {
	t.Helper()
	before := len(f.provider.sent)
	inv, err := f.svc.Invite(context.Background(), inviter, teamID, email, role)
	if err != nil {
		t.Fatalf("Invite: %v", err)
	}
	if len(f.provider.sent) != before+1 {
		t.Fatalf("Invite recorded %d MailProvider.Send calls, want 1", len(f.provider.sent)-before)
	}
	return inv, rawTokenFromEmail(t, f.provider.sent[len(f.provider.sent)-1])
}

// rawTokenFromEmail extracts the hex token following "/invite/" in the text body.
func rawTokenFromEmail(t *testing.T, msg port.OutgoingMessage) string {
	t.Helper()
	const marker = "/invite/"
	i := strings.Index(msg.BodyText, marker)
	if i < 0 {
		t.Fatalf("invite email text body %q has no %q link", msg.BodyText, marker)
	}
	rest := msg.BodyText[i+len(marker):]
	end := 0
	for end < len(rest) && (rest[end] >= '0' && rest[end] <= '9' || rest[end] >= 'a' && rest[end] <= 'f') {
		end++
	}
	if end == 0 {
		t.Fatalf("invite link in %q carries no token", msg.BodyText)
	}
	return rest[:end]
}

// --- Create / List / Get ------------------------------------------------------

func TestTeamCreateMakesCallerOwner(t *testing.T) {
	f := newTeamFixture(t, true)
	ctx := context.Background()

	team, err := f.svc.Create(ctx, "u1", port.TeamInput{Name: "  Acme Corp  "})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if team.Name != "Acme Corp" {
		t.Fatalf("team.Name = %q, want trimmed %q", team.Name, "Acme Corp")
	}
	if team.CreatedBy != "u1" {
		t.Fatalf("team.CreatedBy = %q, want u1", team.CreatedBy)
	}
	m, err := f.teams.GetMember(ctx, team.ID, "u1")
	if err != nil {
		t.Fatalf("creator membership missing: %v", err)
	}
	if m.Role != domain.TeamRoleOwner {
		t.Fatalf("creator role = %s, want owner", m.Role)
	}
}

func TestTeamCreateValidatesName(t *testing.T) {
	f := newTeamFixture(t, true)
	if _, err := f.svc.Create(context.Background(), "u1", port.TeamInput{Name: "   "}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("Create err = %v, want ErrValidation", err)
	}
}

func TestTeamGetNonMemberNotFound(t *testing.T) {
	f := newTeamFixture(t, true)
	f.seedTeam(t, "t1", "owner")
	ctx := context.Background()

	if _, _, err := f.svc.Get(ctx, "stranger", "t1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get as non-member err = %v, want ErrNotFound (never reveal team existence)", err)
	}

	team, members, err := f.svc.Get(ctx, "owner", "t1")
	if err != nil {
		t.Fatalf("Get as member: %v", err)
	}
	if team.ID != "t1" || len(members) != 1 || members[0].UserID != "owner" {
		t.Fatalf("Get = %+v members %+v, want t1 with its sole owner member", team, members)
	}
}

func TestTeamListReturnsOnlyCallersTeams(t *testing.T) {
	f := newTeamFixture(t, true)
	f.seedTeam(t, "t1", "u1")
	f.seedTeam(t, "t2", "u2")
	f.addMember(t, "t2", "u1", domain.TeamRoleMember)
	ctx := context.Background()

	teams, err := f.svc.List(ctx, "u1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(teams) != 2 || teams[0].ID != "t1" || teams[1].ID != "t2" {
		t.Fatalf("List = %+v, want [t1 t2]", teams)
	}
	outsider, err := f.svc.List(ctx, "u3")
	if err != nil {
		t.Fatalf("List outsider: %v", err)
	}
	if outsider == nil || len(outsider) != 0 {
		t.Fatalf("List for outsider = %#v, want empty non-nil slice", outsider)
	}
}

// --- Rename / Delete ----------------------------------------------------------

func TestTeamRenameRoleMatrix(t *testing.T) {
	f := newTeamFixture(t, true)
	f.seedTeam(t, "t1", "owner")
	f.addMember(t, "t1", "admin", domain.TeamRoleAdmin)
	f.addMember(t, "t1", "member", domain.TeamRoleMember)
	ctx := context.Background()

	if _, err := f.svc.Rename(ctx, "member", "t1", "New Name"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("Rename as member err = %v, want ErrForbidden", err)
	}
	if _, err := f.svc.Rename(ctx, "stranger", "t1", "New Name"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Rename as non-member err = %v, want ErrNotFound", err)
	}
	team, err := f.svc.Rename(ctx, "admin", "t1", "New Name")
	if err != nil {
		t.Fatalf("Rename as admin: %v", err)
	}
	if team.Name != "New Name" {
		t.Fatalf("renamed team.Name = %q, want New Name", team.Name)
	}
	if got, _ := f.teams.GetByID(ctx, "t1"); got.Name != "New Name" {
		t.Fatalf("persisted name = %q, want New Name", got.Name)
	}
}

func TestTeamDeleteRequiresOwner(t *testing.T) {
	f := newTeamFixture(t, true)
	f.seedTeam(t, "t1", "owner")
	f.addMember(t, "t1", "admin", domain.TeamRoleAdmin)
	ctx := context.Background()

	if err := f.svc.Delete(ctx, "admin", "t1"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("Delete as admin err = %v, want ErrForbidden", err)
	}
	if err := f.svc.Delete(ctx, "owner", "t1"); err != nil {
		t.Fatalf("Delete as owner: %v", err)
	}
	if _, err := f.teams.GetByID(ctx, "t1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("team lookup after delete err = %v, want ErrNotFound", err)
	}
}

// --- SetMemberRole ------------------------------------------------------------

func TestTeamSetMemberRole(t *testing.T) {
	seed := func(t *testing.T) *teamFixture {
		f := newTeamFixture(t, true)
		f.seedTeam(t, "t1", "owner")
		f.addMember(t, "t1", "admin", domain.TeamRoleAdmin)
		f.addMember(t, "t1", "member", domain.TeamRoleMember)
		return f
	}
	ctx := context.Background()

	t.Run("admin grants admin", func(t *testing.T) {
		f := seed(t)
		m, err := f.svc.SetMemberRole(ctx, "admin", "t1", "member", domain.TeamRoleAdmin)
		if err != nil {
			t.Fatalf("SetMemberRole: %v", err)
		}
		if m.Role != domain.TeamRoleAdmin {
			t.Fatalf("role = %s, want admin", m.Role)
		}
		if got, _ := f.teams.GetMember(ctx, "t1", "member"); got.Role != domain.TeamRoleAdmin {
			t.Fatalf("persisted role = %s, want admin", got.Role)
		}
	})

	t.Run("admin grants owner forbidden", func(t *testing.T) {
		f := seed(t)
		if _, err := f.svc.SetMemberRole(ctx, "admin", "t1", "member", domain.TeamRoleOwner); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden (only owners grant owner)", err)
		}
	})

	t.Run("admin demotes owner forbidden", func(t *testing.T) {
		f := seed(t)
		if _, err := f.svc.SetMemberRole(ctx, "admin", "t1", "owner", domain.TeamRoleMember); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden (only owners revoke owner)", err)
		}
	})

	t.Run("demote last owner conflicts inside a transaction", func(t *testing.T) {
		f := seed(t)
		if _, err := f.svc.SetMemberRole(ctx, "owner", "t1", "owner", domain.TeamRoleAdmin); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
		if f.tx.calls == 0 {
			t.Fatal("owner demotion must run inside TxRunner.RunInTx (read-then-write race)")
		}
		if got, _ := f.teams.GetMember(ctx, "t1", "owner"); got.Role != domain.TeamRoleOwner {
			t.Fatalf("last owner role mutated to %s", got.Role)
		}
	})

	t.Run("demote with second owner succeeds", func(t *testing.T) {
		f := seed(t)
		f.addMember(t, "t1", "owner2", domain.TeamRoleOwner)
		m, err := f.svc.SetMemberRole(ctx, "owner", "t1", "owner2", domain.TeamRoleAdmin)
		if err != nil {
			t.Fatalf("SetMemberRole: %v", err)
		}
		if m.Role != domain.TeamRoleAdmin {
			t.Fatalf("role = %s, want admin", m.Role)
		}
	})

	t.Run("member caller forbidden", func(t *testing.T) {
		f := seed(t)
		if _, err := f.svc.SetMemberRole(ctx, "member", "t1", "member", domain.TeamRoleAdmin); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
	})

	t.Run("non-member caller not found", func(t *testing.T) {
		f := seed(t)
		if _, err := f.svc.SetMemberRole(ctx, "stranger", "t1", "member", domain.TeamRoleAdmin); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("target not a member not found", func(t *testing.T) {
		f := seed(t)
		if _, err := f.svc.SetMemberRole(ctx, "owner", "t1", "ghost", domain.TeamRoleAdmin); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("invalid role validation", func(t *testing.T) {
		f := seed(t)
		if _, err := f.svc.SetMemberRole(ctx, "owner", "t1", "member", domain.TeamRole("boss")); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("non-member with invalid role still not found (membership checked first)", func(t *testing.T) {
		f := seed(t)
		// Oracle consistency: a stranger must get 404 whether or not the
		// payload validates — a 400 here would leak that the team exists.
		if _, err := f.svc.SetMemberRole(ctx, "stranger", "t1", "member", domain.TeamRole("boss")); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("under-privileged member with invalid role still forbidden (role gate before payload)", func(t *testing.T) {
		f := seed(t)
		if _, err := f.svc.SetMemberRole(ctx, "member", "t1", "member", domain.TeamRole("boss")); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
	})
}

// --- RemoveMember / leave -----------------------------------------------------

func TestTeamRemoveMemberAndLeave(t *testing.T) {
	seed := func(t *testing.T) *teamFixture {
		f := newTeamFixture(t, true)
		f.seedTeam(t, "t1", "owner")
		f.addMember(t, "t1", "admin", domain.TeamRoleAdmin)
		f.addMember(t, "t1", "member", domain.TeamRoleMember)
		return f
	}
	ctx := context.Background()

	t.Run("member leaves", func(t *testing.T) {
		f := seed(t)
		if err := f.svc.RemoveMember(ctx, "member", "t1", "member"); err != nil {
			t.Fatalf("leave: %v", err)
		}
		if _, err := f.teams.GetMember(ctx, "t1", "member"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatal("membership still present after leave")
		}
	})

	t.Run("last owner cannot leave", func(t *testing.T) {
		f := seed(t)
		if err := f.svc.RemoveMember(ctx, "owner", "t1", "owner"); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
		if f.tx.calls == 0 {
			t.Fatal("owner removal must run inside TxRunner.RunInTx (read-then-write race)")
		}
		if _, err := f.teams.GetMember(ctx, "t1", "owner"); err != nil {
			t.Fatal("last owner was removed")
		}
	})

	t.Run("owner leaves once another owner exists", func(t *testing.T) {
		f := seed(t)
		f.addMember(t, "t1", "owner2", domain.TeamRoleOwner)
		if err := f.svc.RemoveMember(ctx, "owner", "t1", "owner"); err != nil {
			t.Fatalf("leave with co-owner: %v", err)
		}
	})

	t.Run("admin removes member", func(t *testing.T) {
		f := seed(t)
		if err := f.svc.RemoveMember(ctx, "admin", "t1", "member"); err != nil {
			t.Fatalf("admin removing member: %v", err)
		}
	})

	t.Run("admin removes admin forbidden", func(t *testing.T) {
		f := seed(t)
		f.addMember(t, "t1", "admin2", domain.TeamRoleAdmin)
		if err := f.svc.RemoveMember(ctx, "admin", "t1", "admin2"); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden (admins remove members only)", err)
		}
	})

	t.Run("owner removes admin", func(t *testing.T) {
		f := seed(t)
		if err := f.svc.RemoveMember(ctx, "owner", "t1", "admin"); err != nil {
			t.Fatalf("owner removing admin: %v", err)
		}
	})

	t.Run("member removing others forbidden", func(t *testing.T) {
		f := seed(t)
		if err := f.svc.RemoveMember(ctx, "member", "t1", "admin"); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
	})

	t.Run("non-member caller not found", func(t *testing.T) {
		f := seed(t)
		if err := f.svc.RemoveMember(ctx, "stranger", "t1", "member"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("missing target not found", func(t *testing.T) {
		f := seed(t)
		if err := f.svc.RemoveMember(ctx, "owner", "t1", "ghost"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

// --- SetShareReadStatuses -----------------------------------------------------

func TestTeamSetShareReadStatuses(t *testing.T) {
	f := newTeamFixture(t, true)
	f.seedTeam(t, "t1", "owner")
	f.addMember(t, "t1", "member", domain.TeamRoleMember)
	ctx := context.Background()

	m, err := f.svc.SetShareReadStatuses(ctx, "member", "t1", true)
	if err != nil {
		t.Fatalf("SetShareReadStatuses: %v", err)
	}
	if !m.ShareReadStatuses {
		t.Fatal("ShareReadStatuses = false, want true")
	}
	if got, _ := f.teams.GetMember(ctx, "t1", "member"); !got.ShareReadStatuses {
		t.Fatal("persisted ShareReadStatuses = false, want true")
	}
	if m, _ = f.svc.SetShareReadStatuses(ctx, "member", "t1", false); m.ShareReadStatuses {
		t.Fatal("toggle back: ShareReadStatuses = true, want false")
	}
	if _, err := f.svc.SetShareReadStatuses(ctx, "stranger", "t1", true); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("non-member err = %v, want ErrNotFound", err)
	}
}

// --- Invite -------------------------------------------------------------------

func TestTeamInviteAuthorization(t *testing.T) {
	f := newTeamFixture(t, true)
	f.seedTeam(t, "t1", "owner")
	f.addMember(t, "t1", "admin", domain.TeamRoleAdmin)
	f.addMember(t, "t1", "member", domain.TeamRoleMember)
	f.seedSendAccount(t, "admin")
	ctx := context.Background()

	if _, err := f.svc.Invite(ctx, "member", "t1", "x@example.com", domain.TeamRoleMember); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("Invite as member err = %v, want ErrForbidden", err)
	}
	if _, err := f.svc.Invite(ctx, "stranger", "t1", "x@example.com", domain.TeamRoleMember); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Invite as non-member err = %v, want ErrNotFound", err)
	}
	if _, err := f.svc.Invite(ctx, "admin", "t1", "x@example.com", domain.TeamRoleOwner); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("admin inviting an owner err = %v, want ErrForbidden (only owners grant owner)", err)
	}
	if len(f.provider.sent) != 0 {
		t.Fatalf("rejected invites sent %d emails, want 0", len(f.provider.sent))
	}
}

// TestTeamInviteFallsBackToLinkWithoutSender replaces the old
// TestTeamInviteRequiresConnectedAccount: with neither a connected mailbox
// nor an instance Mailer the invitation is still created and the response
// carries the accept link for the inviter to share by hand.
func TestTeamInviteFallsBackToLinkWithoutSender(t *testing.T) {
	f := newTeamFixture(t, true)
	f.seedTeam(t, "t1", "owner")
	ctx := context.Background()

	inv, err := f.svc.Invite(ctx, "owner", "t1", "x@example.com", domain.TeamRoleMember)
	if err != nil {
		t.Fatalf("Invite without any sender: %v", err)
	}
	if inv.Delivery != domain.DeliveryLink {
		t.Fatalf("Delivery = %q, want link", inv.Delivery)
	}
	const prefix = "https://app.calendium.test/invite/"
	if !strings.HasPrefix(inv.InviteURL, prefix) {
		t.Fatalf("InviteURL = %q, want prefix %q", inv.InviteURL, prefix)
	}
	raw := strings.TrimPrefix(inv.InviteURL, prefix)
	if len(raw) != 64 {
		t.Fatalf("token in link has length %d, want 64 hex chars", len(raw))
	}
	if inv.TokenHash != hashInviteToken(raw) {
		t.Fatal("InviteURL token does not hash to the stored TokenHash")
	}
	if len(f.provider.sent) != 0 {
		t.Fatalf("link fallback sent %d mailbox emails, want 0", len(f.provider.sent))
	}
	stored, _ := f.invites.ListByTeam(ctx, "t1")
	if len(stored) != 1 || stored[0].Status != domain.InvitePending {
		t.Fatalf("stored = %+v, want one pending invitation", stored)
	}
	// The link is response-only: the raw token is never persisted.
	if _, err := f.invites.GetByTokenHash(ctx, raw); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("raw token must not be stored")
	}
}

func TestTeamInviteUsesSMTPWhenNoMailbox(t *testing.T) {
	mailer := newMailer()
	f := newTeamFixtureWithMailer(t, true, mailer)
	f.seedTeam(t, "t1", "owner")
	name := "Olive Owner"
	if _, err := f.users.Upsert(context.Background(), domain.User{ID: "owner", Email: "olive@acme.com", Name: &name}); err != nil {
		t.Fatal(err)
	}

	inv, err := f.svc.Invite(context.Background(), "owner", "t1", "New@Example.com", domain.TeamRoleMember)
	if err != nil {
		t.Fatalf("Invite: %v", err)
	}
	if inv.Delivery != domain.DeliverySMTP || inv.InviteURL != "" {
		t.Fatalf("inv = %+v, want smtp delivery without an inviteUrl", inv)
	}
	if len(mailer.sent) != 1 {
		t.Fatalf("mailer sends = %d, want 1", len(mailer.sent))
	}
	if len(f.provider.sent) != 0 {
		t.Fatal("the mailbox provider must not be used without a connected account")
	}
	m := mailer.sent[0]
	if len(m.To) != 1 || m.To[0] != "new@example.com" {
		t.Fatalf("To = %v, want the canonical invitee", m.To)
	}
	if m.ReplyTo != "olive@acme.com" {
		t.Fatalf("ReplyTo = %q, want the inviter's email", m.ReplyTo)
	}
	if m.Subject != "Olive Owner invited you to Team t1 on Calendium" {
		t.Fatalf("Subject = %q", m.Subject)
	}
	if !strings.Contains(m.Text, `Olive Owner has invited you to join the team "Team t1" on Calendium.`) {
		t.Fatalf("Text = %q, want the shared wording", m.Text)
	}
	if !strings.Contains(m.Text, "https://app.calendium.test/invite/") || !strings.Contains(m.HTML, "https://app.calendium.test/invite/") {
		t.Fatal("both bodies must carry the invite link")
	}
	if !strings.Contains(m.Text, "expires in 14 days") {
		t.Fatalf("Text = %q, want the expiry footer", m.Text)
	}
}

func TestTeamInvitePrefersMailboxOverSMTP(t *testing.T) {
	mailer := newMailer()
	f := newTeamFixtureWithMailer(t, true, mailer)
	f.seedTeam(t, "t1", "owner")
	acct := f.seedSendAccount(t, "owner")

	inv, raw := f.invite(t, "owner", "t1", "x@example.com", domain.TeamRoleMember)
	if inv.Delivery != domain.DeliveryMailbox || inv.InviteURL != "" {
		t.Fatalf("inv = %+v, want mailbox delivery", inv)
	}
	if len(mailer.sent) != 0 {
		t.Fatalf("SMTP used although a mailbox exists: %d sends", len(mailer.sent))
	}
	if f.provider.sent[0].From.Email != acct.Email {
		t.Fatalf("From = %q, want the inviter's mailbox %q", f.provider.sent[0].From.Email, acct.Email)
	}
	if !strings.Contains(f.provider.sent[0].BodyText, "/invite/"+raw) {
		t.Fatal("mailbox email must carry the invite link")
	}
}

func TestTeamInviteSMTPFailureRevokesInvitation(t *testing.T) {
	mailer := newMailer()
	mailer.sendErr = errors.New("smtp exploded")
	f := newTeamFixtureWithMailer(t, true, mailer)
	f.seedTeam(t, "t1", "owner")
	ctx := context.Background()

	_, err := f.svc.Invite(ctx, "owner", "t1", "x@example.com", domain.TeamRoleMember)
	if !errors.Is(err, mailer.sendErr) {
		t.Fatalf("err = %v, want wrapped %v", err, mailer.sendErr)
	}
	if !strings.HasPrefix(err.Error(), "sending invitation email: ") {
		t.Fatalf("err text = %q", err.Error())
	}
	invs, _ := f.invites.ListByTeam(ctx, "t1")
	if len(invs) != 1 || invs[0].Status != domain.InviteRevoked {
		t.Fatalf("stored = %+v, want one revoked invitation (rollback frees the pending-unique index)", invs)
	}
}

func TestTeamInviteHTMLEscapesTeamName(t *testing.T) {
	mailer := newMailer()
	f := newTeamFixtureWithMailer(t, true, mailer)
	team := f.seedTeam(t, "t1", "owner")
	team.Name = `Ops <script>alert(1)</script> & "Co"`
	if err := f.teams.Update(context.Background(), team); err != nil {
		t.Fatal(err)
	}

	if _, err := f.svc.Invite(context.Background(), "owner", "t1", "x@example.com", domain.TeamRoleMember); err != nil {
		t.Fatal(err)
	}
	got := mailer.sent[0]
	if strings.Contains(got.HTML, "<script>") {
		t.Fatalf("HTML body carries raw markup: %q", got.HTML)
	}
	if !strings.Contains(got.HTML, "&lt;script&gt;") {
		t.Fatalf("HTML body = %q, want escaped markup", got.HTML)
	}
	if !strings.Contains(got.Text, `Ops <script>alert(1)</script> & "Co"`) {
		t.Fatal("text body must not be HTML-escaped")
	}
}

func TestTeamInviteRejectsInvalidEmail(t *testing.T) {
	f := newTeamFixture(t, true)
	f.seedTeam(t, "t1", "owner")
	f.seedSendAccount(t, "owner")

	if _, err := f.svc.Invite(context.Background(), "owner", "t1", "not an email", domain.TeamRoleMember); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("Invite with invalid email err = %v, want ErrValidation", err)
	}
}

func TestTeamInviteHappyPathSendsEmail(t *testing.T) {
	f := newTeamFixture(t, true)
	f.seedTeam(t, "t1", "owner")
	acct := f.seedSendAccount(t, "owner")
	name := "Olive Owner"
	if _, err := f.users.Upsert(context.Background(), domain.User{ID: "owner", Email: acct.Email, Name: &name}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	inv, raw := f.invite(t, "owner", "t1", "  New.Person@Example.COM ", domain.TeamRoleAdmin)

	if inv.Email != "new.person@example.com" {
		t.Fatalf("inv.Email = %q, want canonical lowercased %q", inv.Email, "new.person@example.com")
	}
	if inv.Status != domain.InvitePending || inv.Role != domain.TeamRoleAdmin || inv.InvitedBy != "owner" {
		t.Fatalf("invitation = %+v, want pending admin invited by owner", inv)
	}
	if want := f.clock.Now().Add(14 * 24 * time.Hour); !inv.ExpiresAt.Equal(want) {
		t.Fatalf("inv.ExpiresAt = %v, want %v (14-day TTL)", inv.ExpiresAt, want)
	}

	msg := f.provider.sent[0]
	if msg.From.Email != acct.Email {
		t.Fatalf("From = %q, want the inviter's own account %q", msg.From.Email, acct.Email)
	}
	if len(msg.To) != 1 || msg.To[0].Email != "new.person@example.com" {
		t.Fatalf("To = %+v, want the invitee only", msg.To)
	}
	link := "https://app.calendium.test/invite/" + raw
	if !strings.Contains(msg.BodyText, link) {
		t.Fatalf("text body %q does not contain invite link %q", msg.BodyText, link)
	}
	if !strings.Contains(msg.BodyHTML, "/invite/"+raw) {
		t.Fatalf("html body %q does not contain the invite link", msg.BodyHTML)
	}

	// The raw token is 32 random bytes hex-encoded and only its SHA-256 is stored.
	if len(raw) != 64 {
		t.Fatalf("raw token length = %d, want 64 hex chars", len(raw))
	}
	sum := sha256.Sum256([]byte(raw))
	if inv.TokenHash != hex.EncodeToString(sum[:]) {
		t.Fatalf("TokenHash = %q, want sha256(raw) = %q", inv.TokenHash, hex.EncodeToString(sum[:]))
	}
	if _, err := f.invites.GetByTokenHash(ctx, raw); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("raw token stored verbatim in the repo; only the hash may be persisted")
	}
}

func TestTeamInviteDuplicatePendingConflicts(t *testing.T) {
	f := newTeamFixture(t, true)
	f.seedTeam(t, "t1", "owner")
	f.seedSendAccount(t, "owner")
	ctx := context.Background()

	f.invite(t, "owner", "t1", "dupe@example.com", domain.TeamRoleMember)
	if _, err := f.svc.Invite(ctx, "owner", "t1", "DUPE@example.com", domain.TeamRoleMember); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate pending invite err = %v, want ErrConflict", err)
	}
	if len(f.provider.sent) != 1 {
		t.Fatalf("duplicate invite sent an email; %d sends recorded, want 1", len(f.provider.sent))
	}
}

func TestTeamInviteSendFailureRevokesInvitation(t *testing.T) {
	f := newTeamFixture(t, true)
	f.seedTeam(t, "t1", "owner")
	f.seedSendAccount(t, "owner")
	sendErr := errors.New("provider send exploded")
	f.provider.sendErr = sendErr
	ctx := context.Background()

	if _, err := f.svc.Invite(ctx, "owner", "t1", "x@example.com", domain.TeamRoleMember); !errors.Is(err, sendErr) {
		t.Fatalf("Invite with failing send err = %v, want wrapped %v", err, sendErr)
	}
	invs, err := f.invites.ListByTeam(ctx, "t1")
	if err != nil {
		t.Fatalf("ListByTeam: %v", err)
	}
	if len(invs) != 1 {
		t.Fatalf("stored invitations = %d, want 1 (created then rolled back)", len(invs))
	}
	if invs[0].Status != domain.InviteRevoked {
		t.Fatalf("invitation status after send failure = %s, want revoked (rollback frees the pending-unique index)", invs[0].Status)
	}
}

// --- ListInvitations / RevokeInvitation ---------------------------------------

func TestTeamListInvitationsRequiresAdmin(t *testing.T) {
	f := newTeamFixture(t, true)
	f.seedTeam(t, "t1", "owner")
	f.addMember(t, "t1", "member", domain.TeamRoleMember)
	f.seedSendAccount(t, "owner")
	ctx := context.Background()

	inv, _ := f.invite(t, "owner", "t1", "x@example.com", domain.TeamRoleMember)

	if _, err := f.svc.ListInvitations(ctx, "member", "t1"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("ListInvitations as member err = %v, want ErrForbidden", err)
	}
	if _, err := f.svc.ListInvitations(ctx, "stranger", "t1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("ListInvitations as non-member err = %v, want ErrNotFound", err)
	}
	got, err := f.svc.ListInvitations(ctx, "owner", "t1")
	if err != nil {
		t.Fatalf("ListInvitations: %v", err)
	}
	if len(got) != 1 || got[0].ID != inv.ID {
		t.Fatalf("ListInvitations = %+v, want the one pending invitation", got)
	}
}

func TestTeamRevokeInvitation(t *testing.T) {
	f := newTeamFixture(t, true)
	f.seedTeam(t, "t1", "owner")
	f.seedTeam(t, "t2", "owner")
	f.addMember(t, "t1", "member", domain.TeamRoleMember)
	f.seedSendAccount(t, "owner")
	ctx := context.Background()

	inv, raw := f.invite(t, "owner", "t1", "x@example.com", domain.TeamRoleMember)

	if err := f.svc.RevokeInvitation(ctx, "member", "t1", inv.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("revoke as member err = %v, want ErrForbidden", err)
	}
	if err := f.svc.RevokeInvitation(ctx, "owner", "t2", inv.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("revoke via foreign team err = %v, want ErrNotFound", err)
	}
	if err := f.svc.RevokeInvitation(ctx, "owner", "t1", inv.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if got, _ := f.invites.GetByID(ctx, inv.ID); got.Status != domain.InviteRevoked {
		t.Fatalf("status after revoke = %s, want revoked", got.Status)
	}
	if err := f.svc.RevokeInvitation(ctx, "owner", "t1", inv.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("second revoke err = %v, want ErrConflict", err)
	}
	if _, err := f.svc.AcceptInvitation(ctx, "joiner", raw); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("accepting a revoked invitation err = %v, want ErrNotFound (no oracle)", err)
	}
}

// --- AcceptInvitation ---------------------------------------------------------

func TestTeamAcceptInvitation(t *testing.T) {
	setup := func(t *testing.T, role domain.TeamRole) (*teamFixture, domain.TeamInvitation, string) {
		f := newTeamFixture(t, true)
		f.seedTeam(t, "t1", "owner")
		f.seedSendAccount(t, "owner")
		inv, raw := f.invite(t, "owner", "t1", "joiner@example.com", role)
		return f, inv, raw
	}
	ctx := context.Background()

	t.Run("happy path adds membership with the invited role", func(t *testing.T) {
		f, inv, raw := setup(t, domain.TeamRoleAdmin)
		team, err := f.svc.AcceptInvitation(ctx, "joiner", raw)
		if err != nil {
			t.Fatalf("AcceptInvitation: %v", err)
		}
		if team.ID != "t1" {
			t.Fatalf("accepted team = %+v, want t1", team)
		}
		m, err := f.teams.GetMember(ctx, "t1", "joiner")
		if err != nil {
			t.Fatalf("membership missing after accept: %v", err)
		}
		if m.Role != domain.TeamRoleAdmin {
			t.Fatalf("role = %s, want the invited role admin", m.Role)
		}
		if m.ShareReadStatuses {
			t.Fatal("ShareReadStatuses = true on join, want privacy default false")
		}
		if got, _ := f.invites.GetByID(ctx, inv.ID); got.Status != domain.InviteAccepted {
			t.Fatalf("invitation status = %s, want accepted", got.Status)
		}
	})

	t.Run("accepting email need not match invited address", func(t *testing.T) {
		// Invites are bearer links: the emailed address is a delivery hint,
		// the token is the credential.
		f, _, raw := setup(t, domain.TeamRoleMember)
		if _, err := f.users.Upsert(ctx, domain.User{ID: "someone", Email: "different@other.example"}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.svc.AcceptInvitation(ctx, "someone", raw); err != nil {
			t.Fatalf("AcceptInvitation with mismatched email: %v", err)
		}
		if _, err := f.teams.GetMember(ctx, "t1", "someone"); err != nil {
			t.Fatalf("membership missing: %v", err)
		}
	})

	t.Run("expired token not found", func(t *testing.T) {
		f, _, raw := setup(t, domain.TeamRoleMember)
		f.clock.Advance(14*24*time.Hour + time.Second)
		if _, err := f.svc.AcceptInvitation(ctx, "joiner", raw); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("expired token err = %v, want ErrNotFound", err)
		}
	})

	t.Run("token reuse after accept not found", func(t *testing.T) {
		f, _, raw := setup(t, domain.TeamRoleMember)
		if _, err := f.svc.AcceptInvitation(ctx, "joiner", raw); err != nil {
			t.Fatalf("first accept: %v", err)
		}
		if _, err := f.svc.AcceptInvitation(ctx, "second", raw); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("token reuse err = %v, want ErrNotFound", err)
		}
	})

	t.Run("garbage token not found", func(t *testing.T) {
		f, _, _ := setup(t, domain.TeamRoleMember)
		if _, err := f.svc.AcceptInvitation(ctx, "joiner", "deadbeef"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("garbage token err = %v, want ErrNotFound", err)
		}
	})

	t.Run("existing member never demoted by an invite link", func(t *testing.T) {
		f, inv, raw := setup(t, domain.TeamRoleMember)
		if _, err := f.svc.AcceptInvitation(ctx, "owner", raw); err != nil {
			t.Fatalf("owner accepting a member invite: %v", err)
		}
		if m, _ := f.teams.GetMember(ctx, "t1", "owner"); m.Role != domain.TeamRoleOwner {
			t.Fatalf("owner role after accept = %s, want owner (no demotion)", m.Role)
		}
		if got, _ := f.invites.GetByID(ctx, inv.ID); got.Status != domain.InviteAccepted {
			t.Fatalf("invitation status = %s, want accepted", got.Status)
		}
	})
}

// --- Paywall ------------------------------------------------------------------

func TestTeamServicePaywall(t *testing.T) {
	f := newTeamFixture(t, false) // hosted mode, no subscription seeded
	ctx := context.Background()

	if _, err := f.svc.Create(ctx, "u1", port.TeamInput{Name: "X"}); !errors.Is(err, domain.ErrPaymentRequired) {
		t.Fatalf("Create err = %v, want ErrPaymentRequired", err)
	}
	if _, err := f.svc.List(ctx, "u1"); !errors.Is(err, domain.ErrPaymentRequired) {
		t.Fatalf("List err = %v, want ErrPaymentRequired", err)
	}
	if _, err := f.svc.Invite(ctx, "u1", "t1", "a@b.com", domain.TeamRoleMember); !errors.Is(err, domain.ErrPaymentRequired) {
		t.Fatalf("Invite err = %v, want ErrPaymentRequired", err)
	}
	if _, err := f.svc.AcceptInvitation(ctx, "u1", "deadbeef"); !errors.Is(err, domain.ErrPaymentRequired) {
		t.Fatalf("AcceptInvitation err = %v, want ErrPaymentRequired", err)
	}
}

func TestTeamServiceSubscribedCaller(t *testing.T) {
	f := newTeamFixture(t, false)
	if err := f.subs.Upsert(context.Background(), domain.Subscription{UserID: "u1", Status: domain.SubscriptionActive}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Create(context.Background(), "u1", port.TeamInput{Name: "X"}); err != nil {
		t.Fatalf("Create with active subscription: %v", err)
	}
}

func TestTeamServiceSelfHostBypassesBilling(t *testing.T) {
	f := newTeamFixture(t, true) // no subscription seeded anywhere
	if _, err := f.svc.Create(context.Background(), "u1", port.TeamInput{Name: "X"}); err != nil {
		t.Fatalf("Create on self-host without subscription: %v", err)
	}
}
