package postgres

// Integration tests for TeamThreadActivityRepo (M2.7 Task 10): real SQL for
// the share_read_statuses privacy filter, upsert merge semantics, the RFC
// Message-ID conversation-key resolver, and rfc_message_id persistence on
// the messages table (migration 0014).

import (
	"context"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

func seedTeamWithMembers(t *testing.T, st *Store, teamID, ownerID string, ownerShares bool) *TeamRepo {
	t.Helper()
	tr := NewTeamRepo(st)
	if _, err := tr.Create(context.Background(),
		domain.Team{ID: teamID, Name: "Ops", CreatedBy: ownerID},
		domain.TeamMember{TeamID: teamID, UserID: ownerID, Role: domain.TeamRoleOwner, ShareReadStatuses: ownerShares},
	); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	return tr
}

func addMember(t *testing.T, tr *TeamRepo, teamID, userID string, shares bool) {
	t.Helper()
	if err := tr.UpsertMember(context.Background(), domain.TeamMember{
		TeamID: teamID, UserID: userID, Role: domain.TeamRoleMember, ShareReadStatuses: shares,
	}); err != nil {
		t.Fatalf("seed member %s: %v", userID, err)
	}
}

// The read-side privacy gate: rows exist for u1 (sharing), u2 (sharing) and
// u3 (opted out) — u3 NEVER appears; flipping u2's flag off hides u2's
// already-written rows immediately.
func TestTeamActivityListByConversationFiltersOptedOutMembers(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	u1, u2, u3 := seedUser(t, st, "u1"), seedUser(t, st, "u2"), seedUser(t, st, "u3")
	tr := seedTeamWithMembers(t, st, "team1", u1.ID, true)
	addMember(t, tr, "team1", u2.ID, true)
	addMember(t, tr, "team1", u3.ID, false) // opted out

	repo := NewTeamThreadActivityRepo(st)
	opened := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	for _, uid := range []string{u1.ID, u2.ID, u3.ID} {
		if err := repo.Upsert(ctx, domain.TeamThreadActivity{
			TeamID: "team1", UserID: uid, ConversationKey: "<k@x>", OpenedAt: &opened,
		}); err != nil {
			t.Fatalf("upsert %s: %v", uid, err)
		}
	}

	got, err := repo.ListByConversation(ctx, "team1", "<k@x>")
	if err != nil {
		t.Fatalf("ListByConversation: %v", err)
	}
	if len(got) != 2 || got[0].UserID != "u1" || got[1].UserID != "u2" {
		t.Fatalf("got %+v, want u1+u2 only (u3 opted out)", got)
	}

	// u2 opts out later: their previously-written rows disappear immediately.
	addMember(t, tr, "team1", u2.ID, false)
	got, err = repo.ListByConversation(ctx, "team1", "<k@x>")
	if err != nil {
		t.Fatalf("ListByConversation after opt-out: %v", err)
	}
	if len(got) != 1 || got[0].UserID != "u1" {
		t.Fatalf("got %+v, want only u1 after u2 opted out", got)
	}
}

// Cross-team isolation: the same conversation key in another team is
// invisible from team1.
func TestTeamActivityListByConversationScopedToTeam(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	u1, u2 := seedUser(t, st, "u1"), seedUser(t, st, "u2")
	seedTeamWithMembers(t, st, "team1", u1.ID, true)
	seedTeamWithMembers(t, st, "team2", u2.ID, true)

	repo := NewTeamThreadActivityRepo(st)
	opened := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	if err := repo.Upsert(ctx, domain.TeamThreadActivity{TeamID: "team2", UserID: u2.ID, ConversationKey: "<k@x>", OpenedAt: &opened}); err != nil {
		t.Fatal(err)
	}

	got, err := repo.ListByConversation(ctx, "team1", "<k@x>")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want none (activity belongs to team2)", got)
	}
}

