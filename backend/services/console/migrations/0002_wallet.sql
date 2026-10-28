-- One wallet per customer. balance_rp only ever changes together with a wallet_entries row (see the wallet
-- package), so it always equals the sum of the customer's entries.
CREATE TABLE wallets (
    customer_id text   PRIMARY KEY REFERENCES customers (id),
    balance_rp  bigint NOT NULL DEFAULT 0 CHECK (balance_rp >= 0),
    -- Counts the events published on the wallet topic for this customer: their aggregateVersion.
    version     bigint NOT NULL DEFAULT 0
);

-- The ledger. kind is the frontend's WalletTxKind. reference is the top-up id for a top-up and the plan id
-- for a payment or refund, so (kind, reference) is unique: a retry can neither charge a plan twice nor credit
-- one top-up twice.
CREATE TABLE wallet_entries (
    seq              bigserial   PRIMARY KEY,
    id               text        NOT NULL UNIQUE,
    customer_id      text        NOT NULL REFERENCES customers (id),
    at               timestamptz NOT NULL,
    kind             text        NOT NULL CHECK (kind IN ('topup', 'pembayaran', 'refund')),
    delta_rp         bigint      NOT NULL CHECK (delta_rp <> 0),
    balance_after_rp bigint      NOT NULL CHECK (balance_after_rp >= 0),
    reference        text        NOT NULL,
    title            jsonb       NOT NULL,
    UNIQUE (kind, reference),
    CHECK ((kind = 'pembayaran') = (delta_rp < 0))
);
CREATE INDEX wallet_entries_customer_idx ON wallet_entries (customer_id, seq DESC);

CREATE TABLE top_ups (
    id          text        PRIMARY KEY,
    customer_id text        NOT NULL REFERENCES customers (id),
    amount_rp   bigint      NOT NULL CHECK (amount_rp > 0),
    method      text        NOT NULL,
    status      text        NOT NULL CHECK (status IN ('menunggu', 'berhasil', 'kedaluwarsa', 'dibatalkan')),
    reference   text        NOT NULL,
    created_at  timestamptz NOT NULL,
    expires_at  timestamptz NOT NULL,
    paid_at     timestamptz
);
CREATE INDEX top_ups_customer_idx ON top_ups (customer_id, created_at DESC);
