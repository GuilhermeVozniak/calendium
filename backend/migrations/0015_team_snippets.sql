-- 0015_team_snippets.sql — Team-scoped snippets (M2.7 Task 11).
-- Extends the existing snippet model with an optional team scope — no new
-- resource. team_id NULL keeps the row a personal snippet (M2.1 behavior
-- unchanged); deleting a team cascades its snippets away.
ALTER TABLE snippets ADD COLUMN team_id text REFERENCES teams(id) ON DELETE CASCADE;
CREATE INDEX snippets_team_idx ON snippets (team_id) WHERE team_id IS NOT NULL;
