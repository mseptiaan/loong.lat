-- The bell menu of one audience. dedupe_key makes a handler idempotent: the event that causes a notification
-- names it, and a redelivery finds the row there already.
CREATE TABLE IF NOT EXISTS notifications (
    id         text        PRIMARY KEY,
    audience   text        NOT NULL,
    at         timestamptz NOT NULL,
    title      jsonb       NOT NULL,
    body       jsonb       NOT NULL,
    plan_id    text,
    read_at    timestamptz,
    dedupe_key text
);
CREATE INDEX IF NOT EXISTS notifications_audience_idx ON notifications (audience, at DESC, id DESC);
CREATE UNIQUE INDEX IF NOT EXISTS notifications_dedupe_idx ON notifications (audience, dedupe_key) WHERE dedupe_key IS NOT NULL;
