package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

type exportFixture struct {
	svc      *ExportService
	users    *fakeUserRepo
	accounts *fakeAccountRepo
	cals     *fakeCalendarRepo
	events   *fakeEventRepo
	threads  *fakeThreadRepo
	messages *fakeMessageRepo
	snippets *fakeSnippetRepo
	exports  *fakeUserExportRepo
	clock    *fakeClock
	comments *fakeCommentRepo
	calSubs  *fakeCalSubRepo
	classif  *fakeClassifierRepo
	voice    *fakeVoiceProfileRepo
	links    *fakeBookingLinkRepo
	bookings *fakeBookingRepo
}

var exportNow = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

func newExportFixture(t *testing.T) *exportFixture {
	t.Helper()
	f := &exportFixture{
		users: newUserRepo(), accounts: newAccountRepo(), cals: newCalendarRepo(), events: newEventRepo(),
		threads: newThreadRepo(), messages: newMessageRepo(), snippets: newSnippetRepo(),
		exports: newUserExportRepo(), clock: newClock(exportNow),
		comments: newFakeCommentRepo(), calSubs: newFakeCalSubRepo(), classif: newClassifierRepo(),
		voice: newVoiceProfileRepo(), links: newBookingLinkRepo(),
	}
	f.bookings = newBookingRepo(f.links)
	f.cals.accounts = f.accounts
	f.events.calendars = f.cals
	f.events.accounts = f.accounts
	f.svc = NewExportService(ExportDeps{
		Users: f.users, Accounts: f.accounts, Calendars: f.cals, Events: f.events, Threads: f.threads,
		Messages: f.messages, Drafts: newDraftRepo(f.accounts), Snippets: f.snippets,
		Templates: newEventTemplateRepo(), Sets: newCalendarSetRepo(), Tasks: newTaskRepo(),
		Links: f.links, Bookings: f.bookings, Polls: newPollRepo(), Labels: newLabelRepo(),
		EventNotes: newFakeEventNoteRepo(), Prefs: newPrefsRepo(), Preferences: newUserPreferencesRepo(),
		CalendarPrefs: newCalendarPrefsRepo(), Settings: newUserSettingsRepo(), Exports: f.exports, Clock: f.clock,
		Comments: f.comments, CalendarSubscriptions: f.calSubs, Classifiers: f.classif, VoiceProfiles: f.voice,
	})
	ctx := context.Background()
	if _, err := f.users.Upsert(ctx, domain.User{ID: "u1", Email: "u1@example.com", CreatedAt: exportNow}); err != nil {
		t.Fatal(err)
	}
	return f
}

