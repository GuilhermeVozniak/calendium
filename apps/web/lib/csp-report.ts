/** One normalised CSP violation, whichever wire format delivered it. */
export type CspViolation = {
  documentUri: string;
  violatedDirective: string;
  blockedUri: string;
  sourceFile: string;
  lineNumber: number | null;
};

export const MAX_CSP_REPORT_BYTES = 16 * 1024;

type Fields = Record<string, unknown>;

function isObject(v: unknown): v is Fields {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

function str(v: unknown): string {
  return typeof v === 'string' ? v : '';
}

function num(v: unknown): number | null {
  return typeof v === 'number' && Number.isFinite(v) ? v : null;
}

/**
 * Accepts the legacy `report-uri` body ({"csp-report": {...}}) and the
 * Reporting API array ([{type:"csp-violation", body:{...}}]); anything
 * else yields no reports (the route still answers 204 — no oracle).
 */
export function parseCspReports(parsed: unknown): CspViolation[] {
  if (Array.isArray(parsed)) {
    return parsed
      .filter((r): r is { type: string; body: Fields } => isObject(r) && r.type === 'csp-violation' && isObject(r.body))
      .map(({ body }) => ({
        documentUri: str(body.documentURL),
        violatedDirective: str(body.effectiveDirective),
        blockedUri: str(body.blockedURL),
        sourceFile: str(body.sourceFile),
        lineNumber: num(body.lineNumber),
      }));
  }
  if (isObject(parsed) && isObject(parsed['csp-report'])) {
    const r = parsed['csp-report'];
    return [
      {
        documentUri: str(r['document-uri']),
        violatedDirective: str(r['violated-directive']),
        blockedUri: str(r['blocked-uri']),
        sourceFile: str(r['source-file']),
        lineNumber: num(r['line-number']),
      },
    ];
  }
  return [];
}
