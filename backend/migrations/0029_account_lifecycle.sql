-- 0029_account_lifecycle.sql — production-readiness piece 3: account
-- deletion, data export, background-AI switch.
--
-- Cascade audit (docs/superpowers/specs/2026-10-04-account-lifecycle-design.md):
-- every table reachable from users(id) already cascades except
--   user_preferences (0010): user_id PRIMARY KEY with NO foreign key → orphans
--   teams (0011): created_by ... ON DELETE RESTRICT → deletion refused for
--                 anyone who created a surviving team.

-- user_preferences: drop orphans, then make the row follow its user.
DELETE FROM user_preferences WHERE user_id NOT IN (SELECT id FROM users);
ALTER TABLE user_preferences ADD CONSTRAINT user_preferences_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

-- teams: the team outlives its creator; created_by becomes NULL (scanned
-- back as '' by postgres/team.go).
ALTER TABLE teams ALTER COLUMN created_by DROP NOT NULL;
ALTER TABLE teams DROP CONSTRAINT teams_created_by_fkey;
ALTER TABLE teams ADD CONSTRAINT teams_created_by_fkey
    FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL;

-- Settings → AI → "Background AI processing". Default on; written only by
-- UserSettingsRepo.SetAIBackground so clients omitting the field keep it.
ALTER TABLE user_settings ADD COLUMN ai_background boolean NOT NULL DEFAULT true;

-- One data export per user per hour, claimed atomically (UserExportRepo.Claim).
CREATE TABLE user_exports (
    user_id    text PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    started_at timestamptz NOT NULL
);

-- Deleted-user tombstones. Purge inserts the id first in its transaction
-- (unconditionally, even with no users row) and then deletes the user; an
-- access token still valid after the purge answers 401 instead of
-- re-creating an empty account. Deliberately NOT FK'd to users (the row
-- outlives it). Holds only the opaque auth subject id.
CREATE TABLE deleted_users (
    id         text PRIMARY KEY,
    deleted_at timestamptz NOT NULL DEFAULT now()
);

-- The refusal must hold at insert time, not at statement-snapshot time: an
-- INSERT ... ON CONFLICT that started while the purge tx was open would
-- otherwise wait on the users row lock and re-insert after COMMIT. Both
-- tables' BEFORE INSERT triggers take the same per-id transaction advisory
-- lock, so a users insert for an id being purged waits for the purge to end
-- and then (READ COMMITTED: fresh snapshot per plpgsql statement) sees the
-- committed tombstone. SQLSTATE CU001 is mapped to domain.ErrUserDeleted
-- (401) by postgres/user.go.
CREATE FUNCTION calendium_user_lifecycle_lock() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(29, hashtext(NEW.id));
    RETURN NEW;
END
$$;

CREATE TRIGGER deleted_users_lifecycle_lock
    BEFORE INSERT ON deleted_users
    FOR EACH ROW EXECUTE FUNCTION calendium_user_lifecycle_lock();

CREATE FUNCTION calendium_refuse_deleted_user() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(29, hashtext(NEW.id));
    IF EXISTS (SELECT 1 FROM deleted_users WHERE id = NEW.id) THEN
        RAISE EXCEPTION 'calendium: account deleted' USING ERRCODE = 'CU001';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER users_refuse_deleted
    BEFORE INSERT ON users
    FOR EACH ROW EXECUTE FUNCTION calendium_refuse_deleted_user();

-- Export keyset pagers (ThreadRepo.ListByAccountPage, EventRepo
-- .ListByUserPage) order by id within an account/calendar; without these a
-- page re-sorts the scope's full set.
CREATE INDEX threads_account_id_id_idx ON threads (account_id, id);
CREATE INDEX events_calendar_id_id_idx ON events (calendar_id, id);
