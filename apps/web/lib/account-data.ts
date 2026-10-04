import { format } from 'date-fns';

import { getApiClient } from '@/lib/api';

/**
 * Data-access + browser helpers for Settings → Account. No demo fallback on
 * purpose: in demo mode the UI never calls these (it toasts instead), and
 * outside demo mode a failure must surface honestly.
 */

/**
 * GET /v1/me/export as a Blob (throws ApiRequestError, 409 export_throttled
 * with retryAfterSeconds). The path is not delegable, so an acting-as client
 * never adds the act-as header to it.
 */
export function downloadExportApi(): Promise<Blob> {
  return getApiClient().downloadExport();
}

export function exportFilename(now: Date = new Date()): string {
  return `calendium-export-${format(now, 'yyyy-MM-dd')}.zip`;
}

/** Hands the blob to the browser's download manager via a transient object URL. */
export function saveBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}

export function retryMessage(seconds: number): string {
  const minutes = Math.max(1, Math.ceil(seconds / 60));
  return `You exported your data recently. Try again in ${minutes} minute${minutes === 1 ? '' : 's'}.`;
}
