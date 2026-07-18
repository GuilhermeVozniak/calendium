-- 0009_compose_contact.sql — M2.5: signatures, auto-BCC, reactions,
-- attachment quick access, recent-opens feed.

-- Per-account rich signature + auto-BCC, following the vip_senders
-- precedent (per-account scalar prefs live on connected_accounts).
ALTER TABLE connected_accounts ADD COLUMN signature_html text NOT NULL DEFAULT '';
ALTER TABLE connected_accounts ADD COLUMN auto_bcc text[] NOT NULL DEFAULT '{}';

-- Provider-native attachment id so bodies can be fetched on demand
-- (Gmail body.attachmentId / Graph attachment id). Backfilled empty;
-- sync ingest populates it for new/updated messages.
ALTER TABLE attachments ADD COLUMN provider_attachment_id text NOT NULL DEFAULT '';

-- Attachment quick access: filename substring search.
CREATE INDEX attachments_filename_trgm_idx ON attachments USING gin (filename gin_trgm_ops);

-- Recent Opens feed: keyset scan of opened sent mail per account.
CREATE INDEX messages_opened_feed_idx ON messages (account_id, opened_at DESC, id DESC)
    WHERE opened_at IS NOT NULL;

-- Emoji reactions: stored locally; optionally also delivered as a tiny reply.
CREATE TABLE message_reactions (
    id         text PRIMARY KEY,
    message_id text NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    user_id    text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    emoji      text NOT NULL,
    delivery   text NOT NULL DEFAULT 'local', -- 'local' | 'sent'
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (message_id, user_id, emoji)
);
CREATE INDEX message_reactions_message_idx ON message_reactions (message_id);
