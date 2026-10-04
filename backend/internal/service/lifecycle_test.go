package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- local fakes (lifecycle-prefixed; the shared fakes carry no call log) ---

type lifecycleCancel struct {
	ID          string
	Immediately bool
}

// lifecyclePayments embeds the port so only CancelSubscription is
// implemented; Purge never calls anything else (a nil embed would panic).
type lifecyclePayments struct {
	port.Payments
	cancelCalls []lifecycleCancel
	cancelErr   error
	log         *[]string
}

func (p *lifecyclePayments) CancelSubscription(_ context.Context, id string, immediately bool) error {
	p.cancelCalls = append(p.cancelCalls, lifecycleCancel{ID: id, Immediately: immediately})
	*p.log = append(*p.log, "cancel:"+id)
	return p.cancelErr
}

type loggingUserRepo struct {
	*fakeUserRepo
	log *[]string
}

func (r *loggingUserRepo) Delete(ctx context.Context, id string) error {
	*r.log = append(*r.log, "users.delete:"+id)
	return r.fakeUserRepo.Delete(ctx, id)
}

type loggingTeamRepo struct {
	*fakeTeamRepo
	log *[]string
}

func (r *loggingTeamRepo) ListMembers(ctx context.Context, teamID string) ([]domain.TeamMember, error) {
	*r.log = append(*r.log, "teams.listMembers:"+teamID)
	return r.fakeTeamRepo.ListMembers(ctx, teamID)
}

func (r *loggingTeamRepo) Delete(ctx context.Context, id string) error {
	*r.log = append(*r.log, "teams.delete:"+id)
	return r.fakeTeamRepo.Delete(ctx, id)
}

type lifecycleFixture struct {
	svc      *UserLifecycleService
	users    *loggingUserRepo
	teams    *loggingTeamRepo
	delegs   *delegRepoFake
	subs     *fakeSubscriptionRepo
	payments *lifecyclePayments
	tx       *fakeTxRunner
	log      []string
}

var lifecycleNow = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

