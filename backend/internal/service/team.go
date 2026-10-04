package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	htmltemplate "html/template"
	netmail "net/mail"
	"strings"
	texttemplate "text/template"
	"time"
	"unicode"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// inviteTTL is how long an emailed team invitation stays redeemable.
const inviteTTL = 14 * 24 * time.Hour

// inviteRollbackTimeout bounds the best-effort revoke after a failed send. It
// runs detached from the request context, so a cancelled request still frees
// the pending-unique index for a retry.
const inviteRollbackTimeout = 5 * time.Second

// Cloud anti-abuse budget: an inviter may send at most inviteEmailLimit
// invitation emails per rolling inviteEmailWindow.
const (
	inviteEmailLimit  = 20
	inviteEmailWindow = time.Hour
)

// TeamServiceDeps wires a TeamService.
type TeamServiceDeps struct {
	Teams       port.TeamRepo
	Invitations port.TeamInvitationRepo
	Users       port.UserRepo
	Accounts    port.AccountRepo
	Mail        map[domain.Provider]port.MailProvider
	OAuth       map[domain.Provider]port.OAuthGateway
	// Mailer is the instance's own SMTP sender, used for invitations when the
	// inviter has no connected mailbox. nil when SMTP_HOST is unset: the
	// invitation is then returned with a copyable link instead.
	Mailer port.Mailer
	Subs   port.SubscriptionRepo
	Tx     port.TxRunner
	Clock  port.Clock
	// SelfHost unlocks the paywall (open-core self-hosted mode).
	SelfHost bool
	// AppBaseURL prefixes the emailed invite link <AppBaseURL>/invite/<token>.
	AppBaseURL string
}

// TeamService implements port.TeamService. All role/authorization decisions
// live here, never in adapters: non-members always get ErrNotFound (team
// existence is never leaked), known members lacking the required role get
// ErrForbidden. Invitation emails go out through the inviter's own connected
// account when one exists, else through the instance's SMTP sender, else the
// response carries a link the inviter shares by hand.
type TeamService struct {
	ent         entitlement
	teams       port.TeamRepo
	invitations port.TeamInvitationRepo
	users       port.UserRepo
	accounts    port.AccountRepo
	mail        map[domain.Provider]port.MailProvider
	mailer      port.Mailer
	tokens      tokenSource
	tx          port.TxRunner
	clock       port.Clock
	appBaseURL  string
	// inviteLimit caps invitation emails per inviter on cloud; nil on
	// self-host (unlimited).
	inviteLimit *rollingLimiter
}

var _ port.TeamService = (*TeamService)(nil)

// Invitation wording mirrors apps/web/lib/email/templates/team-invitation.ts
// so an invite reads the same whichever sender delivered it. html/template
// escapes the inviter name and team name contextually; the text template
// keeps them raw.
var (
	inviteTextTmpl = texttemplate.Must(texttemplate.New("invite-text").Parse(
		"{{.Inviter}} has invited you to join the team \"{{.Team}}\" on Calendium.\n\n" +
			"Accept the invitation: {{.Link}}\n\n" +
			"The link expires in 14 days. If you weren't expecting this, you can safely ignore this email.\n"))
	inviteHTMLTmpl = htmltemplate.Must(htmltemplate.New("invite-html").Parse(
		`<p>{{.Inviter}} has invited you to join the team <strong>{{.Team}}</strong> on Calendium.</p>` +
			`<p><a href="{{.Link}}">Accept the invitation</a></p>` +
			`<p>The link expires in 14 days. If you weren&rsquo;t expecting this, you can safely ignore this email.</p>`))
)

type inviteEmailData struct{ Inviter, Team, Link string }

func NewTeamService(d TeamServiceDeps) *TeamService {
	var limit *rollingLimiter
	if !d.SelfHost {
		limit = newRollingLimiter(inviteEmailLimit, inviteEmailWindow)
	}
	return &TeamService{
		ent:         entitlement{subs: d.Subs, users: d.Users, clock: d.Clock, selfHost: d.SelfHost},
		teams:       d.Teams,
		invitations: d.Invitations,
		users:       d.Users,
		accounts:    d.Accounts,
		mail:        d.Mail,
		mailer:      d.Mailer,
		tokens:      tokenSource{accounts: d.Accounts, oauth: d.OAuth, clock: d.Clock},
		tx:          d.Tx,
		clock:       d.Clock,
		appBaseURL:  strings.TrimRight(d.AppBaseURL, "/"),
		inviteLimit: limit,
	}
}

// --- Teams ---

