package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

func seedMessageForReactions(t *testing.T, st *Store, acctID, threadID string) domain.Message {
	t.Helper()
	m, err := st.Messages().Upsert(context.Background(), domain.Message{
		ThreadID: threadID, AccountID: acctID, ProviderMessageID: newID(),
		From: domain.EmailAddress{Email: "sender@example.com"}, SentAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("seed message: %v", err)
	}
	return m
}

func TestReactionRepoCreateIdempotent(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	th := seedThread(t, st, acct.ID, time.Now())
	msg := seedMessageForReactions(t, st, acct.ID, th.ID)

	first, err := st.Reactions().Create(ctx, domain.Reaction{MessageID: msg.ID, UserID: "u1", Emoji: "👍"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if first.ID == "" {
		t.Fatalf("Create returned empty ID")
	}
	if first.Delivery != "local" {
		t.Fatalf("Delivery = %q, want local (default)", first.Delivery)
	}

	second, err := st.Reactions().Create(ctx, domain.Reaction{MessageID: msg.ID, UserID: "u1", Emoji: "👍"})
	if err != nil {
		t.Fatalf("duplicate Create should not error: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("duplicate Create returned different id: got %s want %s", second.ID, first.ID)
	}
}

func TestReactionRepoListByMessages(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	th := seedThread(t, st, acct.ID, time.Now())
	m1 := seedMessageForReactions(t, st, acct.ID, th.ID)
	m2 := seedMessageForReactions(t, st, acct.ID, th.ID)

	if _, err := st.Reactions().Create(ctx, domain.Reaction{MessageID: m1.ID, UserID: "u1", Emoji: "👍"}); err != nil {
		t.Fatalf("Create 1: %v", err)
	}
	if _, err := st.Reactions().Create(ctx, domain.Reaction{MessageID: m1.ID, UserID: "u1", Emoji: "🎉"}); err != nil {
		t.Fatalf("Create 2: %v", err)
	}
	if _, err := st.Reactions().Create(ctx, domain.Reaction{MessageID: m2.ID, UserID: "u1", Emoji: "👍"}); err != nil {
		t.Fatalf("Create 3: %v", err)
	}

	grouped, err := st.Reactions().ListByMessages(ctx, []string{m1.ID, m2.ID})
	if err != nil {
		t.Fatalf("ListByMessages: %v", err)
	}
	if len(grouped[m1.ID]) != 2 {
		t.Fatalf("grouped[m1] = %+v, want 2 reactions", grouped[m1.ID])
	}
	if len(grouped[m2.ID]) != 1 {
		t.Fatalf("grouped[m2] = %+v, want 1 reaction", grouped[m2.ID])
	}
}

func TestReactionRepoDeleteByEmoji(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	acct := seedAccount(t, st, "u1")
	th := seedThread(t, st, acct.ID, time.Now())
	msg := seedMessageForReactions(t, st, acct.ID, th.ID)

	if _, err := st.Reactions().Create(ctx, domain.Reaction{MessageID: msg.ID, UserID: "u1", Emoji: "👍"}); err != nil {
		t.Fatalf("Create u1: %v", err)
	}
	if _, err := st.Reactions().Create(ctx, domain.Reaction{MessageID: msg.ID, UserID: "u2", Emoji: "👍"}); err != nil {
		t.Fatalf("Create u2: %v", err)
	}

	if err := st.Reactions().DeleteByEmoji(ctx, msg.ID, "u1", "👍"); err != nil {
		t.Fatalf("DeleteByEmoji: %v", err)
	}

	grouped, err := st.Reactions().ListByMessages(ctx, []string{msg.ID})
	if err != nil {
		t.Fatalf("ListByMessages: %v", err)
	}
	if len(grouped[msg.ID]) != 1 || grouped[msg.ID][0].UserID != "u2" {
		t.Fatalf("u2's reaction should remain untouched: %+v", grouped[msg.ID])
	}

	if err := st.Reactions().DeleteByEmoji(ctx, msg.ID, "u1", "👍"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second delete err = %v, want ErrNotFound", err)
	}
}
