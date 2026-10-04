import { api } from '@/lib/api';
import { ApiRequestError } from '@calendium/shared';
import { File, Paths } from 'expo-file-system';
import * as Sharing from 'expo-sharing';

/** Reads a Blob as base64 (React Native's Blob has no arrayBuffer()). */
function blobToBase64(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      const result = typeof reader.result === 'string' ? reader.result : '';
      resolve(result.slice(result.indexOf(',') + 1));
    };
    reader.onerror = () => reject(new Error('Could not read the export.'));
    reader.readAsDataURL(blob);
  });
}

/**
 * Settings → Account → "Download my data" on iOS/Android: downloads the
 * account's zip through the API (GET /v1/me/export via the shared client's
 * downloadExport, which reuses the cached JWT), writes it to the cache
 * directory and hands it to the OS share sheet (save to Files, AirDrop, mail).
 * No web page is opened, so the store build links nowhere (App Store ruling).
 */
export async function shareDataExport(now: Date = new Date()): Promise<void> {
  if (!(await Sharing.isAvailableAsync())) {
    throw new Error('Sharing is not available on this device.');
  }
  const blob = await api.downloadExport();
  const base64 = await blobToBase64(blob);
  const file = new File(Paths.cache, `calendium-export-${now.toISOString().slice(0, 10)}.zip`);
  file.create({ overwrite: true });
  file.write(base64, { encoding: 'base64' });
  await Sharing.shareAsync(file.uri, {
    mimeType: 'application/zip',
    UTI: 'public.zip-archive',
    dialogTitle: 'Your Calendium data',
  });
}

/** User-facing copy for a failed export. */
export function describeExportError(err: unknown): string {
  if (err instanceof ApiRequestError) {
    if (err.code === 'export_throttled') {
      const minutes = Math.max(1, Math.ceil((err.retryAfterSeconds ?? 60) / 60));
      return `You can download another export in ${minutes} minute${minutes === 1 ? '' : 's'}.`;
    }
    return err.message;
  }
  if (err instanceof Error && err.message) return err.message;
  return 'Try again.';
}
