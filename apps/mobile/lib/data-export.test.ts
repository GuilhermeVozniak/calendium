const mockDownloadExport = jest.fn();
jest.mock('@/lib/api', () => ({
  api: { downloadExport: (...args: unknown[]) => mockDownloadExport(...args) },
}));

const mockWrite = jest.fn();
const mockCreate = jest.fn();
const mockFileArgs = jest.fn();
jest.mock('expo-file-system', () => ({
  Paths: { cache: { uri: 'file:///cache/' } },
  File: class {
    uri: string;
    constructor(dir: { uri: string }, name: string) {
      mockFileArgs(dir, name);
      this.uri = `${dir.uri}${name}`;
    }
    create(opts: unknown) {
      mockCreate(opts);
    }
    write(content: string, opts: unknown) {
      mockWrite(content, opts);
    }
  },
}));

const mockIsAvailable = jest.fn();
const mockShareAsync = jest.fn();
jest.mock('expo-sharing', () => ({
  isAvailableAsync: () => mockIsAvailable(),
  shareAsync: (...args: unknown[]) => mockShareAsync(...args),
}));

import { ApiRequestError } from '@calendium/shared';
import { describeExportError, shareDataExport } from './data-export';

/** Minimal FileReader stand-in: RN's readAsDataURL yields a base64 data URL. */
class FakeFileReader {
  result: string | null = null;
  onload: (() => void) | null = null;
  onerror: (() => void) | null = null;
  readAsDataURL(blob: { b64: string }) {
    this.result = `data:application/zip;base64,${blob.b64}`;
    setTimeout(() => this.onload?.(), 0);
  }
}

beforeEach(() => {
  jest.clearAllMocks();
  (global as unknown as { FileReader: unknown }).FileReader = FakeFileReader;
  mockIsAvailable.mockResolvedValue(true);
  mockShareAsync.mockResolvedValue(undefined);
  mockDownloadExport.mockResolvedValue({ b64: 'UEsDBA==' });
});

describe('shareDataExport', () => {
  it('downloads through the API, writes the zip to the cache and opens the share sheet', async () => {
    await shareDataExport(new Date('2026-10-04T12:00:00Z'));
    expect(mockDownloadExport).toHaveBeenCalledTimes(1);
    expect(mockFileArgs).toHaveBeenCalledWith({ uri: 'file:///cache/' }, 'calendium-export-2026-10-04.zip');
    expect(mockCreate).toHaveBeenCalledWith({ overwrite: true });
    expect(mockWrite).toHaveBeenCalledWith('UEsDBA==', { encoding: 'base64' });
    expect(mockShareAsync).toHaveBeenCalledWith(
      'file:///cache/calendium-export-2026-10-04.zip',
      expect.objectContaining({ mimeType: 'application/zip', UTI: 'public.zip-archive' })
    );
  });

  it('fails before downloading when the OS share sheet is unavailable', async () => {
    mockIsAvailable.mockResolvedValue(false);
    await expect(shareDataExport()).rejects.toThrow(/sharing is not available/i);
    expect(mockDownloadExport).not.toHaveBeenCalled();
  });

  it('propagates API errors without writing a file', async () => {
    mockDownloadExport.mockRejectedValue(new ApiRequestError(500, 'internal', 'boom'));
    await expect(shareDataExport()).rejects.toMatchObject({ status: 500 });
    expect(mockWrite).not.toHaveBeenCalled();
    expect(mockShareAsync).not.toHaveBeenCalled();
  });
});

describe('describeExportError', () => {
  it('maps export_throttled to a wait time in minutes', () => {
    const err = new ApiRequestError(409, 'export_throttled', 'throttled');
    err.retryAfterSeconds = 600;
    expect(describeExportError(err)).toBe('You can download another export in 10 minutes.');
  });

  it('surfaces the server message for other API errors', () => {
    expect(describeExportError(new ApiRequestError(503, 'unavailable', 'Export is not available right now.'))).toBe(
      'Export is not available right now.'
    );
  });

  it('falls back to a generic message', () => {
    expect(describeExportError('nope')).toBe('Try again.');
  });
});
