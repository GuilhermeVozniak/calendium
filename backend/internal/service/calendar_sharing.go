package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// Shared calendars with granular permissions (M2.7 Task 12).
//
// Management surface (ShareCalendar / ListCalendarShares /
// UpdateCalendarShare / RevokeCalendarShare) is owner-only via the
// calendar → account → user ownership chain: everyone else gets 404, never
// a 403 existence oracle. Viewer-side enforcement hooks into the existing
// ListCalendars / ListEvents / Create-Update-DeleteEvent paths:
//
//	free_busy → busy blocks only (redactedBusyEvent), no writes
//	reader    → full read, no writes (ErrForbidden)
//	editor    → full read + writes through the OWNER's provider tokens,
//	            each write audit-logged (actor = grantee, principal = owner)
//
// No share → the calendar is invisible (privacy default).

var _ port.CalendarSharingService = (*CalendarService)(nil)

// --- share management (owner-only) ------------------------------------------

func (s *CalendarService) ShareCalendar(ctx context.Context, userID, calendarID string, in port.CalendarShareInput) (domain.CalendarShare, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.CalendarShare{}, err
	}
	if s.shares == nil {
		return domain.CalendarShare{}, domain.ErrNotImplemented
	}
	c, _, err := s.ownedCalendar(ctx, userID, calendarID)
	if err != nil {
		return domain.CalendarShare{}, err
	}
	perm := domain.PermissionFreeBusy
	if in.Permission != "" {
		if perm, err = domain.ParseCalendarPermission(in.Permission); err != nil {
			return domain.CalendarShare{}, err
		}
	}
	hasUser, hasTeam := in.GranteeUserID != "", in.GranteeTeamID != ""
	if hasUser == hasTeam {
		return domain.CalendarShare{}, fmt.Errorf("%w: exactly one of granteeUserId or granteeTeamId is required", domain.ErrValidation)
	}
	share := domain.CalendarShare{
		ID:         newID(),
		CalendarID: c.ID,
		Permission: perm,
		CreatedBy:  userID,
		CreatedAt:  s.clock.Now(),
	}
	if hasUser {
		if in.GranteeUserID == userID {
			return domain.CalendarShare{}, fmt.Errorf("%w: cannot share a calendar with its owner", domain.ErrValidation)
		}
		if s.users != nil {
			if _, err := s.users.GetByID(ctx, in.GranteeUserID); err != nil {
				if errors.Is(err, domain.ErrNotFound) {
					return domain.CalendarShare{}, fmt.Errorf("%w: unknown grantee user", domain.ErrValidation)
				}
				return domain.CalendarShare{}, err
			}
		}
		share.GranteeUserID = ptr(in.GranteeUserID)
	} else {
		if s.teams == nil {
			return domain.CalendarShare{}, domain.ErrNotImplemented
		}
		// Sharing with a team requires the sharer to be one of its members;
		// non-members get the membership primitive's 404 (no existence oracle).
		if _, err := s.teams.GetMember(ctx, in.GranteeTeamID, userID); err != nil {
			return domain.CalendarShare{}, err
		}
		share.GranteeTeamID = ptr(in.GranteeTeamID)
	}
	return s.shares.Create(ctx, share)
}

func (s *CalendarService) ListCalendarShares(ctx context.Context, userID, calendarID string) ([]domain.CalendarShare, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return nil, err
	}
	if s.shares == nil {
		return nil, domain.ErrNotImplemented
	}
	if _, _, err := s.ownedCalendar(ctx, userID, calendarID); err != nil {
		return nil, err
	}
	shares, err := s.shares.ListByCalendar(ctx, calendarID)
	if err != nil {
		return nil, err
	}
	if shares == nil {
		shares = []domain.CalendarShare{}
	}
	return shares, nil
}

func (s *CalendarService) UpdateCalendarShare(ctx context.Context, userID, calendarID, shareID, permission string) (domain.CalendarShare, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.CalendarShare{}, err
	}
	if s.shares == nil {
		return domain.CalendarShare{}, domain.ErrNotImplemented
	}
	perm, err := domain.ParseCalendarPermission(permission)
	if err != nil {
		return domain.CalendarShare{}, err
	}
	share, err := s.ownedShare(ctx, userID, calendarID, shareID)
	if err != nil {
		return domain.CalendarShare{}, err
	}
	share.Permission = perm
	if err := s.shares.Update(ctx, share); err != nil {
		return domain.CalendarShare{}, err
	}
	return share, nil
}