func (s *TeamService) Create(ctx context.Context, userID string, in port.TeamInput) (domain.Team, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.Team{}, err
	}
	name, err := domain.ValidateTeamName(in.Name)
	if err != nil {
		return domain.Team{}, err
	}
	now := s.clock.Now()
	team := domain.Team{ID: newID(), Name: name, CreatedBy: userID, CreatedAt: now}
	return s.teams.Create(ctx, team, domain.TeamMember{
		TeamID:   team.ID,
		UserID:   userID,
		Role:     domain.TeamRoleOwner,
		JoinedAt: now,
	})
}

func (s *TeamService) List(ctx context.Context, userID string) ([]domain.Team, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return nil, err
	}
	teams, err := s.teams.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if teams == nil {
		teams = []domain.Team{}
	}
	return teams, nil
}

func (s *TeamService) Get(ctx context.Context, userID, teamID string) (domain.Team, []domain.TeamMember, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.Team{}, nil, err
	}
	if _, err := membership(ctx, s.teams, userID, teamID); err != nil {
		return domain.Team{}, nil, err
	}
	team, err := s.teams.GetByID(ctx, teamID)
	if err != nil {
		return domain.Team{}, nil, err
	}
	members, err := s.teams.ListMembers(ctx, teamID)
	if err != nil {
		return domain.Team{}, nil, err
	}
	if members == nil {
		members = []domain.TeamMember{}
	}
	return team, members, nil
}

func (s *TeamService) Rename(ctx context.Context, userID, teamID, name string) (domain.Team, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.Team{}, err
	}
	caller, err := membership(ctx, s.teams, userID, teamID)
	if err != nil {
		return domain.Team{}, err
	}
	if err := requireRole(caller, domain.TeamRoleAdmin); err != nil {
		return domain.Team{}, err
	}
	valid, err := domain.ValidateTeamName(name)
	if err != nil {
		return domain.Team{}, err
	}
	team, err := s.teams.GetByID(ctx, teamID)
	if err != nil {
		return domain.Team{}, err
	}
	team.Name = valid
	if err := s.teams.Update(ctx, team); err != nil {
		return domain.Team{}, err
	}
	return team, nil
}

func (s *TeamService) Delete(ctx context.Context, userID, teamID string) error {
	if err := s.ent.require(ctx, userID); err != nil {
		return err
	}
	caller, err := membership(ctx, s.teams, userID, teamID)
	if err != nil {
		return err
	}
	if err := requireRole(caller, domain.TeamRoleOwner); err != nil {
		return err
	}
	return s.teams.Delete(ctx, teamID)
}

// --- Membership ---

func (s *TeamService) SetMemberRole(ctx context.Context, userID, teamID, memberUserID string, role domain.TeamRole) (domain.TeamMember, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.TeamMember{}, err
	}
	// Membership before payload validation: a non-member probing with an
	// invalid role must see the same 404 as any other non-member (no oracle
	// distinguishing "team exists, bad payload" from "not yours") — M2.7
	// review minor.
	caller, err := membership(ctx, s.teams, userID, teamID)
	if err != nil {
		return domain.TeamMember{}, err
	}
	if err := requireRole(caller, domain.TeamRoleAdmin); err != nil {
		return domain.TeamMember{}, err
	}
	if _, err := domain.ParseTeamRole(string(role)); err != nil {
		return domain.TeamMember{}, err
	}
	var out domain.TeamMember
	// The last-owner invariant is a read-then-write (count, then mutate):
	// it must run inside a transaction or two concurrent demotions could
	// each see two owners and leave the team ownerless.
	err = s.tx.RunInTx(ctx, func(ctx context.Context) error {
		target, err := s.teams.GetMember(ctx, teamID, memberUserID)
		if err != nil {
			return memberLookupErr(err)
		}
		if (role == domain.TeamRoleOwner || target.Role == domain.TeamRoleOwner) && caller.Role != domain.TeamRoleOwner {
			return fmt.Errorf("%w: only owners may grant or revoke the owner role", domain.ErrForbidden)
		}
		if target.Role == domain.TeamRoleOwner && role != domain.TeamRoleOwner {
			if err := s.requireAnotherOwner(ctx, teamID); err != nil {
				return err
			}
		}
		target.Role = role
		if err := s.teams.UpsertMember(ctx, target); err != nil {
			return err
		}
		out = target
		return nil
	})
	if err != nil {
		return domain.TeamMember{}, err
	}
	return out, nil
}

