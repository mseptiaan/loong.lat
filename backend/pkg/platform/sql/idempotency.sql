-- Idempotency-Key bookkeeping: one row per (scope, key). status is NULL while the first request is running.
CREATE TABLE IF NOT EXISTS idempotency_keys (
    scope        text        NOT NULL,
    key          text        NOT NULL,
    fingerprint  bytea       NOT NULL,
    status       integer,
    content_type text,
    body         bytea,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    PRIMARY KEY (scope, key)
);
CREATE INDEX IF NOT EXISTS idempotency_keys_expires_idx ON idempotency_keys (expires_at);