func (s *CalendarService) RevokeCalendarShare(ctx context.Context, userID, calendarID, shareID string) error {
	if err := s.ent.require(ctx, userID); err != nil {
		return err
	}
	if s.shares == nil {
		return domain.ErrNotImplemented
	}
	share, err := s.ownedShare(ctx, userID, calendarID, shareID)
	if err != nil {
		return err
	}
	return s.shares.Delete(ctx, share.ID)
}

// ownedShare enforces the ownership chain and that the share belongs to the
// given calendar; anything else is a 404.
func (s *CalendarService) ownedShare(ctx context.Context, userID, calendarID, shareID string) (domain.CalendarShare, error) {
	if _, _, err := s.ownedCalendar(ctx, userID, calendarID); err != nil {
		return domain.CalendarShare{}, err
	}
	shares, err := s.shares.ListByCalendar(ctx, calendarID)
	if err != nil {
		return domain.CalendarShare{}, err
	}
	for _, sh := range shares {
		if sh.ID == shareID {
			return sh, nil
		}
	}
	return domain.CalendarShare{}, domain.ErrNotFound
}

// --- viewer-side resolution --------------------------------------------------

// grantsFor resolves every share applicable to the viewer (direct user
// grants plus grants to any team the viewer belongs to), collapsed per
// calendar with the most permissive grant winning.
func (s *CalendarService) grantsFor(ctx context.Context, viewerID string) (map[string]domain.CalendarPermission, error) {
	var teamIDs []string
	if s.teams != nil {
		teams, err := s.teams.ListByUser(ctx, viewerID)
		if err != nil {
			return nil, err
		}
		for _, t := range teams {
			teamIDs = append(teamIDs, t.ID)
		}
	}
	shares, err := s.shares.ListForGrantee(ctx, viewerID, teamIDs)
	if err != nil {
		return nil, err
	}
	out := map[string]domain.CalendarPermission{}
	memberOK := map[string]bool{} // "teamID\x00ownerID" -> still a member
	for _, sh := range shares {
		if sh.GranteeTeamID != nil {
			// A team grant is live only while the sharer (CreatedBy — always
			// the calendar owner) still belongs to the team: "any team the
			// viewer shares with the owner" is resolved now, not at grant
			// time, so an owner leaving the team cuts the team's view
			// immediately without a manual revoke.
			if s.teams == nil {
				continue // no membership oracle — fail closed
			}
			key := *sh.GranteeTeamID + "\x00" + sh.CreatedBy
			ok, seen := memberOK[key]
			if !seen {
				_, err := s.teams.GetMember(ctx, *sh.GranteeTeamID, sh.CreatedBy)
				switch {
				case err == nil:
					ok = true
				case errors.Is(err, domain.ErrNotFound):
					ok = false
				default:
					return nil, err // fail closed on lookup errors
				}
				memberOK[key] = ok
			}
			if !ok {
				continue // owner left the team: grant is dormant
			}
		}
		if cur, ok := out[sh.CalendarID]; !ok || sh.Permission.MorePermissive(cur) {
			out[sh.CalendarID] = sh.Permission
		}
	}
	return out, nil
}

// sharedCalendars returns the calendars shared with the viewer, annotated
// with the effective permission. The viewer's own calendars are excluded
// (a team grant can otherwise loop back to the sharer).
func (s *CalendarService) sharedCalendars(ctx context.Context, viewerID string) ([]domain.Calendar, error) {
	grants, err := s.grantsFor(ctx, viewerID)
	if err != nil {
		return nil, err
	}
	out := []domain.Calendar{}
	for _, calID := range sortedKeys(grants) {
		perm := grants[calID]
		c, owner, err := s.calendarOwner(ctx, calID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				continue // dangling grant; the DB cascade normally prevents this
			}
			return nil, err
		}
		if owner.UserID == viewerID {
			continue
		}
		p := perm
		c.SharedPermission = &p
		// A shared calendar is writable for the viewer only with an editor
		// grant (and only where the mirror itself is writable).
		c.CanWrite = c.CanWrite && perm.AtLeast(domain.PermissionEditor)
		out = append(out, c)
	}
	return out, nil
}

