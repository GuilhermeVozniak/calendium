import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ApiRequestError, type AttachmentHit } from '@calendium/shared';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { AttachmentsPane } from '@/components/app/attachments-pane';

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

const useAttachmentSearchMock = vi.fn();
const fetchAttachmentBlobMock = vi.fn();

vi.mock('@/lib/use-mail', () => ({
  useAttachmentSearch: (...args: unknown[]) => useAttachmentSearchMock(...args),
  fetchAttachmentBlob: (...args: unknown[]) => fetchAttachmentBlobMock(...args),
}));

const checkoutMutateMock = vi.fn();
vi.mock('@/components/app/paywall', () => ({
  useCheckoutMutation: () => ({ mutate: checkoutMutateMock, isPending: false }),
}));

const toastErrorMock = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    error: (...args: unknown[]) => toastErrorMock(...args),
    success: vi.fn(),
  },
}));

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

const HIT_PDF: AttachmentHit = {
  id: 'att_pdf',
  filename: 'Renewal-Summary.pdf',
  mimeType: 'application/pdf',
  sizeBytes: 245_000,
  messageId: 'thr_1_m1',
  threadId: 'thr_1',
  threadSubject: 'Renewal terms for FY27',
  from: { name: 'Daniel Cho', email: 'daniel@northwind.com' },
  sentAt: new Date().toISOString(),
};

const HIT_XLSX: AttachmentHit = {
  id: 'att_xlsx',
  filename: 'Comp-Bands.xlsx',
  mimeType: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
  sizeBytes: 12_000,
  messageId: 'thr_2_m1',
  threadId: 'thr_2',
  threadSubject: 'Offer letter',
  from: { name: 'Laura Kim', email: 'laura@calendium.app' },
  sentAt: new Date().toISOString(),
};

interface SearchResultOverrides {
  items?: AttachmentHit[];
  isLoading?: boolean;
  isError?: boolean;
  error?: unknown;
  hasNextPage?: boolean;
  isFetchingNextPage?: boolean;
  fetchNextPage?: () => void;
}

function makeSearchResult(overrides: SearchResultOverrides = {}) {
  const items = overrides.items ?? [HIT_PDF, HIT_XLSX];
  return {
    data: { pages: [{ page: { items, nextCursor: null }, source: 'api' as const }] },
    isLoading: overrides.isLoading ?? false,
    isError: overrides.isError ?? false,
    error: overrides.error ?? null,
    fetchNextPage: overrides.fetchNextPage ?? vi.fn(),
    hasNextPage: overrides.hasNextPage ?? false,
    isFetchingNextPage: overrides.isFetchingNextPage ?? false,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  useAttachmentSearchMock.mockReturnValue(makeSearchResult());
  fetchAttachmentBlobMock.mockReset();
});

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe('AttachmentsPane — search', () => {
  it('debounces the search input into useAttachmentSearch({ q })', async () => {
    const user = userEvent.setup();
    render(<AttachmentsPane />);

    await user.type(screen.getByPlaceholderText('Search attachments…'), 'invoice');

    // Nothing fired yet — still inside the debounce window.
    expect(useAttachmentSearchMock).not.toHaveBeenCalledWith(
      expect.objectContaining({ q: 'invoice' })
    );

    await waitFor(
      () =>
        expect(useAttachmentSearchMock).toHaveBeenLastCalledWith({
          q: 'invoice',
          contact: undefined,
          threadId: undefined,
        }),
      { timeout: 1000 }
    );
  });
});

describe('AttachmentsPane — contact filter', () => {
  it('applies the initial contact filter to the search and shows a clearable chip', async () => {
    const user = userEvent.setup();
    render(<AttachmentsPane initialFilter={{ contact: 'daniel@northwind.com' }} />);

    await waitFor(() =>
      expect(useAttachmentSearchMock).toHaveBeenLastCalledWith(
        expect.objectContaining({ contact: 'daniel@northwind.com' })
      )
    );
    expect(screen.getByText('From: daniel@northwind.com')).toBeInTheDocument();

    await user.click(screen.getByLabelText('Clear contact filter'));

    await waitFor(() =>
      expect(useAttachmentSearchMock).toHaveBeenLastCalledWith(
        expect.objectContaining({ contact: undefined })
      )
    );
    expect(screen.queryByText('From: daniel@northwind.com')).not.toBeInTheDocument();
  });
});

