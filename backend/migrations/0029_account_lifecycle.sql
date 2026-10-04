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
