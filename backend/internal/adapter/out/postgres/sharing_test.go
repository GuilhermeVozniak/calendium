package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

func strp(s string) *string { return &s }

func seedShareWorld(t *testing.T) (*Store, domain.Calendar, domain.Team) {
	t.Helper()
	st, _ := newTestStore(t)
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	seedUser(t, st, "u3")
	acct := seedAccount(t, st, "u1")
	cal := seedCalendar(t, st, acct.ID)
	team, err := NewTeamRepo(st).Create(context.Background(),
		domain.Team{Name: "Crew", CreatedBy: "u1"},
		domain.TeamMember{UserID: "u1", Role: domain.TeamRoleOwner})
	if err != nil {
		t.Fatalf("seed team: %v", err)
	}
	return st, cal, team
}

func TestCalendarShareRepoRoundTrip(t *testing.T) {
	st, cal, team := seedShareWorld(t)
	ctx := context.Background()
	repo := NewCalendarShareRepo(st)

	userShare, err := repo.Create(ctx, domain.CalendarShare{
		CalendarID: cal.ID, GranteeUserID: strp("u2"),
		Permission: domain.PermissionReader, CreatedBy: "u1",
	})
	if err != nil {
		t.Fatalf("create user share: %v", err)
	}
	if userShare.ID == "" || userShare.CreatedAt.IsZero() {
		t.Fatalf("share not fully populated: %+v", userShare)
	}

	// Duplicate (calendar, grantee user) → conflict.
	if _, err := repo.Create(ctx, domain.CalendarShare{
		CalendarID: cal.ID, GranteeUserID: strp("u2"),
		Permission: domain.PermissionEditor, CreatedBy: "u1",
	}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate share err = %v, want ErrConflict", err)
	}

	teamShare, err := repo.Create(ctx, domain.CalendarShare{
		CalendarID: cal.ID, GranteeTeamID: &team.ID, CreatedBy: "u1",
	})
	if err != nil {
		t.Fatalf("create team share: %v", err)
	}
	if teamShare.Permission != domain.PermissionFreeBusy {
		t.Fatalf("default permission = %q, want free_busy", teamShare.Permission)
	}

	byCal, err := repo.ListByCalendar(ctx, cal.ID)
	if err != nil || len(byCal) != 2 {
		t.Fatalf("ListByCalendar = %v, %v; want 2", byCal, err)
	}

	// Grantee resolution: direct only, direct+team, stranger.
	if got, _ := repo.ListForGrantee(ctx, "u2", nil); len(got) != 1 {
		t.Fatalf("ListForGrantee(u2, nil) = %d, want 1", len(got))
	}
	if got, _ := repo.ListForGrantee(ctx, "u2", []string{team.ID}); len(got) != 2 {
		t.Fatalf("ListForGrantee(u2, [team]) = %d, want 2", len(got))
	}
	if got, _ := repo.ListForGrantee(ctx, "u3", nil); len(got) != 0 {
		t.Fatalf("ListForGrantee(u3) = %d, want 0", len(got))
	}

	userShare.Permission = domain.PermissionEditor
	if err := repo.Update(ctx, userShare); err != nil {
		t.Fatalf("update: %v", err)
	}
	byCal, _ = repo.ListByCalendar(ctx, cal.ID)
	for _, sh := range byCal {
		if sh.ID == userShare.ID && sh.Permission != domain.PermissionEditor {
			t.Fatalf("permission not updated: %+v", sh)
		}
	}

	if err := repo.Delete(ctx, userShare.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := repo.Delete(ctx, userShare.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("double delete err = %v, want ErrNotFound", err)
	}
}

func TestCalendarShareConstraints(t *testing.T) {
	st, cal, team := seedShareWorld(t)
	ctx := context.Background()
	repo := NewCalendarShareRepo(st)

	// Both grantees set violates the exactly-one CHECK.
	if _, err := repo.Create(ctx, domain.CalendarShare{
		CalendarID: cal.ID, GranteeUserID: strp("u2"), GranteeTeamID: &team.ID, CreatedBy: "u1",
	}); err == nil {
		t.Fatalf("expected CHECK violation for double grantee")
	}
	// No grantee at all violates it too.
	if _, err := repo.Create(ctx, domain.CalendarShare{
		CalendarID: cal.ID, CreatedBy: "u1",
	}); err == nil {
		t.Fatalf("expected CHECK violation for missing grantee")
	}

	// Deleting the calendar cascades its shares away.
	if _, err := repo.Create(ctx, domain.CalendarShare{
		CalendarID: cal.ID, GranteeUserID: strp("u2"), CreatedBy: "u1",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := st.db.Exec(`DELETE FROM calendars WHERE id = $1`, cal.ID); err != nil {
		t.Fatalf("delete calendar: %v", err)
	}
	if got, _ := repo.ListForGrantee(ctx, "u2", nil); len(got) != 0 {
		t.Fatalf("shares survived calendar delete: %+v", got)
	}
}

func TestAuditRepoRecordAndList(t *testing.T) {
	st, cal, _ := seedShareWorld(t)
	ctx := context.Background()
	repo := NewAuditRepo(st)
	base := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)

	for i, action := range []string{"event.create", "event.update", "event.delete"} {
		if err := repo.Record(ctx, domain.AuditEntry{
			ActorID: "u2", PrincipalID: "u1", Action: action,
			ResourceType: "event", ResourceID: "ev1",
			Metadata:  map[string]any{"calendarId": cal.ID},
			CreatedAt: base.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatalf("record %s: %v", action, err)
		}
	}
	// A foreign principal's entry stays out of u1's listing.
	if err := repo.Record(ctx, domain.AuditEntry{
		ActorID: "u1", PrincipalID: "u3", Action: "event.create",
		ResourceType: "event", ResourceID: "ev9",
	}); err != nil {
		t.Fatalf("record foreign: %v", err)
	}

	got, err := repo.ListByPrincipal(ctx, "u1", 2)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("limit ignored: got %d", len(got))
	}
	if got[0].Action != "event.delete" || got[1].Action != "event.update" {
		t.Fatalf("order wrong: %s, %s", got[0].Action, got[1].Action)
	}
	if got[0].Metadata["calendarId"] != cal.ID {
		t.Fatalf("metadata roundtrip: %+v", got[0].Metadata)
	}
}