describe('AttachmentsPane — row click routing', () => {
  it('a PDF row opens the preview dialog with an iframe pointed at the real blob URL', async () => {
    const user = userEvent.setup();
    const revokeMock = vi.fn();
    vi.stubGlobal('URL', { ...URL, revokeObjectURL: revokeMock });
    fetchAttachmentBlobMock.mockResolvedValue({
      blobUrl: 'blob:mock-pdf',
      filename: 'Renewal-Summary.pdf',
      mimeType: 'application/pdf',
    });

    render(<AttachmentsPane />);
    await user.click(screen.getByText('Renewal-Summary.pdf'));

    expect(fetchAttachmentBlobMock).toHaveBeenCalledWith('att_pdf');
    const iframe = await screen.findByTitle('Renewal-Summary.pdf');
    expect(iframe).toHaveAttribute('src', 'blob:mock-pdf');

    // Closing the dialog revokes the object URL (no leaked blob: refs).
    await user.keyboard('{Escape}');
    await waitFor(() => expect(revokeMock).toHaveBeenCalledWith('blob:mock-pdf'));

    vi.unstubAllGlobals();
  });

  it('a non-previewable row downloads the real bytes under the server-reported filename', async () => {
    const user = userEvent.setup();
    const revokeMock = vi.fn();
    vi.stubGlobal('URL', { ...URL, revokeObjectURL: revokeMock });
    let capturedAnchor: HTMLAnchorElement | null = null;
    const clickSpy = vi
      .spyOn(HTMLAnchorElement.prototype, 'click')
      .mockImplementation(function (this: HTMLAnchorElement) {
        capturedAnchor = this;
      });
    fetchAttachmentBlobMock.mockResolvedValue({
      blobUrl: 'blob:mock-xlsx',
      filename: 'Comp-Bands.xlsx',
      mimeType: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
    });

    render(<AttachmentsPane />);
    await user.click(screen.getByText('Comp-Bands.xlsx'));

    await waitFor(() => expect(fetchAttachmentBlobMock).toHaveBeenCalledWith('att_xlsx'));
    await waitFor(() => expect(clickSpy).toHaveBeenCalled());
    expect(capturedAnchor).not.toBeNull();
    expect(capturedAnchor!.download).toBe('Comp-Bands.xlsx');
    expect(capturedAnchor!.href).toContain('blob:mock-xlsx');
    expect(revokeMock).toHaveBeenCalledWith('blob:mock-xlsx');
    // No preview dialog for a non-previewable mime type.
    expect(screen.queryByTitle('Comp-Bands.xlsx')).not.toBeInTheDocument();

    clickSpy.mockRestore();
    vi.unstubAllGlobals();
  });

  it('surfaces a friendly message when the fetch fails (honesty: no fabricated preview)', async () => {
    const user = userEvent.setup();
    fetchAttachmentBlobMock.mockRejectedValue(new Error('network error'));

    render(<AttachmentsPane />);
    await user.click(screen.getByText('Renewal-Summary.pdf'));

    expect(
      await screen.findByText('Could not load the attachment. Please try again.')
    ).toBeInTheDocument();
  });
});

describe('AttachmentsPane — pagination', () => {
  it('shows a Load more button when hasNextPage is true and calls fetchNextPage on click', async () => {
    const user = userEvent.setup();
    const fetchNextPage = vi.fn();
    useAttachmentSearchMock.mockReturnValue(makeSearchResult({ hasNextPage: true, fetchNextPage }));

    render(<AttachmentsPane />);
    await user.click(screen.getByRole('button', { name: 'Load more' }));

    expect(fetchNextPage).toHaveBeenCalledOnce();
  });

  it('omits Load more when there is no next page', () => {
    render(<AttachmentsPane />);
    expect(screen.queryByRole('button', { name: 'Load more' })).not.toBeInTheDocument();
  });
});

describe('AttachmentsPane — grouping + empty/paywall states', () => {
  it('groups results by thread under a subject heading', () => {
    render(<AttachmentsPane />);
    const pdfRow = screen.getByText('Renewal-Summary.pdf').closest('button')!;
    const xlsxRow = screen.getByText('Comp-Bands.xlsx').closest('button')!;
    expect(within(pdfRow.parentElement!).getByText('Renewal terms for FY27')).toBeInTheDocument();
    expect(within(xlsxRow.parentElement!).getByText('Offer letter')).toBeInTheDocument();
  });

  it('shows an honest empty state when there are no results', () => {
    useAttachmentSearchMock.mockReturnValue(makeSearchResult({ items: [] }));
    render(<AttachmentsPane />);
    expect(screen.getByText('No attachments found yet.')).toBeInTheDocument();
  });

  it('shows the 402 paywall pattern instead of a generic error', () => {
    useAttachmentSearchMock.mockReturnValue(
      makeSearchResult({
        items: [],
        isError: true,
        error: new ApiRequestError(402, 'payment_required', 'payment required'),
      })
    );
    render(<AttachmentsPane />);
    expect(screen.getByText('Attachments are part of the paid plan')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Subscribe/ })).toBeInTheDocument();
  });
});