func newLifecycleFixture(t *testing.T) *lifecycleFixture {
	t.Helper()
	f := &lifecycleFixture{}
	f.users = &loggingUserRepo{fakeUserRepo: newUserRepo(), log: &f.log}
	f.teams = &loggingTeamRepo{fakeTeamRepo: newTeamRepo(), log: &f.log}
	f.delegs = newDelegRepoFake()
	f.subs = newSubscriptionRepo()
	f.payments = &lifecyclePayments{log: &f.log}
	f.tx = newTxRunner()
	f.svc = NewUserLifecycleService(UserLifecycleDeps{
		Users: f.users, Teams: f.teams, Delegations: f.delegs, Subscriptions: f.subs,
		Payments: f.payments, Tx: f.tx, Clock: newClock(lifecycleNow),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	for _, id := range []string{"u1", "u2", "u3"} {
		if _, err := f.users.Upsert(context.Background(), domain.User{ID: id, Email: id + "@example.com"}); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// addTeam creates id/name with members[0] as the creating owner and upserts
// the rest.
func (f *lifecycleFixture) addTeam(t *testing.T, id, name string, members ...domain.TeamMember) {
	t.Helper()
	ctx := context.Background()
	first := members[0]
	first.TeamID = id
	if _, err := f.teams.Create(ctx, domain.Team{ID: id, Name: name, CreatedBy: first.UserID}, first); err != nil {
		t.Fatal(err)
	}
	for _, m := range members[1:] {
		m.TeamID = id
		if err := f.teams.UpsertMember(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
}

func owner(userID string) domain.TeamMember {
	return domain.TeamMember{UserID: userID, Role: domain.TeamRoleOwner}
}

func member(userID string) domain.TeamMember {
	return domain.TeamMember{UserID: userID, Role: domain.TeamRoleMember}
}

func indexOf(log []string, entry string) int {
	for i, e := range log {
		if e == entry {
			return i
		}
	}
	return -1
}

func firstIndexWithPrefix(log []string, prefix string) int {
	for i, e := range log {
		if strings.HasPrefix(e, prefix) {
			return i
		}
	}
	return -1
}

func TestPurgeOrder(t *testing.T) {
	f := newLifecycleFixture(t)
	ctx := context.Background()
	f.addTeam(t, "shared", "Shared", owner("u1"), owner("u2"))
	if err := f.subs.Upsert(ctx, domain.Subscription{UserID: "u1", Status: domain.SubscriptionActive, BillingSubscriptionID: "sub_1"}); err != nil {
		t.Fatal(err)
	}

	report, err := f.svc.Purge(ctx, "u1")
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if len(f.payments.cancelCalls) != 1 || f.payments.cancelCalls[0] != (lifecycleCancel{ID: "sub_1", Immediately: true}) {
		t.Fatalf("cancel calls = %+v, want exactly one (sub_1, immediately=true)", f.payments.cancelCalls)
	}
	check := indexOf(f.log, "teams.listMembers:shared")
	cancel := indexOf(f.log, "cancel:sub_1")
	del := indexOf(f.log, "users.delete:u1")
	if check < 0 || cancel < 0 || del < 0 || !(check < cancel && cancel < del) {
		t.Fatalf("order = %v, want team check < cancel < delete", f.log)
	}
	if f.tx.calls != 1 {
		t.Fatalf("tx calls = %d, want 1", f.tx.calls)
	}
	if !report.SubscriptionCanceled || report.TeamsDeleted != 0 {
		t.Fatalf("report = %+v", report)
	}
	if _, err := f.users.GetByID(ctx, "u1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("user still present after purge: %v", err)
	}
	if _, err := f.teams.GetByID(ctx, "shared"); err != nil {
		t.Fatalf("co-owned team must survive: %v", err)
	}
}

func TestPurgePaddleFailureAborts(t *testing.T) {
	f := newLifecycleFixture(t)
	ctx := context.Background()
	f.payments.cancelErr = errors.New("paddle: 503")
	if err := f.subs.Upsert(ctx, domain.Subscription{UserID: "u1", Status: domain.SubscriptionPastDue, BillingSubscriptionID: "sub_1"}); err != nil {
		t.Fatal(err)
	}
	d, err := f.delegs.Create(ctx, domain.Delegation{PrincipalID: "u1", AssistantID: "u2", Scopes: []domain.DelegationScope{domain.ScopeMailRead}, Status: domain.DelegationActive})
	if err != nil {
		t.Fatal(err)
	}

	_, err = f.svc.Purge(ctx, "u1")
	if !errors.Is(err, domain.ErrBillingUnavailable) {
		t.Fatalf("err = %v, want ErrBillingUnavailable", err)
	}
	if f.tx.calls != 0 {
		t.Fatal("no transaction may start when the cancel fails")
	}
	if i := firstIndexWithPrefix(f.log, "users.delete"); i >= 0 {
		t.Fatalf("rows were deleted after a cancel failure: %v", f.log)
	}
	if _, err := f.users.GetByID(ctx, "u1"); err != nil {
		t.Fatalf("user must remain: %v", err)
	}
	got, _ := f.delegs.GetByID(ctx, d.ID)
	if got.Status != domain.DelegationActive {
		t.Fatalf("delegation status = %q, want active (untouched)", got.Status)
	}
}

func TestPurgeSkipsCancelWithoutLiveSubscription(t *testing.T) {
	cases := []struct {
		name string
		sub  *domain.Subscription
	}{
		{"no row", nil},
		{"status none with id", &domain.Subscription{Status: domain.SubscriptionNone, BillingSubscriptionID: "sub_x"}},
		{"canceled with id", &domain.Subscription{Status: domain.SubscriptionCanceled, BillingSubscriptionID: "sub_x"}},
		{"active without id", &domain.Subscription{Status: domain.SubscriptionActive}},
		{"trialing without id", &domain.Subscription{Status: domain.SubscriptionTrialing}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newLifecycleFixture(t)
			ctx := context.Background()
			if tc.sub != nil {
				s := *tc.sub
				s.UserID = "u1"
				if err := f.subs.Upsert(ctx, s); err != nil {
					t.Fatal(err)
				}
			}
			report, err := f.svc.Purge(ctx, "u1")
			if err != nil {
				t.Fatalf("Purge: %v", err)
			}
			if len(f.payments.cancelCalls) != 0 || report.SubscriptionCanceled {
				t.Fatalf("cancel must be skipped: calls=%v report=%+v", f.payments.cancelCalls, report)
			}
			if _, err := f.users.GetByID(ctx, "u1"); !errors.Is(err, domain.ErrNotFound) {
				t.Fatal("user must still be purged")
			}
		})
	}
}

func TestPurgeCancelsTrialingWithSubscriptionID(t *testing.T) {
	f := newLifecycleFixture(t)
	ctx := context.Background()
	if err := f.subs.Upsert(ctx, domain.Subscription{UserID: "u1", Status: domain.SubscriptionTrialing, BillingSubscriptionID: "sub_t"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Purge(ctx, "u1"); err != nil {
		t.Fatal(err)
	}
	if len(f.payments.cancelCalls) != 1 || f.payments.cancelCalls[0].ID != "sub_t" {
		t.Fatalf("cancel calls = %+v, want sub_t", f.payments.cancelCalls)
	}
}

func TestPurgeOwnsTeamsRefused(t *testing.T) {
	f := newLifecycleFixture(t)
	ctx := context.Background()
	f.addTeam(t, "design", "Design", owner("u1"), member("u2"))
	if err := f.subs.Upsert(ctx, domain.Subscription{UserID: "u1", Status: domain.SubscriptionActive, BillingSubscriptionID: "sub_1"}); err != nil {
		t.Fatal(err)
	}

	_, err := f.svc.Purge(ctx, "u1")
	if !errors.Is(err, domain.ErrOwnsTeams) {
		t.Fatalf("err = %v, want ErrOwnsTeams", err)
	}
	var typed *domain.OwnsTeamsError
	if !errors.As(err, &typed) || len(typed.Teams) != 1 || typed.Teams[0] != (domain.TeamRef{ID: "design", Name: "Design"}) {
		t.Fatalf("owns-teams payload = %+v", typed)
	}
	if len(f.payments.cancelCalls) != 0 {
		t.Fatal("the team check must run before the subscription cancel")
	}
	if f.tx.calls != 0 {
		t.Fatal("nothing may be deleted")
	}
	if _, err := f.users.GetByID(ctx, "u1"); err != nil {
		t.Fatalf("user must remain: %v", err)
	}
}

func TestPurgeCoOwnerAndPlainMemberProceed(t *testing.T) {
	for _, role := range []domain.TeamMember{owner("u1"), member("u1")} {
		t.Run(string(role.Role), func(t *testing.T) {
			f := newLifecycleFixture(t)
			ctx := context.Background()
			f.addTeam(t, "shared", "Shared", owner("u2"), role)
			if _, err := f.svc.Purge(ctx, "u1"); err != nil {
				t.Fatalf("Purge as %s: %v", role.Role, err)
			}
			if _, err := f.teams.GetByID(ctx, "shared"); err != nil {
				t.Fatalf("team must survive: %v", err)
			}
			if _, err := f.users.GetByID(ctx, "u1"); !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("user must be purged: %v", err)
			}
			// The membership row itself goes by SQL cascade from users.Delete —
			// proven in postgres TestPurgeCascadeCoverage, not by the fake.
		})
	}
}

// Review Focus 5: a sole-member team plus a blocking team → refusal lists only
// the blocking team and the sole-member team is untouched.
func TestPurgeOwnsTeamsRefusedKeepsSoleMemberTeams(t *testing.T) {
	f := newLifecycleFixture(t)
	ctx := context.Background()
	f.addTeam(t, "solo", "Solo", owner("u1"))
	f.addTeam(t, "design", "Design", owner("u1"), member("u2"))

	_, err := f.svc.Purge(ctx, "u1")
	var typed *domain.OwnsTeamsError
	if !errors.As(err, &typed) || len(typed.Teams) != 1 || typed.Teams[0].ID != "design" {
		t.Fatalf("err = %v, want owns_teams listing only design", err)
	}
	if _, err := f.teams.GetByID(ctx, "solo"); err != nil {
		t.Fatalf("sole-member team must survive a refused purge: %v", err)
	}
	if i := firstIndexWithPrefix(f.log, "teams.delete"); i >= 0 {
		t.Fatalf("no team may be deleted on refusal: %v", f.log)
	}
}

func TestPurgeDeletesSoleMemberTeams(t *testing.T) {
	f := newLifecycleFixture(t)
	ctx := context.Background()
	f.addTeam(t, "solo", "Solo", owner("u1"))
	f.addTeam(t, "shared", "Shared", owner("u2"), member("u1"))

	report, err := f.svc.Purge(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if report.TeamsDeleted != 1 {
		t.Fatalf("TeamsDeleted = %d, want 1", report.TeamsDeleted)
	}
	if _, err := f.teams.GetByID(ctx, "solo"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("solo team must be deleted: %v", err)
	}
	if _, err := f.teams.GetMember(ctx, "shared", "u2"); err != nil {
		t.Fatalf("u2's membership of the shared team must survive: %v", err)
	}
	if del, user := indexOf(f.log, "teams.delete:solo"), indexOf(f.log, "users.delete:u1"); del < 0 || user < 0 || del > user {
		t.Fatalf("teams must be deleted before the users row: %v", f.log)
	}
}

func TestPurgeRevokesDelegationsBothWays(t *testing.T) {
	f := newLifecycleFixture(t)
	ctx := context.Background()
	asPrincipal, err := f.delegs.Create(ctx, domain.Delegation{PrincipalID: "u1", AssistantID: "u2", Scopes: []domain.DelegationScope{domain.ScopeMailRead}, Status: domain.DelegationActive})
	if err != nil {
		t.Fatal(err)
	}
	asAssistant, err := f.delegs.Create(ctx, domain.Delegation{PrincipalID: "u3", AssistantID: "u1", Scopes: []domain.DelegationScope{domain.ScopeCalendarRead}, Status: domain.DelegationPending})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Purge(ctx, "u1"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{asPrincipal.ID, asAssistant.ID} {
		got, err := f.delegs.GetByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != domain.DelegationRevoked || got.RevokedAt == nil || !got.RevokedAt.Equal(lifecycleNow) {
			t.Fatalf("delegation %s = %+v, want revoked at %v", id, got, lifecycleNow)
		}
	}
}

func TestPurgeUnknownUserIsNoop(t *testing.T) {
	f := newLifecycleFixture(t)
	report, err := f.svc.Purge(context.Background(), "ghost")
	if err != nil {
		t.Fatalf("Purge(ghost) = %v, want nil (idempotent)", err)
	}
	if report != (port.PurgeReport{}) || f.tx.calls != 0 || len(f.payments.cancelCalls) != 0 || len(f.log) != 0 {
		t.Fatalf("unknown user must touch nothing: report=%+v tx=%d log=%v", report, f.tx.calls, f.log)
	}
}

func TestPurgeSelfHostedWithoutPayments(t *testing.T) {
	f := newLifecycleFixture(t)
	ctx := context.Background()
	f.svc = NewUserLifecycleService(UserLifecycleDeps{
		Users: f.users, Teams: f.teams, Delegations: f.delegs, Subscriptions: f.subs,
		Payments: nil, Tx: f.tx, Clock: newClock(lifecycleNow),
	})
	if err := f.subs.Upsert(ctx, domain.Subscription{UserID: "u1", Status: domain.SubscriptionActive, BillingSubscriptionID: "sub_1"}); err != nil {
		t.Fatal(err)
	}
	report, err := f.svc.Purge(ctx, "u1")
	if err != nil || report.SubscriptionCanceled {
		t.Fatalf("self-hosted purge = (%+v, %v), want success with no cancel", report, err)
	}
}

// raceTx promotes u1 to sole owner right before the transaction body runs,
// simulating a co-owner leaving between the pre-check and the tx.
type raceTx struct {
	teams  *loggingTeamRepo
	called int
}

func (r *raceTx) RunInTx(ctx context.Context, fn func(context.Context) error) error {
	r.called++
	_ = r.teams.UpsertMember(ctx, domain.TeamMember{TeamID: "shared", UserID: "u2", Role: domain.TeamRoleMember})
	return fn(ctx)
}

func TestPurgeRaceGuardRefusesInsideTx(t *testing.T) {
	f := newLifecycleFixture(t)
	ctx := context.Background()
	f.addTeam(t, "shared", "Shared", owner("u1"), owner("u2"))
	tx := &raceTx{teams: f.teams}
	f.svc = NewUserLifecycleService(UserLifecycleDeps{
		Users: f.users, Teams: f.teams, Delegations: f.delegs, Subscriptions: f.subs,
		Payments: f.payments, Tx: tx, Clock: newClock(lifecycleNow),
	})
	_, err := f.svc.Purge(ctx, "u1")
	if !errors.Is(err, domain.ErrOwnsTeams) {
		t.Fatalf("err = %v, want ErrOwnsTeams from the in-tx re-check", err)
	}
	if tx.called != 1 {
		t.Fatalf("tx called %d times, want 1", tx.called)
	}
	if _, err := f.users.GetByID(ctx, "u1"); err != nil {
		t.Fatalf("user must remain after the tx rolled back: %v", err)
	}
}

func (r *loggingUserRepo) Tombstone(ctx context.Context, id string) error {
	entry := "users.tombstone:" + id
	if !inFakeTx(ctx) {
		entry = "users.tombstone-outside-tx:" + id
	}
	*r.log = append(*r.log, entry)
	return r.fakeUserRepo.Tombstone(ctx, id)
}

// The purge writes the deleted-user tombstone inside the same transaction
// as the users delete, so a still-valid JWT cannot re-create the row.
func TestPurgeWritesTombstoneInTx(t *testing.T) {
	f := newLifecycleFixture(t)
	ctx := context.Background()
	if _, err := f.svc.Purge(ctx, "u1"); err != nil {
		t.Fatal(err)
	}
	if !f.users.tombstones["u1"] {
		t.Fatal("u1 must be tombstoned after purge")
	}
	tomb, del := indexOf(f.log, "users.tombstone:u1"), indexOf(f.log, "users.delete:u1")
	if tomb < 0 || del < 0 {
		t.Fatalf("log = %v, want tombstone (inside the tx) and delete", f.log)
	}
	if f.tx.calls != 1 {
		t.Fatalf("tx calls = %d, want 1", f.tx.calls)
	}
	// A re-provision attempt for the purged subject is refused.
	users := NewUserService(f.users.fakeUserRepo, newUserPreferencesRepo(), newClock(lifecycleNow))
	if _, err := users.EnsureUser(ctx, port.Identity{Subject: "u1", Email: "u1@example.com"}); !errors.Is(err, domain.ErrUnauthorized) || !errors.Is(err, domain.ErrUserDeleted) {
		t.Fatalf("EnsureUser(purged) = %v, want ErrUserDeleted (401)", err)
	}
	if _, err := f.users.GetByID(ctx, "u1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("purged user re-created: %v", err)
	}
}

func TestPurgeRefusedWritesNoTombstone(t *testing.T) {
	f := newLifecycleFixture(t)
	f.addTeam(t, "design", "Design", owner("u1"), member("u2"))
	if _, err := f.svc.Purge(context.Background(), "u1"); !errors.Is(err, domain.ErrOwnsTeams) {
		t.Fatalf("err = %v, want ErrOwnsTeams", err)
	}
	if f.users.tombstones["u1"] {
		t.Fatal("a refused purge must not tombstone the user")
	}
}

func TestEnsureUserTombstonedSubjectNotRecreated(t *testing.T) {
	ctx := context.Background()
	users := newUserRepo()
	if err := users.Tombstone(ctx, "gone"); err != nil {
		t.Fatal(err)
	}
	svc := NewUserService(users, newUserPreferencesRepo(), newClock(lifecycleNow))
	_, err := svc.EnsureUser(ctx, port.Identity{Subject: "gone", Email: "gone@example.com"})
	if !errors.Is(err, domain.ErrUserDeleted) || !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("EnsureUser = %v, want ErrUserDeleted wrapping ErrUnauthorized", err)
	}
	if _, ok := users.byID["gone"]; ok {
		t.Fatal("tombstoned subject must not get a users row")
	}
	// Any other subject still provisions normally.
	if _, err := svc.EnsureUser(ctx, port.Identity{Subject: "fresh", Email: "f@example.com"}); err != nil {
		t.Fatalf("EnsureUser(fresh) = %v", err)
	}
}

func (r *loggingTeamRepo) LockMembershipsForUpdate(ctx context.Context, userID string) error {
	entry := "teams.lock:" + userID
	if !inFakeTx(ctx) {
		entry = "teams.lock-outside-tx:" + userID
	}
	*r.log = append(*r.log, entry)
	return r.fakeTeamRepo.LockMembershipsForUpdate(ctx, userID)
}

// Review Minor 5: the in-transaction team re-check runs under a row lock on
// the user's teams and their member rows, taken before the re-plan, so an
// invitation accepted (or a co-owner leaving) concurrently cannot slip
// between the re-check and the deletes.
func TestPurgeLocksTeamsBeforeRecheck(t *testing.T) {
	f := newLifecycleFixture(t)
	f.addTeam(t, "solo", "Solo", owner("u1"))
	if _, err := f.svc.Purge(context.Background(), "u1"); err != nil {
		t.Fatal(err)
	}
	lock := indexOf(f.log, "teams.lock:u1")
	if lock < 0 {
		t.Fatalf("log = %v, want teams.lock:u1 inside the tx", f.log)
	}
	// The second (in-tx) re-plan lists members after the lock.
	recheck := -1
	for i := lock + 1; i < len(f.log); i++ {
		if f.log[i] == "teams.listMembers:solo" {
			recheck = i
			break
		}
	}
	del := indexOf(f.log, "teams.delete:solo")
	if recheck < 0 || del < recheck {
		t.Fatalf("order = %v, want lock < re-check < delete", f.log)
	}
}