// seedEverything puts one row in each collection the export reads.
func (f *exportFixture) seedEverything(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if _, err := f.accounts.Create(ctx, domain.ConnectedAccount{ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "me@example.com", Status: domain.AccountActive, CreatedAt: exportNow}); err != nil {
		t.Fatal(err)
	}
	if err := f.accounts.SaveTokens(ctx, "a1", port.TokenSet{AccessToken: "SECRET-ACCESS", RefreshToken: "SECRET-REFRESH", ExpiresAt: exportNow.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.cals.Upsert(ctx, domain.Calendar{ID: "c1", AccountID: "a1", ProviderCalendarID: "pc1", Name: "Primary"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.events.Upsert(ctx, domain.Event{ID: "e1", CalendarID: "c1", Title: "Standup", Start: exportNow, End: exportNow.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.threads.Upsert(ctx, domain.Thread{ID: "t1", AccountID: "a1", Subject: "Hello", LastMessageAt: exportNow}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.messages.Upsert(ctx, domain.Message{
		ID: "m1", ThreadID: "t1", AccountID: "a1", Subject: "Hello", BodyHTML: "<p>body text</p>",
		Attachments: []domain.Attachment{{ID: "att1", Filename: "deck.pdf", MimeType: "application/pdf", SizeBytes: 3, ProviderAttachmentID: "PROVIDER-ATT"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.snippets.Create(ctx, domain.Snippet{ID: "s1", UserID: "u1", Name: "Thanks", BodyHTML: "<p>thanks</p>"}); err != nil {
		t.Fatal(err)
	}
	team := "team1"
	if _, err := f.snippets.Create(ctx, domain.Snippet{ID: "s2", UserID: "u1", TeamID: &team, Name: "TeamMine", BodyHTML: "<p>mine</p>"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.snippets.Create(ctx, domain.Snippet{ID: "s3", UserID: "u2", TeamID: &team, Name: "TeamTheirs", BodyHTML: "<p>theirs</p>"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.comments.Create(ctx, domain.Comment{ID: "cm1", ThreadID: "t1", TeamID: team, AuthorID: "u1", Body: "my comment", CreatedAt: exportNow}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.comments.Create(ctx, domain.Comment{ID: "cm2", ThreadID: "t1", TeamID: team, AuthorID: "u2", Body: "their comment", CreatedAt: exportNow}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.calSubs.Create(ctx, domain.CalendarSubscription{ID: "sub1", UserID: "u1", URL: "https://example.com/mine.ics", Name: "Holidays"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.calSubs.Create(ctx, domain.CalendarSubscription{ID: "sub2", UserID: "u2", URL: "https://example.com/theirs.ics", Name: "Theirs"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.classif.Create(ctx, domain.AiClassifier{ID: "cl1", UserID: "u1", Name: "Receipts", Prompt: "my prompt", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.classif.Create(ctx, domain.AiClassifier{ID: "cl2", UserID: "u2", Name: "Other", Prompt: "their prompt"}); err != nil {
		t.Fatal(err)
	}
	if err := f.voice.Upsert(ctx, domain.VoiceProfile{UserID: "u1", Profile: "Short and warm.", SampleCount: 7, Model: "m", UpdatedAt: exportNow}); err != nil {
		t.Fatal(err)
	}
	if err := f.voice.Upsert(ctx, domain.VoiceProfile{UserID: "u2", Profile: "THEIR VOICE", UpdatedAt: exportNow}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.links.Create(ctx, domain.BookingLink{ID: "l1", UserID: "u1", Slug: "mine", Title: "Call"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.links.Create(ctx, domain.BookingLink{ID: "l2", UserID: "u2", Slug: "theirs", Title: "Theirs"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.bookings.CreateHold(ctx, domain.Booking{ID: "b1", LinkID: "l1", InviteeEmail: "guest@example.com", Start: exportNow, End: exportNow.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.bookings.CreateHold(ctx, domain.Booking{ID: "b2", LinkID: "l2", InviteeEmail: "theirguest@example.com", Start: exportNow, End: exportNow.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
}

func exportToZip(t *testing.T, f *exportFixture) (map[string][]byte, error) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	err := f.svc.Export(context.Background(), "u1", zw)
	if cerr := zw.Close(); cerr != nil {
		t.Fatal(cerr)
	}
	if err != nil {
		return nil, err
	}
	zr, zerr := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if zerr != nil {
		t.Fatalf("zip.NewReader: %v", zerr)
	}
	files := map[string][]byte{}
	for _, zf := range zr.File {
		rc, err := zf.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[zf.Name] = b
	}
	return files, nil
}

func TestExportWritesEveryFile(t *testing.T) {
	f := newExportFixture(t)
	f.seedEverything(t)
	files, err := exportToZip(t, f)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	want := []string{
		"profile.json", "accounts.json", "calendars.json", "events.json", "threads/t1.json",
		"drafts.json", "snippets.json", "templates.json", "calendar-sets.json", "tasks.json",
		"booking-links.json", "bookings.json", "polls.json", "settings.json", "labels.json", "event-notes.json",
		"team-snippets.json", "thread-comments.json", "calendar-subscriptions.json", "classifiers.json", "voice-profile.json",
	}
	got := make([]string, 0, len(files))
	for name := range files {
		got = append(got, name)
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("zip entries = %v\nwant        %v", got, want)
	}
	for name, body := range files {
		if !json.Valid(body) {
			t.Fatalf("%s is not valid JSON: %s", name, body)
		}
	}
	accounts := string(files["accounts.json"])
	for _, forbidden := range []string{"accessToken", "refreshToken", "SECRET-ACCESS", "SECRET-REFRESH", "scopes", "vipSenders"} {
		if strings.Contains(accounts, forbidden) {
			t.Fatalf("accounts.json leaks %q: %s", forbidden, accounts)
		}
	}
	for _, required := range []string{`"id": "a1"`, `"provider": "google"`, `"email": "me@example.com"`, `"status": "active"`, `"createdAt"`} {
		if !strings.Contains(accounts, required) {
			t.Fatalf("accounts.json missing %s: %s", required, accounts)
		}
	}
	thread := string(files["threads/t1.json"])
	for _, required := range []string{`"thread"`, `"messages"`, `"bodyHtml": "<p>body text</p>"`, `"filename": "deck.pdf"`, `"sizeBytes": 3`} {
		if !strings.Contains(thread, required) {
			t.Fatalf("threads/t1.json missing %s: %s", required, thread)
		}
	}
	if strings.Contains(thread, "PROVIDER-ATT") || strings.Contains(thread, "providerAttachmentId") {
		t.Fatalf("threads/t1.json leaks provider attachment ids: %s", thread)
	}
	if !strings.Contains(string(files["snippets.json"]), `"name": "Thanks"`) {
		t.Fatalf("snippets.json = %s", files["snippets.json"])
	}
	if !strings.Contains(string(files["profile.json"]), `"email": "u1@example.com"`) {
		t.Fatalf("profile.json = %s", files["profile.json"])
	}
	settings := string(files["settings.json"])
	for _, key := range []string{`"prefs"`, `"preferences"`, `"calendarPrefs"`, `"settings"`, `"aiBackground": true`} {
		if !strings.Contains(settings, key) {
			t.Fatalf("settings.json missing %s: %s", key, settings)
		}
	}
	if !strings.Contains(string(files["events.json"]), `"title": "Standup"`) {
		t.Fatalf("events.json = %s", files["events.json"])
	}
}

func TestExportThrottle(t *testing.T) {
	f := newExportFixture(t)
	if _, err := exportToZip(t, f); err != nil {
		t.Fatalf("first export: %v", err)
	}
	f.clock.Advance(10 * time.Minute)
	_, err := exportToZip(t, f)
	if !errors.Is(err, domain.ErrExportThrottled) {
		t.Fatalf("second export err = %v, want ErrExportThrottled", err)
	}
	var typed *domain.ExportThrottledError
	if !errors.As(err, &typed) || typed.RetryAfter != 50*time.Minute {
		t.Fatalf("RetryAfter = %+v, want 50m", typed)
	}
	f.clock.Advance(50 * time.Minute)
	if _, err := exportToZip(t, f); err != nil {
		t.Fatalf("export after the window: %v", err)
	}
}

func TestExportNeverReadsProviderTokens(t *testing.T) {
	f := newExportFixture(t)
	f.seedEverything(t)
	before := f.accounts.getTokensCalls
	if _, err := exportToZip(t, f); err != nil {
		t.Fatal(err)
	}
	if f.accounts.getTokensCalls != before {
		t.Fatalf("GetTokens was called %d time(s) during export, want 0", f.accounts.getTokensCalls-before)
	}
}

// Review Focus 1: an event count that is an exact multiple of the page size
// terminates cleanly — every event once, in id order, no phantom page.
func TestExportPagesEventsAcrossBoundary(t *testing.T) {
	f := newExportFixture(t)
	f.seedEverything(t)
	ctx := context.Background()
	for i := 2; i <= 4; i++ { // e1 already exists → 4 events, page size 2
		if _, err := f.events.Upsert(ctx, domain.Event{ID: fmt.Sprintf("e%d", i), CalendarID: "c1", Title: fmt.Sprintf("Event %d", i), Start: exportNow, End: exportNow.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	f.svc.eventPage = 2
	files, err := exportToZip(t, f)
	if err != nil {
		t.Fatal(err)
	}
	var events []domain.Event
	if err := json.Unmarshal(files["events.json"], &events); err != nil {
		t.Fatalf("events.json: %v\n%s", err, files["events.json"])
	}
	ids := []string{}
	for _, e := range events {
		ids = append(ids, e.ID)
	}
	if strings.Join(ids, ",") != "e1,e2,e3,e4" {
		t.Fatalf("events.json ids = %v, want e1..e4 once each in order", ids)
	}
}

func TestExportPagesThreadsPerAccount(t *testing.T) {
	f := newExportFixture(t)
	f.seedEverything(t)
	ctx := context.Background()
	for _, id := range []string{"t2", "t3"} {
		if _, err := f.threads.Upsert(ctx, domain.Thread{ID: id, AccountID: "a1", Subject: id, LastMessageAt: exportNow}); err != nil {
			t.Fatal(err)
		}
	}
	f.svc.threadPage = 2
	files, err := exportToZip(t, f)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"t1", "t2", "t3"} {
		if _, ok := files["threads/"+id+".json"]; !ok {
			t.Fatalf("threads/%s.json missing from %v", id, files)
		}
	}
}

func TestExportRepoErrorPropagatesAndKeepsSlot(t *testing.T) {
	f := newExportFixture(t)
	delete(f.users.byID, "u1") // profile lookup fails
	_, err := exportToZip(t, f)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want the repo error", err)
	}
	if _, err := exportToZip(t, f); !errors.Is(err, domain.ErrExportThrottled) {
		t.Fatalf("slot must stay claimed after a failed export, got %v", err)
	}
}

// Every user-authored dataset the purge deletes is exported, scoped strictly
// to the requesting user (u2's rows in the same repos never appear).
func TestExportIncludesUserAuthoredDatasetsScopedToUser(t *testing.T) {
	f := newExportFixture(t)
	f.seedEverything(t)
	files, err := exportToZip(t, f)
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		file      string
		want      []string
		forbidden []string
	}{
		{"team-snippets.json", []string{`"name": "TeamMine"`, `"teamId": "team1"`}, []string{"TeamTheirs", `"name": "Thanks"`}},
		{"thread-comments.json", []string{`"body": "my comment"`, `"threadId": "t1"`}, []string{"their comment"}},
		{"calendar-subscriptions.json", []string{`"url": "https://example.com/mine.ics"`, `"name": "Holidays"`}, []string{"theirs.ics"}},
		{"classifiers.json", []string{`"prompt": "my prompt"`, `"name": "Receipts"`}, []string{"their prompt"}},
		{"voice-profile.json", []string{`"profile": "Short and warm."`, `"sampleCount": 7`}, []string{"THEIR VOICE"}},
		{"bookings.json", []string{`"id": "b1"`, "guest@example.com"}, []string{"theirguest@example.com", `"id": "b2"`}},
	}
	for _, c := range checks {
		body := string(files[c.file])
		if !json.Valid(files[c.file]) {
			t.Fatalf("%s invalid JSON: %s", c.file, body)
		}
		for _, w := range c.want {
			if !strings.Contains(body, w) {
				t.Errorf("%s missing %s: %s", c.file, w, body)
			}
		}
		for _, x := range c.forbidden {
			if strings.Contains(body, x) {
				t.Errorf("%s leaks %q: %s", c.file, x, body)
			}
		}
	}
	if strings.Contains(string(files["snippets.json"]), "TeamMine") {
		t.Fatalf("snippets.json must stay personal-only: %s", files["snippets.json"])
	}
}

func TestExportVoiceProfileAbsentIsNull(t *testing.T) {
	f := newExportFixture(t)
	files, err := exportToZip(t, f)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(files["voice-profile.json"])); got != "null" {
		t.Fatalf("voice-profile.json = %q, want null", got)
	}
	for _, name := range []string{"team-snippets.json", "thread-comments.json", "calendar-subscriptions.json", "classifiers.json"} {
		if got := strings.TrimSpace(string(files[name])); got != "[]" {
			t.Fatalf("%s = %q, want []", name, got)
		}
	}
}

// Bookings are paged to completion (no cap): a count that is an exact
// multiple of the page size yields every booking once, in id order.
func TestExportPagesBookingsFully(t *testing.T) {
	f := newExportFixture(t)
	ctx := context.Background()
	if _, err := f.links.Create(ctx, domain.BookingLink{ID: "l1", UserID: "u1", Slug: "mine", Title: "Call"}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 6; i++ {
		start := exportNow.Add(time.Duration(i) * time.Hour)
		if _, err := f.bookings.CreateHold(ctx, domain.Booking{ID: fmt.Sprintf("b%d", i), LinkID: "l1", Start: start, End: start.Add(30 * time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	f.svc.bookingPage = 2
	files, err := exportToZip(t, f)
	if err != nil {
		t.Fatal(err)
	}
	var got []domain.Booking
	if err := json.Unmarshal(files["bookings.json"], &got); err != nil {
		t.Fatalf("bookings.json: %v\n%s", err, files["bookings.json"])
	}
	ids := []string{}
	for _, b := range got {
		ids = append(ids, b.ID)
	}
	if strings.Join(ids, ",") != "b1,b2,b3,b4,b5,b6" {
		t.Fatalf("bookings ids = %v, want b1..b6 once each", ids)
	}
}
