import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { AiClassifier } from '@calendium/shared';

const useInstanceMock = vi.fn();
vi.mock('@/lib/use-instance', () => ({
  useInstance: () => useInstanceMock(),
}));

const fetchClassifiersMock = vi.fn();
const createClassifierApiMock = vi.fn();
const updateClassifierApiMock = vi.fn();
const deleteClassifierApiMock = vi.fn();
vi.mock('@/lib/classifiers-data', () => ({
  fetchClassifiers: () => fetchClassifiersMock(),
  createClassifierApi: (...args: unknown[]) => createClassifierApiMock(...args),
  updateClassifierApi: (...args: unknown[]) => updateClassifierApiMock(...args),
  deleteClassifierApi: (...args: unknown[]) => deleteClassifierApiMock(...args),
}));

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    success: (...args: unknown[]) => toastSuccess(...args),
    error: (...args: unknown[]) => toastError(...args),
  },
}));

import ClassifiersPage from './page';

const CLASSIFIER: AiClassifier = {
  id: 'clf_1',
  name: 'Recruiter outreach',
  prompt: 'Cold outreach from a recruiter.',
  targetSplit: 'other',
  labelName: 'Recruiting',
  enabled: true,
};

function renderPage() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <ClassifiersPage />
    </QueryClientProvider>
  );
}

describe('ClassifiersPage', () => {
  beforeEach(() => {
    useInstanceMock.mockReturnValue({ data: { features: { ai: true } } });
    fetchClassifiersMock.mockReset().mockResolvedValue([CLASSIFIER]);
    createClassifierApiMock.mockReset();
    updateClassifierApiMock.mockReset();
    deleteClassifierApiMock.mockReset();
    toastSuccess.mockReset();
    toastError.mockReset();
  });

  it('shows a message instead of the CRUD UI when the server has no AI', () => {
    useInstanceMock.mockReturnValue({ data: { features: { ai: false } } });
    renderPage();
    expect(
      screen.getByText('AI classifiers require AI features to be enabled on this server.')
    ).toBeInTheDocument();
    expect(screen.queryByText('New classifier')).not.toBeInTheDocument();
  });

  it('lists existing classifiers with their split/label badges', async () => {
    renderPage();
    expect(await screen.findByText('Recruiter outreach')).toBeInTheDocument();
    expect(screen.getByText('other')).toBeInTheDocument();
    expect(screen.getByText('Recruiting')).toBeInTheDocument();
  });

  it('validates that a new classifier needs a split or a label', async () => {
    renderPage();
    await userEvent.click(await screen.findByText('New classifier'));
    await userEvent.type(screen.getByLabelText('Name'), 'Newsletters');
    await userEvent.type(screen.getByLabelText('Prompt'), 'Marketing newsletters.');
    await userEvent.click(screen.getByText('Create'));

    expect(
      await screen.findByText('Choose a target split or a label — at least one is required.')
    ).toBeInTheDocument();
    expect(createClassifierApiMock).not.toHaveBeenCalled();
  });

  it('creates a classifier once validation passes', async () => {
    createClassifierApiMock.mockResolvedValue({ ...CLASSIFIER, id: 'clf_2', name: 'Newsletters' });
    renderPage();
    await userEvent.click(await screen.findByText('New classifier'));
    await userEvent.type(screen.getByLabelText('Name'), 'Newsletters');
    await userEvent.type(screen.getByLabelText('Prompt'), 'Marketing newsletters.');
    await userEvent.type(screen.getByLabelText('Label name'), 'Marketing');
    await userEvent.click(screen.getByText('Create'));

    await waitFor(() => expect(createClassifierApiMock).toHaveBeenCalled());
    expect(toastSuccess).toHaveBeenCalledWith('Classifier created');
  });

  it('deletes a classifier only after the mutation resolves', async () => {
    deleteClassifierApiMock.mockResolvedValue(undefined);
    renderPage();
    await userEvent.click(await screen.findByLabelText('Delete Recruiter outreach'));
    await waitFor(() => expect(deleteClassifierApiMock).toHaveBeenCalledWith('clf_1'));
    await waitFor(() => expect(toastSuccess).toHaveBeenCalledWith('Classifier deleted'));
  });
});
