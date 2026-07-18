package postgres

import (
	"context"
	"errors"
	"testing"

	"calendium/backend/internal/domain"
)

// --- EventTemplateRepo ---------------------------------------------------------

func TestEventTemplateRepoCreateAndGetByID(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")

	created, err := st.EventTemplates().Create(ctx, "u1", domain.EventTemplate{
		Name:            "1:1 Meeting",
		Title:           "1:1",
		Description:     "One-on-one sync",
		Location:        "Conference Room A",
		DurationMinutes: 30,
		AllDay:          false,
		AttendeeEmails:  []string{"person@example.com"},
		AddConferencing: true,
		ReminderMinutes: []int{15},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("Create did not assign an id")
	}

	got, ownerID, err := st.EventTemplates().GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if ownerID != "u1" {
		t.Fatalf("ownerID = %q, want u1", ownerID)
	}
	if got.Name != "1:1 Meeting" || got.DurationMinutes != 30 {
		t.Fatalf("got = %+v", got)
	}
	if len(got.AttendeeEmails) != 1 || got.AttendeeEmails[0] != "person@example.com" {
		t.Fatalf("AttendeeEmails = %v, want [person@example.com]", got.AttendeeEmails)
	}
	if len(got.ReminderMinutes) != 1 || got.ReminderMinutes[0] != 15 {
		t.Fatalf("ReminderMinutes = %v, want [15]", got.ReminderMinutes)
	}
}

func TestEventTemplateRepoListByUserOrdered(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")

	if _, err := st.EventTemplates().Create(ctx, "u1", domain.EventTemplate{Name: "Zeta"}); err != nil {
		t.Fatalf("Create Zeta: %v", err)
	}
	if _, err := st.EventTemplates().Create(ctx, "u1", domain.EventTemplate{Name: "Alpha"}); err != nil {
		t.Fatalf("Create Alpha: %v", err)
	}

	list, err := st.EventTemplates().ListByUser(ctx, "u1")
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(list) != 2 || list[0].Name != "Alpha" || list[1].Name != "Zeta" {
		t.Fatalf("ListByUser = %+v, want [Alpha, Zeta]", list)
	}
}

func TestEventTemplateRepoUpdate(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")

	created, err := st.EventTemplates().Create(ctx, "u1", domain.EventTemplate{
		Name:            "Original",
		DurationMinutes: 30,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	created.Name = "Updated"
	created.DurationMinutes = 60
	created.AttendeeEmails = []string{"new@example.com"}
	created.ReminderMinutes = []int{10, 20}
	if err := st.EventTemplates().Update(ctx, created); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, _, err := st.EventTemplates().GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "Updated" || got.DurationMinutes != 60 {
		t.Fatalf("got = %+v", got)
	}
	if len(got.AttendeeEmails) != 1 || got.AttendeeEmails[0] != "new@example.com" {
		t.Fatalf("AttendeeEmails = %v, want [new@example.com]", got.AttendeeEmails)
	}
	if len(got.ReminderMinutes) != 2 || got.ReminderMinutes[0] != 10 || got.ReminderMinutes[1] != 20 {
		t.Fatalf("ReminderMinutes = %v, want [10, 20]", got.ReminderMinutes)
	}

	if err := st.EventTemplates().Update(ctx, domain.EventTemplate{ID: "nope"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Update unknown: err = %v, want ErrNotFound", err)
	}
}

func TestEventTemplateRepoDelete(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")

	created, err := st.EventTemplates().Create(ctx, "u1", domain.EventTemplate{Name: "to delete"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := st.EventTemplates().Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, err := st.EventTemplates().GetByID(ctx, created.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByID after delete: err = %v, want ErrNotFound", err)
	}
	if err := st.EventTemplates().Delete(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Delete unknown: err = %v, want ErrNotFound", err)
	}
}

func TestEventTemplateRepoIncrementUsage(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")

	created, err := st.EventTemplates().Create(ctx, "u1", domain.EventTemplate{Name: "test"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.UsageCount != 0 {
		t.Fatalf("initial UsageCount = %d, want 0", created.UsageCount)
	}

	if err := st.EventTemplates().IncrementUsage(ctx, created.ID); err != nil {
		t.Fatalf("IncrementUsage: %v", err)
	}

	got, _, err := st.EventTemplates().GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.UsageCount != 1 {
		t.Fatalf("UsageCount after increment = %d, want 1", got.UsageCount)
	}

	if err := st.EventTemplates().IncrementUsage(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("IncrementUsage unknown: err = %v, want ErrNotFound", err)
	}
}

func TestEventTemplateRepoCalendarIDSetsNullOnDelete(t *testing.T) {
	st, db := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	cal := seedCalendar(t, st, acct.ID)

	created, err := st.EventTemplates().Create(ctx, "u1", domain.EventTemplate{
		Name:       "with calendar",
		CalendarID: &cal.ID,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, _, err := st.EventTemplates().GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID before delete: %v", err)
	}
	if got.CalendarID == nil || *got.CalendarID != cal.ID {
		t.Fatalf("CalendarID = %v, want %q", got.CalendarID, cal.ID)
	}

	// Delete the calendar via raw SQL (FK CASCADE with SET NULL)
	_, err = db.ExecContext(ctx, `DELETE FROM calendars WHERE id = $1`, cal.ID)
	if err != nil {
		t.Fatalf("Delete calendar: %v", err)
	}

	// Verify CalendarID is now null
	got, _, err = st.EventTemplates().GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID after calendar delete: %v", err)
	}
	if got.CalendarID != nil {
		t.Fatalf("CalendarID after calendar delete = %v, want nil", got.CalendarID)
	}
}

func TestEventTemplateRepoCrossUserIsolation(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")

	tmpl1, err := st.EventTemplates().Create(ctx, "u1", domain.EventTemplate{Name: "User1Template"})
	if err != nil {
		t.Fatalf("Create u1: %v", err)
	}

	tmpl2, err := st.EventTemplates().Create(ctx, "u2", domain.EventTemplate{Name: "User2Template"})
	if err != nil {
		t.Fatalf("Create u2: %v", err)
	}

	list1, err := st.EventTemplates().ListByUser(ctx, "u1")
	if err != nil {
		t.Fatalf("ListByUser u1: %v", err)
	}
	if len(list1) != 1 || list1[0].ID != tmpl1.ID {
		t.Fatalf("ListByUser(u1) = %+v, want only tmpl1", list1)
	}

	// GetByID should work across users (no auth layer at repo level)
	got, ownerID, err := st.EventTemplates().GetByID(ctx, tmpl2.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if ownerID != "u2" {
		t.Fatalf("ownerID = %q, want u2", ownerID)
	}
	if got.ID != tmpl2.ID {
		t.Fatalf("got.ID = %q, want %q", got.ID, tmpl2.ID)
	}
}

// --- CalendarSetRepo ----------------------------------------------------------

func TestCalendarSetRepoCreateAndGetByID(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	cal1 := seedCalendar(t, st, acct.ID)
	cal2 := seedCalendar(t, st, acct.ID)

	created, err := st.CalendarSets().Create(ctx, "u1", domain.CalendarSet{
		Name:        "Work Calendars",
		CalendarIDs: []string{cal1.ID, cal2.ID},
		Position:    0,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("Create did not assign an id")
	}

	got, ownerID, err := st.CalendarSets().GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if ownerID != "u1" {
		t.Fatalf("ownerID = %q, want u1", ownerID)
	}
	if got.Name != "Work Calendars" || len(got.CalendarIDs) != 2 {
		t.Fatalf("got = %+v", got)
	}
	if got.CalendarIDs[0] != cal1.ID || got.CalendarIDs[1] != cal2.ID {
		t.Fatalf("CalendarIDs = %v, want [%q, %q]", got.CalendarIDs, cal1.ID, cal2.ID)
	}
}

func TestCalendarSetRepoListByUserOrdered(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")

	if _, err := st.CalendarSets().Create(ctx, "u1", domain.CalendarSet{
		Name:     "Second",
		Position: 2,
	}); err != nil {
		t.Fatalf("Create Second: %v", err)
	}
	if _, err := st.CalendarSets().Create(ctx, "u1", domain.CalendarSet{
		Name:     "First",
		Position: 1,
	}); err != nil {
		t.Fatalf("Create First: %v", err)
	}
	if _, err := st.CalendarSets().Create(ctx, "u1", domain.CalendarSet{
		Name:     "Another",
		Position: 1,
	}); err != nil {
		t.Fatalf("Create Another: %v", err)
	}

	list, err := st.CalendarSets().ListByUser(ctx, "u1")
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("len(list) = %d, want 3", len(list))
	}
	// Ordered by position, then name
	if list[0].Position != 1 || (list[0].Name != "Another" && list[1].Name != "Another") {
		t.Fatalf("list[0] = %+v, list[1] = %+v, expected one with position 1, name Another", list[0], list[1])
	}
	if list[2].Position != 2 || list[2].Name != "Second" {
		t.Fatalf("list[2] = %+v, want position 2, name Second", list[2])
	}
}

func TestCalendarSetRepoUpdate(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	cal1 := seedCalendar(t, st, acct.ID)
	cal2 := seedCalendar(t, st, acct.ID)

	created, err := st.CalendarSets().Create(ctx, "u1", domain.CalendarSet{
		Name:        "Original",
		CalendarIDs: []string{cal1.ID},
		Position:    0,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	created.Name = "Updated"
	created.CalendarIDs = []string{cal2.ID}
	created.Position = 5
	if err := st.CalendarSets().Update(ctx, created); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, _, err := st.CalendarSets().GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "Updated" || len(got.CalendarIDs) != 1 || got.CalendarIDs[0] != cal2.ID || got.Position != 5 {
		t.Fatalf("got = %+v", got)
	}

	if err := st.CalendarSets().Update(ctx, domain.CalendarSet{ID: "nope"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Update unknown: err = %v, want ErrNotFound", err)
	}
}

func TestCalendarSetRepoDelete(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")

	created, err := st.CalendarSets().Create(ctx, "u1", domain.CalendarSet{Name: "to delete"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := st.CalendarSets().Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, err := st.CalendarSets().GetByID(ctx, created.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByID after delete: err = %v, want ErrNotFound", err)
	}
	if err := st.CalendarSets().Delete(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Delete unknown: err = %v, want ErrNotFound", err)
	}
}

func TestCalendarSetRepoCrossUserIsolation(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")

	set1, err := st.CalendarSets().Create(ctx, "u1", domain.CalendarSet{Name: "User1Set"})
	if err != nil {
		t.Fatalf("Create u1: %v", err)
	}

	set2, err := st.CalendarSets().Create(ctx, "u2", domain.CalendarSet{Name: "User2Set"})
	if err != nil {
		t.Fatalf("Create u2: %v", err)
	}

	list1, err := st.CalendarSets().ListByUser(ctx, "u1")
	if err != nil {
		t.Fatalf("ListByUser u1: %v", err)
	}
	if len(list1) != 1 || list1[0].ID != set1.ID {
		t.Fatalf("ListByUser(u1) = %+v, want only set1", list1)
	}

	// GetByID should work across users (no auth layer at repo level)
	got, ownerID, err := st.CalendarSets().GetByID(ctx, set2.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if ownerID != "u2" {
		t.Fatalf("ownerID = %q, want u2", ownerID)
	}
	if got.ID != set2.ID {
		t.Fatalf("got.ID = %q, want %q", got.ID, set2.ID)
	}
}
