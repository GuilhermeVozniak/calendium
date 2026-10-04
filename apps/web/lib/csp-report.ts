/** One normalised CSP violation, whichever wire format delivered it. */
export type CspViolation = {
  documentUri: string;
  violatedDirective: string;
  blockedUri: string;
  sourceFile: string;
  lineNumber: number | null;
};

export const MAX_CSP_REPORT_BYTES = 16 * 1024;
/** Reports logged per request; the rest of a batch is dropped. */
export const MAX_CSP_REPORTS_PER_REQUEST = 5;
/** Every logged string field is cut to this many characters… */
export const MAX_CSP_FIELD_CHARS = 200;
/** …and the User-Agent, repeated on each line, to this many. */
export const MAX_CSP_USER_AGENT_CHARS = 120;

type Fields = Record<string, unknown>;

function isObject(v: unknown): v is Fields {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

function str(v: unknown): string {
  return typeof v === 'string' ? v.slice(0, MAX_CSP_FIELD_CHARS) : '';
}

function num(v: unknown): number | null {
  return typeof v === 'number' && Number.isFinite(v) ? v : null;
}

/**
 * Path segments whose FOLLOWING segments are bearer capabilities: shared
 * threads (/shared/<token>), polls (/poll/<token>), booking links
 * (/book/<slug>, /booking/<token>) and Better Auth's path-style reset link
 * (/api/auth/reset-password/<token>). Compared decoded and lower-cased.
 */
const TOKEN_PARENTS = new Set(['shared', 'poll', 'book', 'booking', 'reset-password', 'verify-email']);

/** CSP keywords browsers put in blocked-uri instead of a URL ("inline", "eval", "wasm-eval", …). */
const CSP_KEYWORD = /^[a-z][a-z0-9-]{0,39}$/;

function decodeSegment(segment: string): string {
  try {
    return decodeURIComponent(segment).toLowerCase();
  } catch {
    return segment.toLowerCase();
  }
}

function redactPath(pathname: string): string {
  let redacting = false;
  return pathname
    .split('/')
    .map((segment) => {
      if (segment === '') return segment;
      if (redacting) return ':token';
      if (TOKEN_PARENTS.has(decodeSegment(segment))) redacting = true;
      return segment;
    })
    .join('/');
}

/**
 * A report URL reduced to what is safe to log (spec: secrets never logged):
 * http(s)/ws(s) URLs become origin + path with capability tokens replaced
 * by ":token" and the query, fragment and userinfo dropped; other schemes
 * (data:, blob:, extensions) only their scheme; CSP keywords stay; anything
 * else is dropped.
 */
export function redactReportUrl(raw: string): string {
  if (raw === '') return '';
  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    return CSP_KEYWORD.test(raw) ? raw : '';
  }
  if (!['http:', 'https:', 'ws:', 'wss:'].includes(url.protocol)) return url.protocol.slice(0, MAX_CSP_FIELD_CHARS);
  return `${url.origin}${redactPath(url.pathname)}`.slice(0, MAX_CSP_FIELD_CHARS);
}

function violation(documentUri: unknown, directive: unknown, blockedUri: unknown, sourceFile: unknown, line: unknown): CspViolation {
  const url = (v: unknown) => redactReportUrl(typeof v === 'string' ? v : '');
  return {
    documentUri: url(documentUri),
    violatedDirective: str(directive),
    blockedUri: url(blockedUri),
    sourceFile: url(sourceFile),
    lineNumber: num(line),
  };
}

/**
 * Accepts the legacy `report-uri` body ({"csp-report": {...}}) and the
 * Reporting API array ([{type:"csp-violation", body:{...}}]); anything
 * else yields no reports (the route still answers 204 — no oracle). URLs
 * are redacted and every field truncated (see redactReportUrl).
 */
export function parseCspReports(parsed: unknown): CspViolation[] {
  if (Array.isArray(parsed)) {
    return parsed
      .filter((r): r is { type: string; body: Fields } => isObject(r) && r.type === 'csp-violation' && isObject(r.body))
      .map(({ body }) => violation(body.documentURL, body.effectiveDirective, body.blockedURL, body.sourceFile, body.lineNumber));
  }
  if (isObject(parsed) && isObject(parsed['csp-report'])) {
    const r = parsed['csp-report'];
    return [violation(r['document-uri'], r['violated-directive'], r['blocked-uri'], r['source-file'], r['line-number'])];
  }
  return [];
}

export interface CspReportLimiter {
  /** Takes up to `wanted` reports from `key`'s budget; returns how many may be logged. */
  take(key: string, wanted: number): number;
  size(): number;
}

/**
 * In-memory fixed-window budget of reports per client IP (default 60 a
 * minute). At most `maxKeys` windows are kept: at capacity the expired ones
 * are dropped first, then the oldest. Per process, like the reports
 * themselves; a restart only resets the budget.
 */
export function createCspReportLimiter({
  limit = 60,
  windowMs = 60_000,
  maxKeys = 10_000,
  now = Date.now,
}: { limit?: number; windowMs?: number; maxKeys?: number; now?: () => number } = {}): CspReportLimiter {
  const windows = new Map<string, { start: number; used: number }>();
  return {
    take(key, wanted) {
      const t = now();
      let w = windows.get(key);
      if (w && t - w.start >= windowMs) {
        windows.delete(key);
        w = undefined;
      }
      if (!w) {
        if (windows.size >= maxKeys) {
          for (const [k, v] of windows) if (t - v.start >= windowMs) windows.delete(k);
          while (windows.size >= maxKeys) {
            const oldest = windows.keys().next().value;
            if (oldest === undefined) break;
            windows.delete(oldest);
          }
        }
        w = { start: t, used: 0 };
        windows.set(key, w);
      }
      const granted = Math.max(0, Math.min(wanted, limit - w.used));
      w.used += granted;
      return granted;
    },
    size: () => windows.size,
  };
}
