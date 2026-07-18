const mockListClassifiers = jest.fn();
const mockCreateClassifier = jest.fn();
const mockUpdateClassifier = jest.fn();
const mockDeleteClassifier = jest.fn();
jest.mock('@/lib/api', () => ({
  api: {
    listClassifiers: (...args: unknown[]) => mockListClassifiers(...args),
    createClassifier: (...args: unknown[]) => mockCreateClassifier(...args),
    updateClassifier: (...args: unknown[]) => mockUpdateClassifier(...args),
    deleteClassifier: (...args: unknown[]) => mockDeleteClassifier(...args),
  },
}));

const mockBack = jest.fn();
const mockCanGoBack = jest.fn(() => true);
jest.mock('expo-router', () => ({
  useRouter: () => ({ back: mockBack, canGoBack: mockCanGoBack, replace: jest.fn() }),
}));

jest.mock('react-native-safe-area-context', () => ({
  useSafeAreaInsets: () => ({ top: 0, bottom: 0, left: 0, right: 0 }),
}));

import { act, fireEvent, render, screen } from '@testing-library/react-native';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { Alert } from 'react-native';
import ClassifiersScreen from './classifiers';

// Confirmation alerts in this screen are always [Cancel, Delete] — pressing
// the destructive (last) button simulates the user confirming.
let alertSpy: jest.SpyInstance;

const CLASSIFIER: {
  id: string;
  name: string;
  prompt: string;
  targetSplit: 'other';
  labelName: string;
  enabled: boolean;
} = {
  id: 'clf_1',
  name: 'Receipts & invoices',
  prompt: 'Purchase receipts or invoices.',
  targetSplit: 'other',
  labelName: 'Receipts',
  enabled: true,
};

// useQuery/useMutation resolve on a real macrotask; `waitFor` is unreliable in
// this jest-expo + React 19 setup (see hooks/use-push-registration.test.ts),
// so tests drive a tick inside `act()` and assert directly afterward.
async function flush() {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 50));
  });
}

function renderScreen() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <ClassifiersScreen />
    </QueryClientProvider>
  );
}

beforeEach(() => {
  jest.clearAllMocks();
  mockCanGoBack.mockReturnValue(true);
  alertSpy = jest.spyOn(Alert, 'alert').mockImplementation((_title, _message, buttons) => {
    buttons?.[buttons.length - 1]?.onPress?.();
  });
});

afterEach(() => {
  alertSpy.mockRestore();
});

describe('ClassifiersScreen', () => {
  it('lists existing classifiers with their split/label badges', async () => {
    mockListClassifiers.mockResolvedValue([CLASSIFIER]);
    await renderScreen();
    await flush();

    expect(screen.getByText('Receipts & invoices')).toBeTruthy();
    expect(screen.getByText('Purchase receipts or invoices.')).toBeTruthy();
    expect(screen.getByText('Other')).toBeTruthy();
    expect(screen.getByText('Receipts')).toBeTruthy();
  });

  it('creates a new classifier through the inline form (full CRUD, not a web link)', async () => {
    mockListClassifiers.mockResolvedValue([]);
    mockCreateClassifier.mockResolvedValue({
      id: 'clf_new',
      name: 'Travel',
      prompt: 'Flight confirmations.',
      enabled: true,
    });
    await renderScreen();
    await flush();

    await fireEvent.press(screen.getByTestId('add-classifier'));
    await fireEvent.changeText(screen.getByPlaceholderText('e.g. Receipts & invoices'), 'Travel');
    await fireEvent.changeText(
      screen.getByPlaceholderText('Describe the mail this should match, in plain language'),
      'Flight confirmations.'
    );
    await fireEvent.press(screen.getByText('Save'));
    await flush();

    expect(mockCreateClassifier).toHaveBeenCalledWith({
      name: 'Travel',
      prompt: 'Flight confirmations.',
      targetSplit: undefined,
      labelName: undefined,
      enabled: true,
    });
  });

  it('edits an existing classifier by tapping its row', async () => {
    mockListClassifiers.mockResolvedValue([CLASSIFIER]);
    mockUpdateClassifier.mockResolvedValue({ ...CLASSIFIER, name: 'Receipts (updated)' });
    await renderScreen();
    await flush();

    await fireEvent.press(screen.getByText('Receipts & invoices'));
    await fireEvent.changeText(screen.getByDisplayValue('Receipts & invoices'), 'Receipts (updated)');
    await fireEvent.press(screen.getByText('Save'));
    await flush();

    expect(mockUpdateClassifier).toHaveBeenCalledWith('clf_1', {
      name: 'Receipts (updated)',
      prompt: 'Purchase receipts or invoices.',
      targetSplit: 'other',
      labelName: 'Receipts',
      enabled: true,
    });
  });

  it('deletes a classifier after confirmation', async () => {
    mockListClassifiers.mockResolvedValue([CLASSIFIER]);
    mockDeleteClassifier.mockResolvedValue(undefined);
    await renderScreen();
    await flush();

    await fireEvent.press(screen.getByTestId('delete-clf_1'));
    await flush();

    expect(mockDeleteClassifier).toHaveBeenCalledWith('clf_1');
  });
});
