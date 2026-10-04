package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// ExportWindow is the per-user data-export throttle.
const ExportWindow = time.Hour

const (
	exportThreadPage  = 200
	exportEventPage   = 500
	exportBookingPage = 500
)

// ExportDeps wires one repo per export file plus the throttle and clock.
type ExportDeps struct {
	Users         port.UserRepo
	Accounts      port.AccountRepo
	Calendars     port.CalendarRepo
	Events        port.EventRepo
	Threads       port.ThreadRepo
	Messages      port.MessageRepo
	Drafts        port.DraftRepo
	Snippets      port.SnippetRepo
	Templates     port.EventTemplateRepo
	Sets          port.CalendarSetRepo
	Tasks         port.TaskRepo
	Links         port.BookingLinkRepo
	Bookings      port.BookingRepo
	Polls         port.PollRepo
	Labels        port.LabelRepo
	EventNotes    port.EventNoteRepo
	Prefs         port.PrefsRepo
	Preferences   port.UserPreferencesRepo
	CalendarPrefs port.CalendarPrefsRepo
	Settings      port.UserSettingsRepo
	Exports       port.UserExportRepo
	Clock         port.Clock
	// User-authored datasets the purge also deletes.
	Comments              port.CommentRepo
	CalendarSubscriptions port.CalendarSubscriptionRepo
	Classifiers           port.ClassifierRepo
	VoiceProfiles         port.VoiceProfileRepo
}

// ExportService implements port.ExportService: claims the hourly slot, then
// writes every user-authored dataset the purge deletes into the sink in a
// fixed order, paging threads (per account, keyset on id), events and
// bookings (keyset on id) so memory stays flat. Every read is scoped to the
// requesting user. It never touches AccountRepo.GetTokens.
type ExportService struct {
	d           ExportDeps
	threadPage  int
	eventPage   int
	bookingPage int
}

var _ port.ExportService = (*ExportService)(nil)

func NewExportService(d ExportDeps) *ExportService {
	return &ExportService{d: d, threadPage: exportThreadPage, eventPage: exportEventPage, bookingPage: exportBookingPage}
}

// exportAccount is accounts.json's row: identity only, never tokens/scopes.
type exportAccount struct {
	ID        string          `json:"id"`
	Provider  domain.Provider `json:"provider"`
	Email     string          `json:"email"`
	Status    string          `json:"status"`
	CreatedAt time.Time       `json:"createdAt"`
}

type exportThread struct {
	Thread   domain.Thread    `json:"thread"`
	Messages []domain.Message `json:"messages"`
}

type exportPoll struct {
	Poll  domain.MeetingPoll `json:"poll"`
	Votes []domain.PollVote  `json:"votes"`
}

type exportSettings struct {
	Prefs         domain.UserPrefs     `json:"prefs"`
	Preferences   port.UserPreferences `json:"preferences"`
	CalendarPrefs domain.CalendarPrefs `json:"calendarPrefs"`
	Settings      domain.UserSettings  `json:"settings"`
}

// writeExportFile encodes v as one pretty-printed JSON document.
func writeExportFile(sink port.ExportSink, name string, v any) error {
	w, err := sink.Create(name)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	// A data file, not markup: keep bodies readable ("<p>", not "<p").
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("export %s: %w", name, err)
	}
	return nil
}

// exportArray streams a JSON array element by element (paged collections).
type exportArray struct {
	w io.Writer
	n int
}

func newExportArray(sink port.ExportSink, name string) (*exportArray, error) {
	w, err := sink.Create(name)
	if err != nil {
		return nil, err
	}
	if _, err := io.WriteString(w, "[\n"); err != nil {
		return nil, err
	}
	return &exportArray{w: w}, nil
}