// sharedEvents lists the viewer's shared-calendar events in [from, to),
// applying the permission matrix: free_busy grants only redacted busy
// blocks; reader/editor grant the full mirror. calendarIDs, when non-empty,
// restricts the shared calendars considered (same contract as ListEvents).
func (s *CalendarService) sharedEvents(ctx context.Context, viewerID string, from, to time.Time, calendarIDs []string) ([]domain.Event, error) {
	grants, err := s.grantsFor(ctx, viewerID)
	if err != nil {
		return nil, err
	}
	if len(grants) == 0 {
		return nil, nil
	}
	filter := map[string]struct{}{}
	for _, id := range calendarIDs {
		filter[id] = struct{}{}
	}
	var out []domain.Event
	for _, calID := range sortedKeys(grants) {
		if len(filter) > 0 {
			if _, ok := filter[calID]; !ok {
				continue
			}
		}
		c, owner, err := s.calendarOwner(ctx, calID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				continue
			}
			return nil, err
		}
		if owner.UserID == viewerID {
			continue // already served by the personal path
		}
		evs, err := s.events.ListInRange(ctx, owner.UserID, from, to, []string{c.ID})
		if err != nil {
			return nil, err
		}
		if grants[calID].AtLeast(domain.PermissionReader) {
			out = append(out, evs...)
			continue
		}
		for _, ev := range evs {
			out = append(out, redactedBusyEvent(ev))
		}
	}
	return out, nil
}

// redactedBusyEvent reduces an event to {Start, End, FreeBusyOnly: true}
// with title "Busy" and every other field zeroed — the free_busy privacy
// contract. Titles, descriptions, locations, attendees, conferencing links,
// and recurrence rules must never reach a busy-only viewer.
func redactedBusyEvent(ev domain.Event) domain.Event {
	return domain.Event{
		ID:              ev.ID,
		CalendarID:      ev.CalendarID,
		Title:           "Busy",
		Start:           ev.Start,
		End:             ev.End,
		FreeBusyOnly:    true,
		Attendees:       []domain.Attendee{},
		ReminderMinutes: []int{},
	}
}

// calendarOwner resolves a calendar and its owning account.
func (s *CalendarService) calendarOwner(ctx context.Context, calendarID string) (domain.Calendar, domain.ConnectedAccount, error) {
	c, err := s.calendars.GetByID(ctx, calendarID)
	if err != nil {
		return domain.Calendar{}, domain.ConnectedAccount{}, err
	}
	acct, err := s.accounts.GetByID(ctx, c.AccountID)
	if err != nil {
		return domain.Calendar{}, domain.ConnectedAccount{}, err
	}
	return c, acct, nil
}

// --- editor write path (owner tokens + audit) --------------------------------

// sharedWriteAccess authorizes a grantee write on calendarID: no applicable
// share → ErrNotFound (the calendar stays invisible), any share below
// editor → ErrForbidden.
func (s *CalendarService) sharedWriteAccess(ctx context.Context, viewerID, calendarID string) (domain.Calendar, domain.ConnectedAccount, error) {
	c, owner, err := s.calendarOwner(ctx, calendarID)
	if err != nil {
		return domain.Calendar{}, domain.ConnectedAccount{}, err
	}
	if owner.UserID == viewerID {
		// Own calendars never reach this path (ownedCalendar serves them).
		return domain.Calendar{}, domain.ConnectedAccount{}, domain.ErrNotFound
	}
	grants, err := s.grantsFor(ctx, viewerID)
	if err != nil {
		return domain.Calendar{}, domain.ConnectedAccount{}, err
	}
	perm, ok := grants[calendarID]
	if !ok {
		return domain.Calendar{}, domain.ConnectedAccount{}, domain.ErrNotFound
	}
	if !perm.AtLeast(domain.PermissionEditor) {
		return domain.Calendar{}, domain.ConnectedAccount{}, fmt.Errorf("%w: editor permission required", domain.ErrForbidden)
	}
	return c, owner, nil
}

