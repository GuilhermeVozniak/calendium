# Backups & Restore

Self-hosting means **you own your backups**. Calendium's state lives in two
places, and both matter:

1. **Postgres** — all your mail mirror, calendars, events, drafts, snippets,
   accounts, and (encrypted) provider refresh tokens. Stored in the `db_data`
   named volume.
2. **`TOKEN_ENCRYPTION_KEY`** (in `.env`) — the AES-256-GCM key that decrypts
   those refresh tokens. **It is not in the database.** Lose it and every stored
   provider token becomes undecryptable, forcing every user to reconnect their
   accounts. Back it up *separately* and *permanently*, and keep it stable
   across deploys.

> A backup on the same disk as the server is not a backup. Always copy dumps
> **off-box** (S3 / R2 / B2 / another machine).

---

## Quick path: the `make` targets

The [`Makefile`](../../Makefile) wraps `pg_dump`/`psql` against the compose `db`
service for you:

```bash
make db-backup
# -> writes backups/calendium-YYYYMMDD-HHMMSS.sql.gz (gzip-compressed pg_dump)

make db-restore FILE=backups/calendium-20260706-020000.sql.gz
# -> pipes the dump back into psql on the db service
```

Under the hood these run inside the running container and read
`POSTGRES_USER` / `POSTGRES_DB` from the container's environment:

```bash
# what `make db-backup` runs:
docker compose exec -T db sh -c 'pg_dump -U "$POSTGRES_USER" "$POSTGRES_DB"' \
  | gzip > backups/calendium-$(date +%Y%m%d-%H%M%S).sql.gz

# what `make db-restore FILE=...` runs:
gunzip -c "$FILE" | docker compose exec -T db sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB"'
```

The `-T` disables TTY allocation so the pipe works from cron/scripts.

---

## Scheduling with cron

Run a nightly dump and prune anything older than 7 days. Adjust the repo path
(`/opt/calendium`) to wherever you deployed:

```cron
# /etc/cron.d/calendium-backup  — 02:00 daily, keep 7 days, then sync off-box
0 2 * * * deploy cd /opt/calendium && make db-backup \
  && find backups -name 'calendium-*.sql.gz' -mtime +7 -delete \
  && rclone copy backups/ remote:calendium-backups/
```

Swap the `rclone` line for `aws s3 sync`, `rsync`, or whatever moves the files
somewhere durable. Encrypt them first if the destination isn't trusted (see
[below](#encrypting-backups)).

Prefer a maintained sidecar? Drop
[`prodrigestivill/postgres-backup-local`](https://github.com/prodrigestivill/docker-postgres-backup-local)
into the compose file on the `calendium` network pointed at `db:5432`, with
`SCHEDULE=@daily`, `BACKUP_KEEP_DAYS=7`, `BACKUP_KEEP_WEEKS=4`,
`BACKUP_KEEP_MONTHS=6` — it handles rotation and compression for you.

---

## What to back up (full picture)

| Item | Where | How |
| --- | --- | --- |
| Database | `db_data` volume | `pg_dump` (preferred; see above) or volume snapshot |
| `TOKEN_ENCRYPTION_KEY` | `.env` | store in a password manager / secrets vault, offline |
| Rest of `.env` | `.env` | back up with the key; contains Supabase + provider secrets |
| TLS certs (optional) | `caddy_data` volume | not essential — Caddy re-issues; snapshot to avoid rate limits on frequent rebuilds |

A logical `pg_dump` (above) is the portable, restore-anywhere option and is what
you should rely on. If you also want a raw volume snapshot (e.g. for a fast
full-machine restore), stop the DB first for consistency:

```bash
docker compose stop db
docker run --rm -v calendium_db_data:/data -v "$PWD/backups:/backup" alpine \
  tar czf /backup/db_data-$(date +%F).tar.gz -C /data .
docker compose start db
```

(The volume is named `calendium_db_data` because the compose project name is
`calendium`; likewise `calendium_caddy_data`.)

---

## Encrypting backups

Dumps contain your users' mail. Encrypt before shipping off-box:

```bash
make db-backup
gpg --symmetric --cipher-algo AES256 backups/calendium-*.sql.gz   # prompts for a passphrase
# or age:
age -r <recipient-key> -o backup.sql.gz.age backups/calendium-*.sql.gz
```

Store the passphrase/recipient key separately from the backups.

---

## Managed Postgres (RDS / Cloud SQL / Azure)

If you swapped the bundled `db` service for a managed database, lean on the
provider's automated backups **and** keep taking periodic `pg_dump`s for
portability:

- **AWS RDS** — automated backups + PITR (7–35 days); take a manual snapshot
  before every upgrade.
- **GCP Cloud SQL** — automated backups + binary-log point-in-time recovery.
- **Azure Database for PostgreSQL Flexible Server** — automated backups up to
  35 days.

Still run `pg_dump` against the managed endpoint on a schedule — it guards
against provider lock-in and human error (a dropped table isn't fixed by
PITR if you notice it a week later). And always snapshot **before a
schema-changing upgrade** (see [Upgrading](./upgrades.md)).

---

## Restore drill (practice this before you need it)

A backup you've never restored is a hope, not a plan. Rehearse on a throwaway
stack:

```bash
# 1. Spin up a clean stack (or a scratch compose project) with the SAME .env,
#    especially the SAME TOKEN_ENCRYPTION_KEY.
docker compose up -d db

# 2. Restore the dump.
make db-restore FILE=backups/calendium-20260706-020000.sql.gz

# 3. Bring up the app and verify.
docker compose up -d
curl -fsS https://your-domain/healthz          # -> 200
# sign in, open a thread, confirm a connected account still syncs
#   (this is the real test that TOKEN_ENCRYPTION_KEY matched).
```

If connected accounts show as broken after a restore, your
`TOKEN_ENCRYPTION_KEY` didn't match the one in force when the dump was taken —
that's the whole reason it must be backed up alongside the database.

> **Migrations run at boot.** The API applies embedded SQL migrations when it
> starts (idempotently, tracked in `schema_migrations`). Restoring a dump that
> already contains those tables is fine — the migrator sees the versions as
> applied and skips them. See [Upgrading](./upgrades.md#how-migrations-run).

---

## Related pages

- [Upgrading](./upgrades.md) — always `make db-backup` before an upgrade
- [Security Hardening](./security.md) — protecting `TOKEN_ENCRYPTION_KEY` and encrypting backups
- [Configuration & Environment Reference](./configuration.md) — the `POSTGRES_*` / `DATABASE_URL` vars
- [Troubleshooting](./troubleshooting.md) — restore and DB-connection issues