func (a *exportArray) item(v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("  ", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	b := bytes.TrimRight(buf.Bytes(), "\n")
	sep := ",\n"
	if a.n == 0 {
		sep = ""
	}
	a.n++
	_, err := fmt.Fprintf(a.w, "%s  %s", sep, b)
	return err
}

func (a *exportArray) close() error {
	_, err := io.WriteString(a.w, "\n]\n")
	return err
}

// Export implements port.ExportService.
func (s *ExportService) Export(ctx context.Context, userID string, sink port.ExportSink) error {
	now := s.d.Clock.Now().UTC()
	ok, retryAt, err := s.d.Exports.Claim(ctx, userID, now, ExportWindow)
	if err != nil {
		return err
	}
	if !ok {
		wait := retryAt.Sub(now)
		if wait < time.Second {
			wait = time.Second
		}
		return &domain.ExportThrottledError{RetryAfter: wait}
	}

	user, err := s.d.Users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if err := writeExportFile(sink, "profile.json", user); err != nil {
		return err
	}

	accounts, err := s.d.Accounts.ListByUser(ctx, userID)
	if err != nil {
		return err
	}
	exported := make([]exportAccount, 0, len(accounts))
	for _, a := range accounts {
		exported = append(exported, exportAccount{ID: a.ID, Provider: a.Provider, Email: a.Email, Status: string(a.Status), CreatedAt: a.CreatedAt.UTC()})
	}
	if err := writeExportFile(sink, "accounts.json", exported); err != nil {
		return err
	}

	calendars, err := s.d.Calendars.ListByUser(ctx, userID)
	if err != nil {
		return err
	}
	if err := writeExportFile(sink, "calendars.json", calendars); err != nil {
		return err
	}

	events, err := newExportArray(sink, "events.json")
	if err != nil {
		return err
	}
	for after := ""; ; {
		page, err := s.d.Events.ListByUserPage(ctx, userID, after, s.eventPage)
		if err != nil {
			return err
		}
		for _, e := range page {
			if err := events.item(e); err != nil {
				return err
			}
		}
		if len(page) < s.eventPage {
			break
		}
		after = page[len(page)-1].ID
	}
	if err := events.close(); err != nil {
		return err
	}

	for _, a := range accounts {
		for after := ""; ; {
			page, err := s.d.Threads.ListByAccountPage(ctx, a.ID, after, s.threadPage)
			if err != nil {
				return err
			}
			for _, t := range page {
				msgs, err := s.d.Messages.ListByThread(ctx, t.ID)
				if err != nil {
					return err
				}
				if err := writeExportFile(sink, "threads/"+t.ID+".json", exportThread{Thread: t, Messages: msgs}); err != nil {
					return err
				}
			}
			if len(page) < s.threadPage {
				break
			}
			after = page[len(page)-1].ID
		}
	}

	drafts, err := s.d.Drafts.ListByUser(ctx, userID)
	if err != nil {
		return err
	}
	if err := writeExportFile(sink, "drafts.json", drafts); err != nil {
		return err
	}
	snippets, err := s.d.Snippets.ListByUser(ctx, userID)
	if err != nil {
		return err
	}
	if err := writeExportFile(sink, "snippets.json", snippets); err != nil {
		return err
	}
	templates, err := s.d.Templates.ListByUser(ctx, userID)
	if err != nil {
		return err
	}
	if err := writeExportFile(sink, "templates.json", templates); err != nil {
		return err
	}
	sets, err := s.d.Sets.ListByUser(ctx, userID)
	if err != nil {
		return err
	}
	if err := writeExportFile(sink, "calendar-sets.json", sets); err != nil {
		return err
	}
	tasks, err := s.d.Tasks.List(ctx, port.TaskQuery{UserID: userID, IncludeCompleted: true})
	if err != nil {
		return err
	}
	if err := writeExportFile(sink, "tasks.json", tasks); err != nil {
		return err
	}
	links, err := s.d.Links.ListByUser(ctx, userID)
	if err != nil {
		return err
	}
	if err := writeExportFile(sink, "booking-links.json", links); err != nil {
		return err
	}
	bookings, err := newExportArray(sink, "bookings.json")
	if err != nil {
		return err
	}
	for after := ""; ; {
		page, err := s.d.Bookings.ListByUserPage(ctx, userID, after, s.bookingPage)
		if err != nil {
			return err
		}
		for _, b := range page {
			if err := bookings.item(b); err != nil {
				return err
			}
		}
		if len(page) < s.bookingPage {
			break
		}
		after = page[len(page)-1].ID
	}
	if err := bookings.close(); err != nil {
		return err
	}
	polls, err := s.d.Polls.ListByUser(ctx, userID)
	if err != nil {
		return err
	}
	exportedPolls := make([]exportPoll, 0, len(polls))
	for _, p := range polls {
		votes, err := s.d.Polls.ListVotes(ctx, p.ID)
		if err != nil {
			return err
		}
		exportedPolls = append(exportedPolls, exportPoll{Poll: p, Votes: votes})
	}
	if err := writeExportFile(sink, "polls.json", exportedPolls); err != nil {
		return err
	}
	prefs, err := s.d.Prefs.Get(ctx, userID)
	if err != nil {
		return err
	}
	preferences, err := s.d.Preferences.Get(ctx, userID)
	if err != nil {
		return err
	}
	calendarPrefs, err := s.d.CalendarPrefs.Get(ctx, userID)
	if err != nil {
		return err
	}
	settings, err := s.d.Settings.Get(ctx, userID)
	if err != nil {
		return err
	}
	if err := writeExportFile(sink, "settings.json", exportSettings{Prefs: prefs, Preferences: preferences, CalendarPrefs: calendarPrefs, Settings: settings}); err != nil {
		return err
	}
	labels, err := s.d.Labels.ListByUser(ctx, userID)
	if err != nil {
		return err
	}
	if err := writeExportFile(sink, "labels.json", labels); err != nil {
		return err
	}
	notes, err := s.d.EventNotes.ListByUser(ctx, userID)
	if err != nil {
		return err
	}
	if err := writeExportFile(sink, "event-notes.json", notes); err != nil {
		return err
	}
	return s.exportAuthored(ctx, userID, sink)
}

// exportAuthored writes the remaining user-authored datasets: team snippets
// the user wrote, their thread comments, ICS subscriptions, classifier
// prompts and the AI voice profile (null when none was learned).
func (s *ExportService) exportAuthored(ctx context.Context, userID string, sink port.ExportSink) error {
	teamSnippets, err := s.d.Snippets.ListTeamByAuthor(ctx, userID)
	if err != nil {
		return err
	}
	if err := writeExportFile(sink, "team-snippets.json", teamSnippets); err != nil {
		return err
	}
	comments, err := s.d.Comments.ListByAuthor(ctx, userID)
	if err != nil {
		return err
	}
	if err := writeExportFile(sink, "thread-comments.json", comments); err != nil {
		return err
	}
	subs, err := s.d.CalendarSubscriptions.ListByUser(ctx, userID)
	if err != nil {
		return err
	}
	if subs == nil {
		subs = []domain.CalendarSubscription{}
	}
	if err := writeExportFile(sink, "calendar-subscriptions.json", subs); err != nil {
		return err
	}
	classifiers, err := s.d.Classifiers.ListByUser(ctx, userID)
	if err != nil {
		return err
	}
	if classifiers == nil {
		classifiers = []domain.AiClassifier{}
	}
	if err := writeExportFile(sink, "classifiers.json", classifiers); err != nil {
		return err
	}
	var voice *domain.VoiceProfile
	switch p, err := s.d.VoiceProfiles.Get(ctx, userID); {
	case err == nil:
		voice = &p
	case !errors.Is(err, domain.ErrNotFound):
		return err
	}
	return writeExportFile(sink, "voice-profile.json", voice)
}
