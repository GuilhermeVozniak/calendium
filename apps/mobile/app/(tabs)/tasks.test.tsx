const mockListTasks = jest.fn();
const mockCompleteTask = jest.fn();
const mockReopenTask = jest.fn();
jest.mock('@/lib/api', () => ({
  api: {
    listTasks: (...args: unknown[]) => mockListTasks(...args),
    completeTask: (...args: unknown[]) => mockCompleteTask(...args),
    reopenTask: (...args: unknown[]) => mockReopenTask(...args),
  },
}));

jest.mock('react-native-safe-area-context', () => ({
  useSafeAreaInsets: () => ({ top: 0, bottom: 0, left: 0, right: 0 }),
}));

import { act, fireEvent, render, screen } from '@testing-library/react-native';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { Task } from '@calendium/shared';
import TasksScreen from './tasks';

function makeTask(overrides: Partial<Task> = {}): Task {
  const now = new Date().toISOString();
  return {
    id: 't1',
    title: 'Prep board deck',
    notes: null,
    due: null,
    allDayDue: false,
    scheduledStart: null,
    scheduledEnd: null,
    completedAt: null,
    source: 'local',
    sourceUrl: null,
    position: 1,
    createdAt: now,
    updatedAt: now,
    ...overrides,
  };
}

// useQuery resolves on a real macrotask; drive a tick inside `act()` (same
// pattern as settings.test.tsx / compose.test.tsx) since `waitFor` is
// unreliable in this jest-expo + React 19 setup.
async function flush() {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 50));
  });
}

function renderScreen() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <TasksScreen />
    </QueryClientProvider>
  );
}

beforeEach(() => {
  jest.clearAllMocks();
  mockListTasks.mockResolvedValue([]);
});

describe('TasksScreen — list', () => {
  it('renders open tasks and a completed group from the API', async () => {
    mockListTasks.mockResolvedValue([
      makeTask(),
      makeTask({
        id: 't2',
        title: 'Renew passport',
        position: 2,
        completedAt: new Date().toISOString(),
      }),
    ]);
    await renderScreen();
    await flush();

    expect(screen.getByText('Prep board deck')).toBeTruthy();
    expect(screen.getByText('Renew passport')).toBeTruthy();
    expect(screen.getByText('Completed (1)')).toBeTruthy();
  });

  it('shows the empty state when there are no tasks', async () => {
    await renderScreen();
    await flush();

    expect(screen.getByText('No tasks yet.')).toBeTruthy();
  });
});

describe('TasksScreen — check-off', () => {
  it('completes a task optimistically before the request settles', async () => {
    mockListTasks.mockResolvedValue([makeTask()]);
    // Never settles: the checked state below can only come from the
    // optimistic cache patch, not the server response.
    mockCompleteTask.mockReturnValue(new Promise(() => {}));
    await renderScreen();
    await flush();

    await fireEvent.press(screen.getByTestId('task-toggle-t1'));
    await flush();

    expect(mockCompleteTask).toHaveBeenCalledWith('t1');
    expect(screen.getByTestId('task-toggle-t1').props.accessibilityState.checked).toBe(true);
  });

  it('reopens a completed task via api.reopenTask', async () => {
    const done = makeTask({ completedAt: new Date().toISOString() });
    mockListTasks.mockResolvedValue([done]);
    mockReopenTask.mockResolvedValue({ ...done, completedAt: null });
    await renderScreen();
    await flush();

    await fireEvent.press(screen.getByTestId('task-toggle-t1'));
    await flush();

    expect(mockReopenTask).toHaveBeenCalledWith('t1');
    expect(screen.getByTestId('task-toggle-t1').props.accessibilityState.checked).toBe(false);
  });

  it('rolls the optimistic flip back when the request fails', async () => {
    mockListTasks.mockResolvedValue([makeTask()]);
    mockCompleteTask.mockRejectedValue(new Error('boom'));
    await renderScreen();
    await flush();

    await fireEvent.press(screen.getByTestId('task-toggle-t1'));
    await flush();

    expect(screen.getByTestId('task-toggle-t1').props.accessibilityState.checked).toBe(false);
  });
});
