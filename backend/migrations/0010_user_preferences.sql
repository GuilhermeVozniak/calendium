-- M2.6 Task 13: named themes — per-user cross-device preference document.
-- Distinct from user_prefs (inbox split layout, /v1/prefs): this table backs
-- GET/PUT /v1/me/preferences.
CREATE TABLE user_preferences (
    user_id text PRIMARY KEY,
    theme text NOT NULL DEFAULT 'neutral',
    updated_at timestamptz NOT NULL DEFAULT now()
);
