import type { Task } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const listTasksMock = vi.fn();
const createTaskMock = vi.fn();
const updateTaskMock = vi.fn();
const completeTaskMock = vi.fn();
const reopenTaskMock = vi.fn();
const deleteTaskMock = vi.fn();
vi.mock('@/lib/api', () => ({
  getApiClient: () => ({
    listTasks: (...args: unknown[]) => listTasksMock(...args),
    createTask: (...args: unknown[]) => createTaskMock(...args),
    updateTask: (...args: unknown[]) => updateTaskMock(...args),
    completeTask: (...args: unknown[]) => completeTaskMock(...args),
    reopenTask: (...args: unknown[]) => reopenTaskMock(...args),
    deleteTask: (...args: unknown[]) => deleteTaskMock(...args),
  }),
}));

const toastError = vi.fn();
vi.mock('sonner', () => ({
  toast: { error: (...args: unknown[]) => toastError(...args), success: vi.fn() },
}));

import { TaskRail } from '@/components/app/task-rail';
import { TASK_DRAG_TYPE } from '@/lib/task-drag';

let seq = 0;
function makeTask(overrides: Partial<Task> = {}): Task {
  const now = new Date().toISOString();
  return {
    id: `task-${++seq}`,
    title: 'A task',
    notes: null,
    due: null,
    allDayDue: false,
    scheduledStart: null,
    scheduledEnd: null,
    completedAt: null,
    source: 'local',
    sourceUrl: null,
    position: seq,
    createdAt: now,
    updatedAt: now,
    ...overrides,
  };
}

function makeDataTransfer(data: Record<string, string> = {}) {
  const store: Record<string, string> = { ...data };
  return {
    setData: vi.fn((type: string, value: string) => {
      store[type] = value;
    }),
    getData: vi.fn((type: string) => store[type] ?? ''),
    get types() {
      return Object.keys(store);
    },
    effectAllowed: '',
    dropEffect: '',
  } as unknown as DataTransfer;
}

const TODAY = new Date();
function todayAt(hours: number, minutes = 0): Date {
  return new Date(TODAY.getFullYear(), TODAY.getMonth(), TODAY.getDate(), hours, minutes);
}

function fixtures(): Task[] {
  return [
    makeTask({ id: 'task-due-1', title: 'Prep board deck', due: todayAt(17).toISOString() }),
    makeTask({ id: 'task-un-1', title: 'Book flights' }),
    makeTask({
      id: 'task-sch-1',
      title: 'Deep work',
      scheduledStart: todayAt(10).toISOString(),
      scheduledEnd: todayAt(10, 30).toISOString(),
    }),
    makeTask({
      id: 'task-done-1',
      title: 'Old thing',
      completedAt: new Date().toISOString(),
    }),
  ];
}

function renderRail() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <TaskRail />
    </QueryClientProvider>
  );
  return client;
}

beforeEach(() => {
  vi.clearAllMocks();
  listTasksMock.mockResolvedValue(fixtures());
});

describe('TaskRail — groups', () => {
  it('renders due-today and unscheduled groups', async () => {
    renderRail();
    expect(await screen.findByText('Prep board deck')).toBeInTheDocument();
    expect(screen.getByText('Due today')).toBeInTheDocument();
    expect(screen.getByText('Unscheduled')).toBeInTheDocument();
    expect(screen.getByText('Book flights')).toBeInTheDocument();
    // Scheduled tasks live on the grid, not in the rail groups.
    expect(screen.queryByText('Deep work')).not.toBeInTheDocument();
    // Completed collapse under the toggle by default.
    expect(screen.queryByText('Old thing')).not.toBeInTheDocument();
  });

  it('expands the completed section from its toggle', async () => {
    renderRail();
    await screen.findByText('Book flights');
    fireEvent.click(screen.getByRole('button', { name: /completed/i }));
    expect(screen.getByText('Old thing')).toBeInTheDocument();
    expect(screen.getByText('Old thing')).toHaveClass('line-through');
  });
});

describe('TaskRail — check-off', () => {
  it('checks a task off in place before the request resolves', async () => {
    completeTaskMock.mockReturnValue(new Promise(() => {})); // never resolves
    renderRail();
    await screen.findByText('Book flights');

    fireEvent.click(screen.getByRole('checkbox', { name: 'Complete "Book flights"' }));

    // Optimistic: strikethrough is applied synchronously, item stays in place.
    expect(completeTaskMock).toHaveBeenCalledWith('task-un-1');
    expect(screen.getByText('Book flights')).toHaveClass('line-through');
    expect(
      screen.getByRole('checkbox', { name: 'Reopen "Book flights"' })
    ).toHaveAttribute('aria-checked', 'true');
  });
});

describe('TaskRail — quick add', () => {
  it('creates a task on Enter and clears the input', async () => {
    createTaskMock.mockResolvedValue(makeTask({ id: 'new-1', title: 'Write tests' }));
    renderRail();
    await screen.findByText('Book flights');

    const user = userEvent.setup();
    const input = screen.getByLabelText('Add a task');
    await user.type(input, 'Write tests{enter}');

    await waitFor(() =>
      expect(createTaskMock).toHaveBeenCalledWith(
        expect.objectContaining({ title: 'Write tests' })
      )
    );
    expect(input).toHaveValue('');
  });

  it('ignores empty submissions', async () => {
    renderRail();
    await screen.findByText('Book flights');
    const user = userEvent.setup();
    await user.type(screen.getByLabelText('Add a task'), '   {enter}');
    expect(createTaskMock).not.toHaveBeenCalled();
  });
});

describe('TaskRail — drag contract', () => {
  it('drag start sets the task drag payload', async () => {
    renderRail();
    await screen.findByText('Book flights');

    const dt = makeDataTransfer();
    fireEvent.dragStart(screen.getByTestId('task-item-task-un-1'), { dataTransfer: dt });
    expect(dt.setData).toHaveBeenCalledWith(TASK_DRAG_TYPE, 'task-un-1');
  });

  it('dropping a scheduled task on the rail clears its timeblock', async () => {
    updateTaskMock.mockReturnValue(new Promise(() => {}));
    renderRail();
    await screen.findByText('Book flights');

    const dt = makeDataTransfer({ [TASK_DRAG_TYPE]: 'task-sch-1' });
    fireEvent.drop(screen.getByTestId('task-rail'), { dataTransfer: dt });
    expect(updateTaskMock).toHaveBeenCalledWith('task-sch-1', {
      scheduledStart: null,
      scheduledEnd: null,
    });
  });
});