func (s *CalendarService) createSharedEvent(ctx context.Context, userID string, in domain.EventInput) (domain.Event, error) {
	c, owner, err := s.sharedWriteAccess(ctx, userID, in.CalendarID)
	if err != nil {
		return domain.Event{}, err
	}
	if !c.CanWrite {
		return domain.Event{}, fmt.Errorf("%w: calendar is read-only", domain.ErrValidation)
	}
	ev := eventFromInput(in, c.ID)
	if provider, ok := s.cal[owner.Provider]; ok {
		token, err := s.tokens.accessToken(ctx, owner)
		if err != nil {
			return domain.Event{}, err
		}
		created, err := provider.CreateEvent(ctx, token, c.ProviderCalendarID, in)
		if err != nil {
			return domain.Event{}, fmt.Errorf("provider write-through failed: %w", err)
		}
		created.ID = ev.ID
		created.CalendarID = c.ID
		carryLocalGeo(&created, ev)
		ev = created
	}
	ev, err = s.events.Upsert(ctx, ev)
	if err != nil {
		return domain.Event{}, err
	}
	if err := s.recordAudit(ctx, userID, owner.UserID, "event.create", ev.ID, c.ID); err != nil {
		return domain.Event{}, err
	}
	return ev, nil
}

func (s *CalendarService) updateSharedEvent(ctx context.Context, userID, eventID string, patch domain.EventPatch) (domain.Event, error) {
	ev, err := s.events.GetByID(ctx, eventID)
	if err != nil {
		return domain.Event{}, err
	}
	c, owner, err := s.sharedWriteAccess(ctx, userID, ev.CalendarID)
	if err != nil {
		return domain.Event{}, err
	}
	if !c.CanWrite {
		return domain.Event{}, fmt.Errorf("%w: calendar is read-only", domain.ErrValidation)
	}
	if patch.Start != nil && patch.End != nil && !patch.End.After(*patch.Start) {
		return domain.Event{}, fmt.Errorf("%w: end must be after start", domain.ErrValidation)
	}
	applyEventPatch(&ev, patch)
	if provider, ok := s.cal[owner.Provider]; ok && ev.ProviderEventID != "" {
		token, err := s.tokens.accessToken(ctx, owner)
		if err != nil {
			return domain.Event{}, err
		}
		updated, err := provider.UpdateEvent(ctx, token, c.ProviderCalendarID, ev.ProviderEventID, patch)
		if err != nil {
			return domain.Event{}, fmt.Errorf("provider write-through failed: %w", err)
		}
		updated.ID = ev.ID
		updated.CalendarID = c.ID
		carryLocalGeo(&updated, ev)
		ev = updated
	}
	ev, err = s.events.Upsert(ctx, ev)
	if err != nil {
		return domain.Event{}, err
	}
	if err := s.recordAudit(ctx, userID, owner.UserID, "event.update", ev.ID, c.ID); err != nil {
		return domain.Event{}, err
	}
	return ev, nil
}

func (s *CalendarService) deleteSharedEvent(ctx context.Context, userID, eventID string) error {
	ev, err := s.events.GetByID(ctx, eventID)
	if err != nil {
		return err
	}
	c, owner, err := s.sharedWriteAccess(ctx, userID, ev.CalendarID)
	if err != nil {
		return err
	}
	if !c.CanWrite {
		return fmt.Errorf("%w: calendar is read-only", domain.ErrValidation)
	}
	if provider, ok := s.cal[owner.Provider]; ok && ev.ProviderEventID != "" {
		token, err := s.tokens.accessToken(ctx, owner)
		if err != nil {
			return err
		}
		if err := provider.DeleteEvent(ctx, token, c.ProviderCalendarID, ev.ProviderEventID); err != nil {
			return fmt.Errorf("provider write-through failed: %w", err)
		}
	}
	if err := s.events.Delete(ctx, ev.ID); err != nil {
		return err
	}
	return s.recordAudit(ctx, userID, owner.UserID, "event.delete", ev.ID, c.ID)
}

// recordAudit stores the cross-principal write trail (actor = grantee,
// principal = calendar owner). A failed audit write fails the request:
// editor mutations must never be silently unaccounted for.
func (s *CalendarService) recordAudit(ctx context.Context, actorID, principalID, action, eventID, calendarID string) error {
	if s.audit == nil {
		return nil
	}
	return s.audit.Record(ctx, domain.AuditEntry{
		ID:           newID(),
		ActorID:      actorID,
		PrincipalID:  principalID,
		Action:       action,
		ResourceType: "event",
		ResourceID:   eventID,
		Metadata:     map[string]any{"calendarId": calendarID},
		CreatedAt:    s.clock.Now(),
	})
}

// sortedKeys returns the map's keys in deterministic order.
func sortedKeys(m map[string]domain.CalendarPermission) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