func (s *TeamService) SetShareReadStatuses(ctx context.Context, userID, teamID string, share bool) (domain.TeamMember, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.TeamMember{}, err
	}
	m, err := membership(ctx, s.teams, userID, teamID)
	if err != nil {
		return domain.TeamMember{}, err
	}
	m.ShareReadStatuses = share
	if err := s.teams.UpsertMember(ctx, m); err != nil {
		return domain.TeamMember{}, err
	}
	return m, nil
}

func (s *TeamService) RemoveMember(ctx context.Context, userID, teamID, memberUserID string) error {
	if err := s.ent.require(ctx, userID); err != nil {
		return err
	}
	caller, err := membership(ctx, s.teams, userID, teamID)
	if err != nil {
		return err
	}
	leave := memberUserID == userID
	if !leave {
		if err := requireRole(caller, domain.TeamRoleAdmin); err != nil {
			return err
		}
	}
	// Removing an owner counts owners first (read-then-write); run the
	// whole lookup+count+delete atomically.
	return s.tx.RunInTx(ctx, func(ctx context.Context) error {
		target, err := s.teams.GetMember(ctx, teamID, memberUserID)
		if err != nil {
			return memberLookupErr(err)
		}
		if !leave && target.Role != domain.TeamRoleMember && caller.Role != domain.TeamRoleOwner {
			return fmt.Errorf("%w: only owners may remove admins or owners", domain.ErrForbidden)
		}
		if target.Role == domain.TeamRoleOwner {
			if err := s.requireAnotherOwner(ctx, teamID); err != nil {
				return err
			}
		}
		return s.teams.RemoveMember(ctx, teamID, memberUserID)
	})
}

// requireAnotherOwner enforces the last-owner invariant before an owner is
// demoted or removed. Callers must invoke it inside TxRunner.RunInTx.
func (s *TeamService) requireAnotherOwner(ctx context.Context, teamID string) error {
	n, err := s.teams.CountByRole(ctx, teamID, domain.TeamRoleOwner)
	if err != nil {
		return err
	}
	if n <= 1 {
		return fmt.Errorf("%w: a team must keep at least one owner", domain.ErrConflict)
	}
	return nil
}

// memberLookupErr keeps a missing target member a plain 404 while letting
// infrastructure errors surface unchanged.
func memberLookupErr(err error) error {
	if errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("%w: no such team member", domain.ErrNotFound)
	}
	return err
}

// --- Invitations ---

