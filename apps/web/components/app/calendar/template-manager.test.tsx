import type { Calendar, EventTemplate } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { TemplateManager } from '@/components/app/calendar/template-manager';

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

const fetchEventTemplatesMock = vi.fn();
const createEventTemplateApiMock = vi.fn();
const updateEventTemplateApiMock = vi.fn();
const deleteEventTemplateApiMock = vi.fn();
vi.mock('@/lib/template-data', () => ({
  fetchEventTemplates: (...args: unknown[]) => fetchEventTemplatesMock(...args),
  createEventTemplateApi: (...args: unknown[]) => createEventTemplateApiMock(...args),
  updateEventTemplateApi: (...args: unknown[]) => updateEventTemplateApiMock(...args),
  deleteEventTemplateApi: (...args: unknown[]) => deleteEventTemplateApiMock(...args),
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

const CAL_WORK: Calendar = {
  id: 'cal-work',
  accountId: 'acc1',
  name: 'Work',
  color: '#3b82f6',
  timeZone: 'UTC',
  isPrimary: true,
  isVisible: true,
  canWrite: true,
};

const TEMPLATE_1: EventTemplate = {
  id: 'tpl-1',
  name: '1:1',
  title: '1:1 with teammate',
  description: 'Weekly sync',
  location: 'Zoom',
  durationMinutes: 30,
  allDay: false,
  calendarId: 'cal-work',
  attendeeEmails: [],
  addConferencing: true,
  reminderMinutes: [10],
  recurrenceRule: null,
  usageCount: 4,
};

function renderManager() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const onOpenChange = vi.fn();
  const utils = render(
    <QueryClientProvider client={queryClient}>
      <TemplateManager open onOpenChange={onOpenChange} />
    </QueryClientProvider>
  );
  return { ...utils, onOpenChange };
}

beforeEach(() => {
  vi.clearAllMocks();
  fetchEventTemplatesMock.mockResolvedValue([TEMPLATE_1]);
  fetchCalendarsMock.mockResolvedValue([CAL_WORK]);
  createEventTemplateApiMock.mockResolvedValue({ ...TEMPLATE_1, id: 'tpl-new' });
  updateEventTemplateApiMock.mockResolvedValue({ ...TEMPLATE_1 });
  deleteEventTemplateApiMock.mockResolvedValue(undefined);
});

describe('TemplateManager — listing', () => {
  it('lists templates fetched from the API', async () => {
    renderManager();
    expect(await screen.findByText('1:1')).toBeInTheDocument();
    expect(screen.getByText('1:1 with teammate')).toBeInTheDocument();
    expect(screen.getByText('used 4x')).toBeInTheDocument();
  });

  it('shows an empty state when there are no templates', async () => {
    fetchEventTemplatesMock.mockResolvedValue([]);
    renderManager();
    expect(await screen.findByText(/no templates yet/i)).toBeInTheDocument();
  });
});

describe('TemplateManager — create', () => {
  it('disables the create button until a name is entered', async () => {
    const user = userEvent.setup();
    renderManager();
    await screen.findByText('1:1');
    await user.click(screen.getByRole('button', { name: /new template/i }));

    const createButton = await screen.findByRole('button', { name: /create template/i });
    expect(createButton).toBeDisabled();

    await user.type(screen.getByLabelText('Name'), 'Focus block');
    expect(createButton).not.toBeDisabled();
  });

  it('creates a template with the entered fields and shows a success toast', async () => {
    const user = userEvent.setup();
    renderManager();
    await screen.findByText('1:1');
    await user.click(screen.getByRole('button', { name: /new template/i }));

    await user.type(await screen.findByLabelText('Name'), 'Focus block');
    await user.type(screen.getByLabelText('Event title'), 'Deep work');
    await user.click(screen.getByRole('button', { name: /create template/i }));

    await waitFor(() => expect(createEventTemplateApiMock).toHaveBeenCalledTimes(1));
    const input = createEventTemplateApiMock.mock.calls[0]![0];
    expect(input.name).toBe('Focus block');
    expect(input.title).toBe('Deep work');
    expect(toastSuccess).toHaveBeenCalledWith('Template created');
  });
});

describe('TemplateManager — edit', () => {
  it('prefills the edit form from the selected template and round-trips changes', async () => {
    const user = userEvent.setup();
    renderManager();
    await screen.findByText('1:1');
    await user.click(screen.getByRole('button', { name: 'Edit template 1:1' }));

    const nameInput = await screen.findByLabelText('Name');
    expect(nameInput).toHaveValue('1:1');
    expect(screen.getByLabelText('Event title')).toHaveValue('1:1 with teammate');
    expect(screen.getByLabelText('Location')).toHaveValue('Zoom');
    expect(screen.getByLabelText('Description')).toHaveValue('Weekly sync');

    await user.clear(nameInput);
    await user.type(nameInput, '1:1 (updated)');
    await user.click(screen.getByRole('button', { name: /save changes/i }));

    await waitFor(() =>
      expect(updateEventTemplateApiMock).toHaveBeenCalledWith(
        'tpl-1',
        expect.objectContaining({ name: '1:1 (updated)', title: '1:1 with teammate' })
      )
    );
    expect(toastSuccess).toHaveBeenCalledWith('Template updated');
  });
});

describe('TemplateManager — delete', () => {
  it('deletes a template, removes it from the list, and shows a success toast', async () => {
    const user = userEvent.setup();
    renderManager();
    await screen.findByText('1:1');
    await user.click(screen.getByRole('button', { name: 'Delete template 1:1' }));

    await waitFor(() => expect(deleteEventTemplateApiMock).toHaveBeenCalledWith('tpl-1'));
    expect(toastSuccess).toHaveBeenCalledWith('Template deleted');
    await waitFor(() => expect(screen.queryByText('1:1')).not.toBeInTheDocument());
  });
});
