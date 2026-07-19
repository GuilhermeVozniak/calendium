package postgres

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"calendium/backend/internal/domain"
)

// Team booking links (M2.7 Task 14): team_id + booking_link_members
// persistence round-trip against real Postgres (migration 0018).
func TestBookingLinkTeamFieldsRoundTrip(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()

	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	a := seedAccount(t, st, "u1")
	c := seedCalendar(t, st, a.ID)

	teamRepo := NewTeamRepo(st)
	team, err := teamRepo.Create(ctx, domain.Team{ID: "t1", Name: "Sales", CreatedBy: "u1"},
		domain.TeamMember{TeamID: "t1", UserID: "u1", Role: domain.TeamRoleOwner})
	if err != nil {
		t.Fatalf("create team: %v", err)
	}

	link, err := st.BookingLinks().Create(ctx, domain.BookingLink{
		UserID: "u1", Slug: "team-intro", Title: "Team Intro", CalendarID: c.ID,
		DurationMinutes: 30, TimeZone: "UTC",
		Windows: []domain.AvailabilityWindow{{Weekday: 1, Start: "09:00", End: "17:00"}},
		Active:  true,
		TeamID:  &team.ID, MemberUserIDs: []string{"u2", "u1"},
	})
	if err != nil {
		t.Fatalf("create link: %v", err)
	}

	got, err := st.BookingLinks().GetByID(ctx, link.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.TeamID == nil || *got.TeamID != "t1" {
		t.Fatalf("TeamID = %v, want t1", got.TeamID)
	}
	if !reflect.DeepEqual(got.MemberUserIDs, []string{"u1", "u2"}) { // ordered by user_id
		t.Fatalf("MemberUserIDs = %v, want [u1 u2]", got.MemberUserIDs)
	}

	bySlug, err := st.BookingLinks().GetBySlug(ctx, "TEAM-INTRO")
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if !reflect.DeepEqual(bySlug.MemberUserIDs, []string{"u1", "u2"}) {
		t.Fatalf("GetBySlug MemberUserIDs = %v, want [u1 u2]", bySlug.MemberUserIDs)
	}

	list, err := st.BookingLinks().ListByUser(ctx, "u1")
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(list) != 1 || !reflect.DeepEqual(list[0].MemberUserIDs, []string{"u1", "u2"}) {
		t.Fatalf("ListByUser = %+v, want one link with members [u1 u2]", list)
	}

	// Update replaces the member set atomically.
	link.MemberUserIDs = []string{"u2"}
	if err := st.BookingLinks().Update(ctx, link); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err = st.BookingLinks().GetByID(ctx, link.ID)
	if err != nil {
		t.Fatalf("GetByID after update: %v", err)
	}
	if !reflect.DeepEqual(got.MemberUserIDs, []string{"u2"}) {
		t.Fatalf("MemberUserIDs after update = %v, want [u2]", got.MemberUserIDs)
	}

	// A personal link stays team-free with an empty (non-nil) member list.
	personal, err := st.BookingLinks().Create(ctx, domain.BookingLink{
		UserID: "u1", Slug: "solo", Title: "Solo", CalendarID: c.ID,
		DurationMinutes: 30, TimeZone: "UTC",
		Windows: []domain.AvailabilityWindow{{Weekday: 1, Start: "09:00", End: "17:00"}},
		Active:  true,
	})
	if err != nil {
		t.Fatalf("create personal link: %v", err)
	}
	gotPersonal, err := st.BookingLinks().GetByID(ctx, personal.ID)
	if err != nil {
		t.Fatalf("GetByID personal: %v", err)
	}
	if gotPersonal.TeamID != nil || gotPersonal.MemberUserIDs == nil || len(gotPersonal.MemberUserIDs) != 0 {
		t.Fatalf("personal link = %+v, want nil TeamID and empty members", gotPersonal)
	}

	// Deleting the team cascades the team link (ON DELETE CASCADE) but
	// leaves personal links alone.
	if err := teamRepo.Delete(ctx, "t1"); err != nil {
		t.Fatalf("delete team: %v", err)
	}
	if _, err := st.BookingLinks().GetByID(ctx, link.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("team link survived team deletion: err = %v", err)
	}
	if _, err := st.BookingLinks().GetByID(ctx, personal.ID); err != nil {
		t.Fatalf("personal link vanished with the team: %v", err)
	}
}
