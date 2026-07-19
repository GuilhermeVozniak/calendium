-- 0023_integration_connections.sql — per-user vendor OAuth grants (M2.8 Task 9:
-- Todoist / HubSpot integrations). Tokens are AES-256-GCM sealed by the
-- postgres adapter (same vault as connected_accounts); one connection per
-- (user, vendor).

CREATE TABLE integration_connections (
    id                text PRIMARY KEY,
    user_id           text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    vendor            text NOT NULL,
    external_account  text NOT NULL DEFAULT '',
    status            text NOT NULL DEFAULT 'active', -- 'active' | 'error'
    last_error        text,
    access_token_enc  bytea,
    refresh_token_enc bytea,
    token_expires_at  timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, vendor)
);
-- Worker-side sync passes enumerate every connection of one vendor.
CREATE INDEX integration_connections_vendor_idx ON integration_connections (vendor);
