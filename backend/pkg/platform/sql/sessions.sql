-- Opaque session tokens, stored only as SHA-256 hashes.
CREATE TABLE IF NOT EXISTS sessions (
    token_hash bytea       PRIMARY KEY,
    role       text        NOT NULL,
    user_id    text        NOT NULL,
    name       text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_user_idx ON sessions (user_id);
CREATE INDEX IF NOT EXISTS sessions_expires_idx ON sessions (expires_at);