// Upsert merge: an open then a reply keep both timestamps; a later open
// advances opened_at without clearing replied_at.
func TestTeamActivityUpsertMergesOpenAndReply(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	u1 := seedUser(t, st, "u1")
	seedTeamWithMembers(t, st, "team1", u1.ID, true)

	repo := NewTeamThreadActivityRepo(st)
	open1 := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	replied := open1.Add(5 * time.Minute)
	open2 := open1.Add(time.Hour)
	key := "<k@x>"
	if err := repo.Upsert(ctx, domain.TeamThreadActivity{TeamID: "team1", UserID: u1.ID, ConversationKey: key, OpenedAt: &open1}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Upsert(ctx, domain.TeamThreadActivity{TeamID: "team1", UserID: u1.ID, ConversationKey: key, RepliedAt: &replied}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Upsert(ctx, domain.TeamThreadActivity{TeamID: "team1", UserID: u1.ID, ConversationKey: key, OpenedAt: &open2}); err != nil {
		t.Fatal(err)
	}

	got, err := repo.ListByConversation(ctx, "team1", key)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("rows = %d, want 1 (PK merge)", len(got))
	}
	if got[0].OpenedAt == nil || !got[0].OpenedAt.Equal(open2) {
		t.Fatalf("OpenedAt = %v, want advanced to %v", got[0].OpenedAt, open2)
	}
	if got[0].RepliedAt == nil || !got[0].RepliedAt.Equal(replied) {
		t.Fatalf("RepliedAt = %v, want preserved %v", got[0].RepliedAt, replied)
	}
}

// The write-side privacy gate: only memberships with the flag set surface.
func TestTeamActivityListSharingTeamIDs(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	u1 := seedUser(t, st, "u1")
	seedTeamWithMembers(t, st, "team1", u1.ID, true)  // sharing
	seedTeamWithMembers(t, st, "team2", u1.ID, false) // member, opted out

	repo := NewTeamThreadActivityRepo(st)
	got, err := repo.ListSharingTeamIDs(ctx, u1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "team1" {
		t.Fatalf("got %v, want [team1] only", got)
	}
}

// The conversation-key resolver picks the EARLIEST message carrying an
// rfc_message_id, skipping older messages without one; a thread with none
// resolves to "" (no error). Also proves messages.rfc_message_id round-trips
// through the message repo (migration 0014 column + adapter change).
func TestTeamActivityEarliestRFCMessageID(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	u1 := seedUser(t, st, "u1")
	acct := seedAccount(t, st, u1.ID)
	base := time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC)
	th := seedThread(t, st, acct.ID, base)

	mk := func(pmID, rfc string, at time.Time) {
		t.Helper()
		if _, err := st.Messages().Upsert(ctx, domain.Message{
			ThreadID: th.ID, AccountID: acct.ID, ProviderMessageID: pmID,
			From: domain.EmailAddress{Email: "a@x.com"}, Subject: "s",
			SentAt: at, RFCMessageID: rfc,
		}); err != nil {
			t.Fatalf("seed message %s: %v", pmID, err)
		}
	}
	mk("pm0", "", base.Add(-2*time.Hour))            // earliest, but no Message-ID
	mk("pm1", "<first@x.com>", base.Add(-time.Hour)) // earliest WITH one → the key
	mk("pm2", "<later@x.com>", base)

	repo := NewTeamThreadActivityRepo(st)
	key, err := repo.EarliestRFCMessageID(ctx, th.ID)
	if err != nil {
		t.Fatal(err)
	}
	if key != "<first@x.com>" {
		t.Fatalf("key = %q, want <first@x.com>", key)
	}

	// Round-trip: the column survives the message repo's scan.
	m, err := st.Messages().GetByProviderID(ctx, acct.ID, "pm1")
	if err != nil {
		t.Fatal(err)
	}
	if m.RFCMessageID != "<first@x.com>" {
		t.Fatalf("scanned RFCMessageID = %q, want <first@x.com>", m.RFCMessageID)
	}

	// A thread with no Message-ID anywhere resolves gracefully.
	other := seedThread(t, st, acct.ID, base)
	key, err = repo.EarliestRFCMessageID(ctx, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		t.Fatalf("key = %q, want empty for a thread without Message-IDs", key)
	}
}
