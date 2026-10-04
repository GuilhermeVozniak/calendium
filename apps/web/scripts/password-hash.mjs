/**
 * Better Auth's password hash, re-implemented on node:crypto alone so the
 * operator CLI (reset-password.mjs) runs in the production image: Next's
 * standalone trace ships `pg` but not better-auth or its crypto deps.
 *
 * Format (better-auth 1.6 / @better-auth/utils password.node): scrypt with
 * N=16384, r=16, p=1, a 64-byte key; the password is NFKC-normalised; the
 * salt is 16 random bytes HEX-ENCODED and that hex STRING is the scrypt salt;
 * stored as `<salt hex>:<key hex>`. scripts/password-hash.test.ts proves
 * round-trips against better-auth's own hashPassword/verifyPassword.
 */
import { randomBytes, scrypt, timingSafeEqual } from 'node:crypto';

const N = 16384;
const r = 16;
const p = 1;
const KEY_LENGTH = 64;

/**
 * @param {string} password
 * @param {string} salt
 * @returns {Promise<Buffer>}
 */
function deriveKey(password, salt) {
  return new Promise((resolve, reject) => {
    scrypt(password.normalize('NFKC'), salt, KEY_LENGTH, { N, r, p, maxmem: 128 * N * r * 2 }, (err, key) => {
      if (err) reject(err);
      else resolve(key);
    });
  });
}

/**
 * @param {string} password
 * @returns {Promise<string>} `<salt hex>:<key hex>`
 */
export async function hashPassword(password) {
  const salt = randomBytes(16).toString('hex');
  const key = await deriveKey(password, salt);
  return `${salt}:${key.toString('hex')}`;
}

/**
 * @param {string} hash `<salt hex>:<key hex>`
 * @param {string} password
 * @returns {Promise<boolean>}
 */
export async function verifyPassword(hash, password) {
  const [salt, key] = hash.split(':');
  if (!salt || !key) throw new Error('Invalid password hash');
  const expected = Buffer.from(key, 'hex');
  const actual = await deriveKey(password, salt);
  return expected.length === actual.length && timingSafeEqual(expected, actual);
}
