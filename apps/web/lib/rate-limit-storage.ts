import type { BetterAuthRateLimitOptions, DBAdapter, RateLimit } from 'better-auth';

import { rateLimitRules } from '@/lib/auth-env';

type RateLimitStorage = NonNullable<BetterAuthRateLimitOptions['customStorage']>;
interface Rule {
  window: number;
  max: number;
}

const MODEL = 'rateLimit';
/** Default rule for every auth path without a custom rule. */
const DEFAULT_RULE: Rule = { window: 60, max: 100 };
/** Longest window among Better Auth's built-in special rules (sign-in 10 s, email sends 60 s). */
const BUILTIN_SPECIAL_WINDOW = 60;

/**
 * "lastRequest" is an int8 column (migration 0028) and node-postgres returns
 * int8 as a STRING; Better Auth only converts real bigints, so its own
 * Retry-After became string concatenation. Accepts number, bigint or string.
 */
export function epochMs(value: unknown): number {
  const n = typeof value === 'number' ? value : Number(value);
  return Number.isFinite(n) ? n : 0;
}

/** Seconds until `lastRequest + window` frees up, never below 1. */
function retryAfterSeconds(lastRequest: number, windowSeconds: number, now: number): number {
  return Math.max(1, Math.ceil((lastRequest + windowSeconds * 1000 - now) / 1000));
}

/**
 * Database-backed rate-limit storage over Better Auth's own adapter (the same
 * "rateLimit" table and atomic consume steps as its built-in database
 * storage) with two corrections:
 *
 * - "lastRequest" is coerced to a number on every read (pg int8 → string), so
 *   X-Retry-After is real seconds.
 * - Expired-row pruning keeps every row younger than `retentionSeconds` (the
 *   LONGEST configured window). Better Auth's built-in prune cuts at
 *   max(top-level window, built-in rules) = 60 s and ignores customRules, so
 *   the first window rollover of ANY client deleted the 600 s email rows and
 *   turned 3 per 600 s into roughly 3 per 60 s.
 */
export function createRateLimitStorage(getAdapter: () => Promise<DBAdapter>, retentionSeconds: number): RateLimitStorage {
  async function read(adapter: DBAdapter, key: string): Promise<RateLimit | null> {
    const row = (await adapter.findMany<RateLimit>({ model: MODEL, where: [{ field: 'key', value: key }] }))[0];
    return row ? { ...row, count: Number(row.count), lastRequest: epochMs(row.lastRequest) } : null;
  }

  function prune(adapter: DBAdapter, now: number): void {
    const cutoff = now - retentionSeconds * 1000;
    adapter.deleteMany({ model: MODEL, where: [{ field: 'lastRequest', operator: 'lt', value: cutoff }] }).catch((err: unknown) => {
      console.error('auth: pruning rate-limit rows failed', err);
    });
  }

  async function consume(key: string, rule: Rule): Promise<{ allowed: boolean; retryAfter: number | null }> {
    const adapter = await getAdapter();
    const windowMs = rule.window * 1000;
    const now = Date.now();
    const row = await read(adapter, key);

    if (!row) {
      try {
        await adapter.create({ model: MODEL, data: { key, count: 1, lastRequest: now } });
        return { allowed: true, retryAfter: null };
      } catch (err) {
        // Lost the insert race on the unique key: re-run against the winner's row.
        if (!(await read(adapter, key))) throw err;
        return consume(key, rule);
      }
    }

    if (now - row.lastRequest > windowMs) {
      // Window over: restart it, guarded so concurrent restarts collapse to one.
      const restarted = await adapter.incrementOne({
        model: MODEL,
        where: [
          { field: 'key', value: key },
          { field: 'lastRequest', operator: 'lte', value: row.lastRequest },
        ],
        increment: {},
        set: { count: 1, lastRequest: now },
      });
      if (!restarted) return consume(key, rule);
      prune(adapter, now);
      return { allowed: true, retryAfter: null };
    }

    const counted = await adapter.incrementOne({
      model: MODEL,
      where: [
        { field: 'key', value: key },
        { field: 'lastRequest', operator: 'gt', value: now - windowMs },
        { field: 'count', operator: 'lt', value: rule.max },
      ],
      increment: { count: 1 },
      set: { lastRequest: now },
    });
    if (counted) return { allowed: true, retryAfter: null };

    const fresh = await read(adapter, key);
    if (!fresh || now - fresh.lastRequest > windowMs) return consume(key, rule);
    return { allowed: false, retryAfter: retryAfterSeconds(fresh.lastRequest, rule.window, now) };
  }

  return {
    async get(key) {
      return read(await getAdapter(), key);
    },
    async set(key, value, update) {
      const adapter = await getAdapter();
      if (update) {
        await adapter.updateMany({ model: MODEL, where: [{ field: 'key', value: key }], update: { count: value.count, lastRequest: value.lastRequest } });
      } else {
        await adapter.create({ model: MODEL, data: { key, count: value.count, lastRequest: value.lastRequest } });
      }
    },
    consume,
  };
}

/**
 * Better Auth `rateLimit` options for lib/auth.ts: per-IP limits in Postgres
 * (replica-safe, restart-safe; keys are ip+path) with the custom rules from
 * lib/auth-env.ts, stored through createRateLimitStorage so every rule's
 * window — including the 600 s email rules — actually holds.
 */
export function rateLimitConfig(getAdapter: () => Promise<DBAdapter>): BetterAuthRateLimitOptions {
  const customRules = rateLimitRules();
  const retentionSeconds = Math.max(DEFAULT_RULE.window, BUILTIN_SPECIAL_WINDOW, ...Object.values(customRules).map((r) => r.window));
  return {
    enabled: true,
    storage: 'database',
    window: DEFAULT_RULE.window,
    max: DEFAULT_RULE.max,
    customRules,
    customStorage: createRateLimitStorage(getAdapter, retentionSeconds),
  };
}
