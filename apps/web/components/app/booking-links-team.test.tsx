import type { BookingLink, Calendar, Team, TeamMember } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { BookingLinks } from '@/components/app/booking-links';

// ---------------------------------------------------------------------------
// Team booking links (M2.7 Task 14): team picker + member toggles in the
// booking-link editor. Kept in its own file so parallel tasks appending to
// booking-links.test.tsx merge cleanly.
// ---------------------------------------------------------------------------

const fetchBookingLinksMock = vi.fn();
const createBookingLinkApiMock = vi.fn();
const updateBookingLinkApiMock = vi.fn();
const deleteBookingLinkApiMock = vi.fn();
const fetchSchedulingTeamsMock = vi.fn();
const fetchTeamMembersMock = vi.fn();
vi.mock('@/lib/scheduling-data', () => ({
  fetchBookingLinks: (...args: unknown[]) => fetchBookingLinksMock(...args),
  createBookingLinkApi: (...args: unknown[]) => createBookingLinkApiMock(...args),
  updateBookingLinkApi: (...args: unknown[]) => updateBookingLinkApiMock(...args),
  deleteBookingLinkApi: (...args: unknown[]) => deleteBookingLinkApiMock(...args),
  fetchSchedulingTeams: (...args: unknown[]) => fetchSchedulingTeamsMock(...args),
  fetchTeamMembers: (...args: unknown[]) => fetchTeamMembersMock(...args),
}));

const fetchCalendarsMock = vi.fn();
vi.mock('@/lib/calendar-data', () => ({
  fetchCalendars: (...args: unknown[]) => fetchCalendarsMock(...args),
}));

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    success: (...args: unknown[]) => toastSuccess(...args),
    error: (...args: unknown[]) => toastError(...args),
  },
}));

const CALENDAR: Calendar = {
  id: 'cal-1',
  accountId: 'acc1',
  name: 'Work',
  color: '#3b82f6',
  timeZone: 'UTC',
  isPrimary: true,
  isVisible: true,
  canWrite: true,
};

const TEAM: Team = {
  id: 'team-1',
  name: 'Sales',
  createdBy: 'u1',
  createdAt: new Date().toISOString(),
};

const MEMBERS: TeamMember[] = [
  {
    teamId: 'team-1',
    userId: 'u2',
    role: 'member',
    shareReadStatuses: false,
    joinedAt: new Date().toISOString(),
  },
];

// F2: rosters arrive enriched with display identity from the server.
const NAMED_MEMBERS: TeamMember[] = [
  { ...MEMBERS[0]!, name: 'Uma Two', email: 'u2@x.com' },
  {
    teamId: 'team-1',
    userId: 'u3',
    role: 'admin',
    shareReadStatuses: false,
    joinedAt: new Date().toISOString(),
    email: 'u3@x.com',
  },
];

function makeTeamLink(overrides: Partial<BookingLink> = {}): BookingLink {
  return {
    id: 'link-1',
    slug: 'team-intro',
    title: 'Sales intro',
    description: null,
    calendarId: CALENDAR.id,
    durationMinutes: 30,
    timeZone: 'UTC',
    windows: [{ weekday: 1, start: '09:00', end: '17:00' }],
    bufferBeforeMin: 0,
    bufferAfterMin: 0,
    dailyLimit: 0,
    minNoticeMin: 60,
    maxAdvanceDays: 30,
    respectWorkingHours: true,
    addConferencing: false,
    active: true,
    createdAt: new Date().toISOString(),
    teamId: 'team-1',
    memberUserIds: ['u2'],
    ...overrides,
  };
}

function renderComponent() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <BookingLinks />
    </QueryClientProvider>
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  fetchCalendarsMock.mockResolvedValue([CALENDAR]);
  fetchSchedulingTeamsMock.mockResolvedValue([TEAM]);
  fetchTeamMembersMock.mockResolvedValue(MEMBERS);
});

