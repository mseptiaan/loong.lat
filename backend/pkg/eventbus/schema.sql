-- Transactional outbox: a service writes its events here in the same transaction as the data change, and the
-- relay publishes them to Kafka afterwards. seq is the publish order; rows of one aggregate are written by
-- transactions that serialize on that aggregate, so seq order is event order per key.
CREATE TABLE IF NOT EXISTS outbox (
    seq          bigserial   PRIMARY KEY,
    event_id     uuid        NOT NULL UNIQUE,
    topic        text        NOT NULL,
    key          text        NOT NULL,
    event_type   text        NOT NULL,
    traceparent  text,
    payload      text        NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz,
    attempts     integer     NOT NULL DEFAULT 0,
    last_error   text
);
CREATE INDEX IF NOT EXISTS outbox_unpublished_idx ON outbox (seq) WHERE published_at IS NULL;

-- Idempotent inbox: a consumer records each event id it has applied, in the transaction that applies it, so
-- a redelivered event is recognised and skipped. consumer is the consumer group.
CREATE TABLE IF NOT EXISTS inbox (
    consumer    text        NOT NULL,
    event_id    uuid        NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, event_id)
);
