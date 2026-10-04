import { MAX_CSP_REPORT_BYTES, parseCspReports } from '@/lib/csp-report';

export const dynamic = 'force-dynamic';

/**
 * Reads at most `limit` bytes of the body; null when it is larger. Streams so
 * a chunked body without Content-Length cannot make us buffer it whole.
 */
async function readCapped(req: Request, limit: number): Promise<string | null> {
  if (!req.body) return '';
  const reader = req.body.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    total += value.byteLength;
    if (total > limit) {
      await reader.cancel();
      return null;
    }
    chunks.push(value);
  }
  const bytes = new Uint8Array(total);
  let offset = 0;
  for (const c of chunks) {
    bytes.set(c, offset);
    offset += c.byteLength;
  }
  return new TextDecoder().decode(bytes);
}

/**
 * CSP violation sink: one console.warn JSON line per report (picked up by
 * the container's log shipper), no storage. Rejects anything that is not a
 * CSP report body (400) or is over 16 KiB (413).
 */
export async function POST(req: Request): Promise<Response> {
  const type = req.headers.get('content-type') ?? '';
  if (!type.startsWith('application/csp-report') && !type.startsWith('application/reports+json')) {
    return new Response(null, { status: 400 });
  }
  const declared = Number(req.headers.get('content-length') ?? '0');
  if (declared > MAX_CSP_REPORT_BYTES) return new Response(null, { status: 413 });
  const text = await readCapped(req, MAX_CSP_REPORT_BYTES);
  if (text === null) return new Response(null, { status: 413 });
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch {
    return new Response(null, { status: 400 });
  }
  const userAgent = req.headers.get('user-agent') ?? '';
  for (const report of parseCspReports(parsed)) {
    console.warn(JSON.stringify({ msg: 'csp_violation', ...report, userAgent }));
  }
  return new Response(null, { status: 204 });
}
