#!/usr/bin/env node
/**
 * Self-host password reset without email (docs/self-hosting/security.md).
 *
 *   docker compose exec web node apps/web/scripts/reset-password.mjs <email>
 *
 * Hashes a fresh temporary password in Better Auth's scrypt format (see
 * password-hash.mjs — node:crypto only, because the standalone image ships
 * `pg` but not better-auth), upserts the user's `credential` row in
 * "account", deletes every "session" row (all devices are signed out) and
 * prints the temporary password once. Reads DATABASE_URL like the web server.
 */
import { randomBytes } from 'node:crypto';

import pg from 'pg';

import { hashPassword } from './password-hash.mjs';

const email = process.argv[2]?.trim().toLowerCase();
if (!email) {
  console.error('usage: node apps/web/scripts/reset-password.mjs <email>');
  process.exit(2);
}
if (!process.env.DATABASE_URL) {
  console.error('DATABASE_URL is required');
  process.exit(2);
}

const pool = new pg.Pool({ connectionString: process.env.DATABASE_URL });
/** @type {pg.PoolClient | undefined} */
let client;
try {
  client = await pool.connect();
  const { rows } = await client.query('SELECT "id" FROM "user" WHERE lower("email") = $1', [email]);
  if (rows.length === 0) {
    console.error(`no user with email ${email}`);
    process.exitCode = 1;
  } else {
    await resetPassword(client, rows[0].id);
  }
} catch (err) {
  await client?.query('ROLLBACK').catch(() => {});
  console.error(err instanceof Error ? err.message : err);
  process.exitCode = 1;
} finally {
  // process.exitCode (never process.exit) so the client and pool always close.
  client?.release();
  await pool.end();
}

/**
 * @param {pg.PoolClient} client
 * @param {string} userId
 */
async function resetPassword(client, userId) {
  const password = randomBytes(12).toString('base64url');
  const hash = await hashPassword(password);

  await client.query('BEGIN');
  const updated = await client.query(
    'UPDATE "account" SET "password" = $1, "updatedAt" = now() WHERE "userId" = $2 AND "providerId" = $3',
    [hash, userId, 'credential']
  );
  if (updated.rowCount === 0) {
    await client.query(
      'INSERT INTO "account" ("id", "accountId", "providerId", "userId", "password", "createdAt", "updatedAt") VALUES ($1, $2, $3, $2, $4, now(), now())',
      [randomBytes(16).toString('hex'), userId, 'credential', hash]
    );
  }
  await client.query('DELETE FROM "session" WHERE "userId" = $1', [userId]);
  await client.query('COMMIT');

  console.log(`Temporary password for ${email}: ${password}`);
  console.log('All sessions were signed out. Ask the user to sign in and change it in Settings → Account.');
}
