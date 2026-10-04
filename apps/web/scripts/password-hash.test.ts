// @vitest-environment node
import { readFileSync } from 'node:fs';
import path from 'node:path';

import { hashPassword as betterAuthHash, verifyPassword as betterAuthVerify } from 'better-auth/crypto';
import { describe, expect, it } from 'vitest';

import { hashPassword, verifyPassword } from './password-hash.mjs';

/**
 * scripts/reset-password.mjs runs in the production image, where Next's
 * standalone trace does not ship better-auth. Its hash must therefore be a
 * node:crypto re-implementation that Better Auth accepts byte for byte.
 */
describe('operator CLI password hash (node:crypto scrypt, Better Auth format)', () => {
  it('produces salt:key hex in Better Auth\'s layout (16-byte salt, 64-byte key)', async () => {
    const hash = await hashPassword('correct-horse-battery');
    expect(hash).toMatch(/^[0-9a-f]{32}:[0-9a-f]{128}$/);
    expect(await hashPassword('correct-horse-battery')).not.toBe(hash); // random salt
  });

  it('a hash it produces verifies with Better Auth\'s verifyPassword', async () => {
    const hash = await hashPassword('Temporär-Passwört-１２');
    expect(await betterAuthVerify({ hash, password: 'Temporär-Passwört-１２' })).toBe(true);
    expect(await betterAuthVerify({ hash, password: 'Temporär-Passwört-12x' })).toBe(false);
  });

  it('verifies a hash produced by Better Auth\'s hashPassword', async () => {
    const hash = await betterAuthHash('correct-horse-battery');
    expect(await verifyPassword(hash, 'correct-horse-battery')).toBe(true);
    expect(await verifyPassword(hash, 'correct-horse-batterz')).toBe(false);
  });

  it('applies the same NFKC normalisation as Better Auth', async () => {
    // U+FF21 (fullwidth A) normalises to "A" under NFKC.
    const hash = await betterAuthHash('Ａbcdefghijk');
    expect(await verifyPassword(hash, 'Abcdefghijk')).toBe(true);
  });

  it('reset-password.mjs imports nothing the standalone image lacks (no better-auth)', () => {
    const source = readFileSync(path.join(__dirname, 'reset-password.mjs'), 'utf8');
    const specifiers = [...source.matchAll(/^\s*import\s[^'"]*['"]([^'"]+)['"]/gm)].map((m) => m[1]);
    expect(specifiers.length).toBeGreaterThan(0);
    for (const spec of specifiers) {
      expect(spec === 'pg' || spec?.startsWith('node:') || spec === './password-hash.mjs').toBe(true);
    }
  });
});
