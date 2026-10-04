#!/usr/bin/env node
/**
 * Self-host password reset without email (docs/self-hosting/security.md).
 *
 *   docker compose exec web node apps/web/scripts/reset-password.mjs <email>
 *
 * Hashes a fresh temporary password with Better Auth's own hashPassword,
 * upserts the user's `credential` row in "account", deletes every "session"
 * row (all devices are signed out) and prints the temporary password once.
 * Reads DATABASE_URL like the web server does.
 */
import { randomBytes } from 'node:crypto';

import { hashPassword } from 'better-auth/crypto';
import pg from 'pg';

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
const client = await pool.connect();
try {
  const { rows } = await client.query('SELECT "id" FROM "user" WHERE lower("email") = $1', [email]);
  if (rows.length === 0) {
    console.error(`no user with email ${email}`);
    process.exit(1);
  }
  const userId = rows[0].id;
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
} catch (err) {
  await client.query('ROLLBACK').catch(() => {});
  console.error(err instanceof Error ? err.message : err);
  process.exit(1);
} finally {
  client.release();
  await pool.end();
}