func (s *TeamService) Invite(ctx context.Context, userID, teamID, email string, role domain.TeamRole) (domain.TeamInvitation, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.TeamInvitation{}, err
	}
	caller, err := membership(ctx, s.teams, userID, teamID)
	if err != nil {
		return domain.TeamInvitation{}, err
	}
	if err := requireRole(caller, domain.TeamRoleAdmin); err != nil {
		return domain.TeamInvitation{}, err
	}
	if _, err := domain.ParseTeamRole(string(role)); err != nil {
		return domain.TeamInvitation{}, err
	}
	if role == domain.TeamRoleOwner && caller.Role != domain.TeamRoleOwner {
		return domain.TeamInvitation{}, fmt.Errorf("%w: only owners may grant the owner role", domain.ErrForbidden)
	}
	canonical, err := canonicalInviteEmail(email)
	if err != nil {
		return domain.TeamInvitation{}, err
	}
	team, err := s.teams.GetByID(ctx, teamID)
	if err != nil {
		return domain.TeamInvitation{}, err
	}
	// Pick the sender BEFORE persisting anything so a broken mailbox never
	// leaves a dangling pending invitation behind: (a) the inviter's own
	// connected mailbox, (b) the instance SMTP sender, (c) a link the inviter
	// shares by hand. Cloud always has (b), so (c) only happens on self-host.
	acct, provider, hasMailbox, err := s.senderAccount(ctx, userID)
	if err != nil {
		return domain.TeamInvitation{}, err
	}
	delivery := domain.DeliveryLink
	var accessToken string
	switch {
	case hasMailbox:
		delivery = domain.DeliveryMailbox
		accessToken, err = s.tokens.accessToken(ctx, acct)
		if err != nil {
			return domain.TeamInvitation{}, err
		}
	case s.mailer != nil:
		delivery = domain.DeliverySMTP
	}
	now := s.clock.Now()
	raw := randomToken(32)
	link := s.appBaseURL + "/invite/" + raw
	inviter, replyTo := s.inviterIdentity(ctx, userID, acct)
	// Render before persisting so a render failure never strands a pending row.
	subject, htmlBody, textBody, err := renderInviteEmail(inviter, team.Name, link)
	if err != nil {
		return domain.TeamInvitation{}, err
	}
	// Cloud anti-abuse: reserve one of the inviter's invitation emails
	// before persisting, so a limited request leaves no row behind.
	limited := s.inviteLimit != nil && delivery != domain.DeliveryLink
	if limited && !s.inviteLimit.reserve(userID, now) {
		return domain.TeamInvitation{}, fmt.Errorf("%w: at most %d invitation emails per hour; try again later", domain.ErrRateLimited, inviteEmailLimit)
	}
	inv, err := s.invitations.Create(ctx, domain.TeamInvitation{
		ID:        newID(),
		TeamID:    teamID,
		Email:     canonical,
		Role:      role,
		InvitedBy: userID,
		Status:    domain.InvitePending,
		TokenHash: hashInviteToken(raw),
		ExpiresAt: now.Add(inviteTTL),
		CreatedAt: now,
	})
	if err != nil {
		if limited {
			s.inviteLimit.release(userID, now) // nothing was sent
		}
		return domain.TeamInvitation{}, err
	}
	switch delivery {
	case domain.DeliveryMailbox:
		_, err = provider.Send(ctx, accessToken, port.OutgoingMessage{
			From:     domain.EmailAddress{Email: acct.Email},
			To:       []domain.EmailAddress{{Email: canonical}},
			Subject:  subject,
			BodyHTML: htmlBody,
			BodyText: textBody,
		})
	case domain.DeliverySMTP:
		err = s.mailer.Send(ctx, port.Email{
			To:      []string{canonical},
			ReplyTo: replyTo,
			Subject: subject,
			Text:    textBody,
			HTML:    htmlBody,
		})
	case domain.DeliveryLink:
		inv.InviteURL = link
	}
	if err != nil {
		// Best-effort rollback so the pending-unique index does not block
		// a retry after a transient send failure. Detached from ctx: the
		// send most often fails BECAUSE the request was cancelled.
		rbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), inviteRollbackTimeout)
		inv.Status = domain.InviteRevoked
		_ = s.invitations.Update(rbCtx, inv)
		cancel()
		// writeError logs this at error level: keep the invitee's domain,
		// never the address (mailbox and SMTP replies may echo it).
		return domain.TeamInvitation{}, fmt.Errorf("sending invitation email: %w", domain.RedactEmailsInError(err, canonical))
	}
	inv.Delivery = delivery
	return inv, nil
}

func (s *TeamService) ListInvitations(ctx context.Context, userID, teamID string) ([]domain.TeamInvitation, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return nil, err
	}
	caller, err := membership(ctx, s.teams, userID, teamID)
	if err != nil {
		return nil, err
	}
	if err := requireRole(caller, domain.TeamRoleAdmin); err != nil {
		return nil, err
	}
	invs, err := s.invitations.ListByTeam(ctx, teamID)
	if err != nil {
		return nil, err
	}
	if invs == nil {
		invs = []domain.TeamInvitation{}
	}
	return invs, nil
}

func (s *TeamService) RevokeInvitation(ctx context.Context, userID, teamID, invitationID string) error {
	if err := s.ent.require(ctx, userID); err != nil {
		return err
	}
	caller, err := membership(ctx, s.teams, userID, teamID)
	if err != nil {
		return err
	}
	if err := requireRole(caller, domain.TeamRoleAdmin); err != nil {
		return err
	}
	inv, err := s.invitations.GetByID(ctx, invitationID)
	if err != nil {
		return err
	}
	if inv.TeamID != teamID {
		// An invitation reached through the wrong team is indistinguishable
		// from a missing one.
		return domain.ErrNotFound
	}
	if inv.Status != domain.InvitePending {
		return fmt.Errorf("%w: invitation is not pending", domain.ErrConflict)
	}
	inv.Status = domain.InviteRevoked
	return s.invitations.Update(ctx, inv)
}

