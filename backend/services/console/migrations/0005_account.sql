-- What the console knows about a customer beyond their credentials.
--   version        numbers the events the console publishes about the customer on loong.customer.v1
--                  (customer.registered is 1, every customer.updated adds one).
--   blocked        back office's decision (customer.blocked / customer.unblocked). A blocked customer cannot sign
--                  in or start and pay for plans. block_version and block_at say which of back office's events
--                  was applied last, so a late, older one cannot undo a newer.
ALTER TABLE customers
    ADD COLUMN version       bigint  NOT NULL DEFAULT 1,
    ADD COLUMN blocked       boolean NOT NULL DEFAULT false,
    ADD COLUMN block_version bigint  NOT NULL DEFAULT 0,
    ADD COLUMN block_at      timestamptz;

-- The shipper's address book. The console is its only owner: nothing else is told about it.
CREATE TABLE saved_addresses (
    seq           bigserial   PRIMARY KEY,
    id            text        NOT NULL UNIQUE,
    customer_id   text        NOT NULL REFERENCES customers (id),
    label         text        NOT NULL,
    place         jsonb       NOT NULL,
    contact_name  text        NOT NULL DEFAULT '',
    contact_phone text        NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL
);
CREATE INDEX saved_addresses_customer_idx ON saved_addresses (customer_id, seq);
