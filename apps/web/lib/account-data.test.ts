import { afterEach, describe, expect, it, vi } from 'vitest';

const downloadExport = vi.fn();
vi.mock('@/lib/api', () => ({ getApiClient: () => ({ downloadExport }) }));

import { downloadExportApi, exportFilename, retryMessage, saveBlob } from '@/lib/account-data';

afterEach(() => {
  vi.restoreAllMocks();
});

describe('exportFilename', () => {
  it('uses the calendar date', () => {
    expect(exportFilename(new Date(2026, 9, 4, 15, 30))).toBe('calendium-export-2026-10-04.zip');
  });
});

describe('retryMessage', () => {
  it('rounds up to whole minutes and never says 0', () => {
    expect(retryMessage(59)).toBe('You exported your data recently. Try again in 1 minute.');
    expect(retryMessage(61)).toBe('You exported your data recently. Try again in 2 minutes.');
    expect(retryMessage(1800)).toBe('You exported your data recently. Try again in 30 minutes.');
  });
});

describe('downloadExportApi', () => {
  it('delegates to the shared client', async () => {
    const blob = new Blob(['PK']);
    downloadExport.mockResolvedValue(blob);
    await expect(downloadExportApi()).resolves.toBe(blob);
  });
});

describe('saveBlob', () => {
  it('clicks a transient download link and revokes the object URL', () => {
    const createObjectURL = vi.fn(() => 'blob:x');
    const revokeObjectURL = vi.fn();
    Object.assign(URL, { createObjectURL, revokeObjectURL });
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
    const blob = new Blob(['PK']);
    saveBlob(blob, 'calendium-export-2026-10-04.zip');
    expect(createObjectURL).toHaveBeenCalledWith(blob);
    const anchor = click.mock.contexts[0] as HTMLAnchorElement;
    expect(anchor.download).toBe('calendium-export-2026-10-04.zip');
    expect(anchor.href).toBe('blob:x');
    expect(anchor.isConnected).toBe(false);
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:x');
  });
});