func (s *TeamService) AcceptInvitation(ctx context.Context, userID, token string) (domain.Team, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.Team{}, err
	}
	hash := hashInviteToken(token)
	var team domain.Team
	// Redeem atomically: lookup + flip-to-accepted is a read-then-write
	// that must not double-redeem under concurrent accepts.
	err := s.tx.RunInTx(ctx, func(ctx context.Context) error {
		inv, err := s.invitations.GetByTokenHash(ctx, hash)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return domain.ErrNotFound
			}
			return err
		}
		if !inv.Usable(s.clock.Now()) {
			// Expired, revoked, and already-used tokens are all
			// indistinguishable from unknown ones (no oracle).
			return domain.ErrNotFound
		}
		// The emailed address is a delivery hint; the token is the
		// credential. Any authenticated user holding the link may join.
		if _, err := s.teams.GetMember(ctx, inv.TeamID, userID); err != nil {
			if err := s.teams.UpsertMember(ctx, domain.TeamMember{
				TeamID:   inv.TeamID,
				UserID:   userID,
				Role:     inv.Role,
				JoinedAt: s.clock.Now(),
			}); err != nil {
				return err
			}
		}
		// Already a member: keep the existing membership untouched — an
		// invite link must never demote (e.g. an owner opening a member
		// invite).
		inv.Status = domain.InviteAccepted
		if err := s.invitations.Update(ctx, inv); err != nil {
			return err
		}
		joined, err := s.teams.GetByID(ctx, inv.TeamID)
		if err != nil {
			return err
		}
		team = joined
		return nil
	})
	if err != nil {
		return domain.Team{}, err
	}
	return team, nil
}

// --- helpers ---

// hashInviteToken derives the stored lookup key: invitations persist only the
// SHA-256 hex of the raw token, which itself appears once, inside the emailed
// link.
func hashInviteToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// canonicalInviteEmail validates and canonicalizes an invitation address via
// net/mail parsing, lowercasing the bare addr-spec so the pending-unique
// index treats case variants as the same invitee.
func canonicalInviteEmail(email string) (string, error) {
	addr, err := netmail.ParseAddress(strings.TrimSpace(email))
	if err != nil {
		return "", fmt.Errorf("%w: invalid invitation email address", domain.ErrValidation)
	}
	return strings.ToLower(addr.Address), nil
}

// senderAccount picks the inviter's first active connected account that has a
// configured mail provider. found=false (with a nil error) means the inviter
// has no usable mailbox and the caller falls back to SMTP or a link.
func (s *TeamService) senderAccount(ctx context.Context, userID string) (acct domain.ConnectedAccount, provider port.MailProvider, found bool, err error) {
	accounts, err := s.accounts.ListByUser(ctx, userID)
	if err != nil {
		return domain.ConnectedAccount{}, nil, false, err
	}
	for _, a := range accounts {
		if a.Status != domain.AccountActive {
			continue
		}
		if p, ok := s.mail[a.Provider]; ok {
			return a, p, true, nil
		}
	}
	return domain.ConnectedAccount{}, nil, false, nil
}

// inviterIdentity resolves the display name used in the invitation and the
// Reply-To for SMTP delivery: the user row's name (else its email), with the
// connected mailbox address as the fallback when the user row is missing.
func (s *TeamService) inviterIdentity(ctx context.Context, inviterID string, acct domain.ConnectedAccount) (name, replyTo string) {
	name, replyTo = acct.Email, acct.Email
	if u, err := s.users.GetByID(ctx, inviterID); err == nil {
		if u.Email != "" {
			name, replyTo = u.Email, u.Email
		}
		if u.Name != nil && strings.TrimSpace(*u.Name) != "" {
			name = strings.TrimSpace(*u.Name)
		}
	}
	if name == "" {
		name = "A teammate"
	}
	return name, replyTo
}

// renderInviteEmail renders the subject and both bodies from the shared
// templates. Both names are user-controlled (ValidateTeamName only trims;
// Better Auth names are unchecked), so they pass through headerSafe first: a
// line break would otherwise make the mail builder reject the Subject and
// fail every invitation from that team or inviter.
func renderInviteEmail(inviter, team, link string) (subject, htmlBody, textBody string, err error) {
	inviter, team = headerSafe(inviter), headerSafe(team)
	if inviter == "" {
		inviter = "A teammate"
	}
	data := inviteEmailData{Inviter: inviter, Team: team, Link: link}
	var textOut, htmlOut strings.Builder
	if err := inviteTextTmpl.Execute(&textOut, data); err != nil {
		return "", "", "", fmt.Errorf("rendering invitation text: %w", err)
	}
	if err := inviteHTMLTmpl.Execute(&htmlOut, data); err != nil {
		return "", "", "", fmt.Errorf("rendering invitation html: %w", err)
	}
	return fmt.Sprintf("%s invited you to %s on Calendium", inviter, team), htmlOut.String(), textOut.String(), nil
}

// headerSafe collapses every run of control characters and whitespace
// (CR, LF, TAB, NUL, DEL, U+2028/U+2029, …) to a single space and trims the
// ends, so a user-controlled name can be placed in a one-line header.
func headerSafe(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}), " ")
}
