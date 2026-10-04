import type {
  Booking,
  BookingLink,
  BookingLinkInput,
  BusyInterval,
  MeetingPoll,
  PollInput,
  Team,
  TeamMember,
  UserSettings,
  UserSettingsUpdate,
} from '@calendium/shared';

import { getApiClient } from '@/lib/api';
import { DEMO_MODE } from '@/lib/demo';
import { schedulingMock } from '@/lib/scheduling-mock';

/**
 * Data-access wrappers for booking links, bookings, meeting polls, and
 * working-hours settings (calendar-data.ts pattern). They hit the real API
 * and, in explicit demo mode only (lib/demo.ts), fall back to the in-memory
 * mock (lib/scheduling-mock.ts). Outside demo mode failures propagate so the
 * UI shows real loading / empty / error states and mutations surface their
 * failures — including the 409 slug conflict, which callers check for via
 * ApiRequestError rather than this layer swallowing it.
 */

export async function fetchBookingLinks(): Promise<BookingLink[]> {
  try {
    return await getApiClient().listBookingLinks();
  } catch (err) {
    if (DEMO_MODE) return schedulingMock.listBookingLinks();
    throw err;
  }
}

export async function createBookingLinkApi(input: BookingLinkInput): Promise<BookingLink> {
  try {
    return await getApiClient().createBookingLink(input);
  } catch (err) {
    if (DEMO_MODE) return schedulingMock.createBookingLink(input);
    throw err;
  }
}

export async function updateBookingLinkApi(
  id: string,
  input: BookingLinkInput
): Promise<BookingLink> {
  try {
    return await getApiClient().updateBookingLink(id, input);
  } catch (err) {
    if (DEMO_MODE) return schedulingMock.updateBookingLink(id, input);
    throw err;
  }
}

export async function deleteBookingLinkApi(id: string): Promise<void> {
  try {
    await getApiClient().deleteBookingLink(id);
  } catch (err) {
    if (DEMO_MODE) {
      schedulingMock.deleteBookingLink(id);
      return;
    }
    throw err;
  }
}

export async function fetchBookings(): Promise<Booking[]> {
  try {
    return await getApiClient().listBookings();
  } catch (err) {
    if (DEMO_MODE) return schedulingMock.listBookings();
    throw err;
  }
}

export async function cancelBookingApi(id: string): Promise<void> {
  try {
    await getApiClient().cancelBooking(id);
  } catch (err) {
    if (DEMO_MODE) {
      schedulingMock.cancelBooking(id);
      return;
    }
    throw err;
  }
}

export async function fetchPolls(): Promise<MeetingPoll[]> {
  try {
    return await getApiClient().listPolls();
  } catch (err) {
    if (DEMO_MODE) return schedulingMock.listPolls();
    throw err;
  }
}

export async function createPollApi(input: PollInput): Promise<MeetingPoll> {
  try {
    return await getApiClient().createPoll(input);
  } catch (err) {
    if (DEMO_MODE) return schedulingMock.createPoll(input);
    throw err;
  }
}

export async function confirmPollApi(id: string, optionId: string): Promise<MeetingPoll> {
  try {
    return await getApiClient().confirmPoll(id, optionId);
  } catch (err) {
    if (DEMO_MODE) return schedulingMock.confirmPoll(id, optionId);
    throw err;
  }
}

export async function deletePollApi(id: string): Promise<void> {
  try {
    await getApiClient().deletePoll(id);
  } catch (err) {
    if (DEMO_MODE) {
      schedulingMock.deletePoll(id);
      return;
    }
    throw err;
  }
}

export async function fetchSettings(): Promise<UserSettings> {
  try {
    return await getApiClient().getSettings();
  } catch (err) {
    if (DEMO_MODE) return schedulingMock.getSettings();
    throw err;
  }
}

export async function updateSettingsApi(s: UserSettingsUpdate): Promise<UserSettings> {
  try {
    return await getApiClient().updateSettings(s);
  } catch (err) {
    if (DEMO_MODE) return schedulingMock.updateSettings(s);
    throw err;
  }
}

export async function fetchFreeBusy(
  emails: string[],
  from: string,
  to: string
): Promise<Record<string, BusyInterval[]>> {
  try {
    return await getApiClient().getFreeBusy(emails, from, to);
  } catch (err) {
    if (DEMO_MODE) return schedulingMock.getFreeBusy(emails, from, to);
    throw err;
  }
}

// --- Team booking links (M2.7 Task 14) --------------------------------------

/** Teams the signed-in user belongs to (team picker in the link editor). */
export async function fetchSchedulingTeams(): Promise<Team[]> {
  try {
    return await getApiClient().listTeams();
  } catch (err) {
    if (DEMO_MODE) return [];
    throw err;
  }
}

/** Member list of one team; the server 404s for non-members. */
export async function fetchTeamMembers(teamId: string): Promise<TeamMember[]> {
  try {
    const { members } = await getApiClient().getTeam(teamId);
    return members;
  } catch (err) {
    if (DEMO_MODE) return [];
    throw err;
  }
}