describe('BookingLinks — team links', () => {
  it('badges team links in the list', async () => {
    fetchBookingLinksMock.mockResolvedValue([makeTeamLink()]);
    renderComponent();

    await screen.findByText('Sales intro');
    expect(screen.getByText('Team')).toBeInTheDocument();
  });

  it('creates a team link with selected members', async () => {
    fetchBookingLinksMock.mockResolvedValue([]);
    createBookingLinkApiMock.mockResolvedValue(makeTeamLink());
    renderComponent();
    await screen.findByText(/No booking links yet/);

    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: /New booking link/ }));

    await user.type(await screen.findByLabelText('Title'), 'Sales intro');
    await user.type(screen.getByLabelText('Slug'), 'team-intro');
    await user.click(screen.getByRole('combobox', { name: /^Calendar$/ }));
    await user.click(await screen.findByRole('option', { name: 'Work' }));

    await user.click(screen.getByRole('combobox', { name: /Team \(optional\)/ }));
    await user.click(await screen.findByRole('option', { name: 'Sales' }));

    // Member list loads for the selected team; include u2.
    await waitFor(() => expect(fetchTeamMembersMock).toHaveBeenCalledWith('team-1'));
    await user.click(await screen.findByRole('switch', { name: 'Include member u2' }));

    await user.click(screen.getByRole('button', { name: /Create booking link/ }));

    await waitFor(() =>
      expect(createBookingLinkApiMock).toHaveBeenCalledWith(
        expect.objectContaining({ teamId: 'team-1', memberUserIds: ['u2'] })
      )
    );
  });

  it('labels member toggles with server-resolved names, email then id fallback (F2)', async () => {
    fetchBookingLinksMock.mockResolvedValue([]);
    fetchTeamMembersMock.mockResolvedValue(NAMED_MEMBERS);
    renderComponent();
    await screen.findByText(/No booking links yet/);

    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: /New booking link/ }));
    await user.click(await screen.findByRole('combobox', { name: /Team \(optional\)/ }));
    await user.click(await screen.findByRole('option', { name: 'Sales' }));
    await waitFor(() => expect(fetchTeamMembersMock).toHaveBeenCalledWith('team-1'));

    expect(await screen.findByRole('switch', { name: 'Include member Uma Two' })).toBeInTheDocument();
    expect(screen.getByRole('switch', { name: 'Include member u3@x.com' })).toBeInTheDocument();
    // Toggling still records the stable user id, not the label.
    await user.click(screen.getByRole('switch', { name: 'Include member Uma Two' }));
    await user.type(await screen.findByLabelText('Title'), 'Sales intro');
    await user.type(screen.getByLabelText('Slug'), 'team-intro');
    await user.click(screen.getByRole('combobox', { name: /^Calendar$/ }));
    await user.click(await screen.findByRole('option', { name: 'Work' }));
    createBookingLinkApiMock.mockResolvedValue(makeTeamLink());
    await user.click(screen.getByRole('button', { name: /Create booking link/ }));
    await waitFor(() =>
      expect(createBookingLinkApiMock).toHaveBeenCalledWith(
        expect.objectContaining({ memberUserIds: ['u2'] })
      )
    );
  });

  it('sends a null team for personal links', async () => {
    fetchBookingLinksMock.mockResolvedValue([]);
    createBookingLinkApiMock.mockResolvedValue(makeTeamLink({ teamId: null, memberUserIds: [] }));
    renderComponent();
    await screen.findByText(/No booking links yet/);

    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: /New booking link/ }));

    await user.type(await screen.findByLabelText('Title'), 'Solo intro');
    await user.type(screen.getByLabelText('Slug'), 'solo-intro');
    await user.click(screen.getByRole('combobox', { name: /^Calendar$/ }));
    await user.click(await screen.findByRole('option', { name: 'Work' }));

    await user.click(screen.getByRole('button', { name: /Create booking link/ }));

    await waitFor(() =>
      expect(createBookingLinkApiMock).toHaveBeenCalledWith(
        expect.objectContaining({ teamId: null, memberUserIds: [] })
      )
    );
  });
});
