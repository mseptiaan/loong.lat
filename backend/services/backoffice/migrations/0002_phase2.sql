-- Phase 2: the two-phase cancel, customers as users, and the wallet projection. The notifications table comes
-- from pkg/notices.

-- plan_version is the highest plan.version of console-api's events applied to the plan (plan.confirmed, cancel
-- requested/cancelled/aborted, rescheduled): an event not newer than it is a duplicate or a late arrival.
-- pending_action mirrors Plan.PendingAction ("cancel" while a cancel waits for the drivers' votes) so the
-- dispatch queries can leave such a plan alone.
ALTER TABLE plans ADD COLUMN plan_version   bigint NOT NULL DEFAULT 0;
ALTER TABLE plans ADD COLUMN pending_action text;

-- hold is the request id of the cancel that froze the route: dispatch neither assigns nor suggests it while set.
ALTER TABLE routes ADD COLUMN hold text;
CREATE INDEX routes_hold_idx ON routes (plan_id) WHERE hold IS NOT NULL;

-- Customers are the "users" of the admin console. email, phone and created_at follow customer.registered /
-- customer.updated. blocked is decided here; block_version numbers our own customer.blocked / customer.unblocked
-- events (its own family: console-api's customer versions are another counter).
ALTER TABLE customers ADD COLUMN email         text        NOT NULL DEFAULT '';
ALTER TABLE customers ADD COLUMN phone         text        NOT NULL DEFAULT '';
ALTER TABLE customers ADD COLUMN created_at    timestamptz NOT NULL DEFAULT now();
ALTER TABLE customers ADD COLUMN blocked       boolean     NOT NULL DEFAULT false;
ALTER TABLE customers ADD COLUMN block_version bigint      NOT NULL DEFAULT 0;
-- Customers projected before these columns existed have no email or phone. Version 0 lets the next customer event
-- about them (or a replay of the customer topic into a fresh consumer group) fill them in.
UPDATE customers SET version = 0;

-- Wallet projection (loong.wallet.v1, keyed by customer, numbered by console-api's per-wallet counter).
--   wallet_balances.version  is the highest event version whose balance was applied; an older event is dropped.
--   wallet_entries           the ledger: one row per wallet transaction (topup, payment, refund), written once.
--   topups                   every top-up in its latest state (version again keeps the newest).
CREATE TABLE wallet_balances (
    customer_id text   PRIMARY KEY,
    balance_rp  bigint NOT NULL,
    version     bigint NOT NULL
);

CREATE TABLE wallet_entries (
    id               text        PRIMARY KEY,
    customer_id      text        NOT NULL,
    at               timestamptz NOT NULL,
    kind             text        NOT NULL CHECK (kind IN ('topup', 'pembayaran', 'refund')),
    amount_rp        bigint      NOT NULL,
    balance_after_rp bigint      NOT NULL,
    plan_id          text,
    topup_id         text,
    topup_method     text,
    version          bigint      NOT NULL
);
CREATE INDEX wallet_entries_customer_idx ON wallet_entries (customer_id, at DESC, version DESC, id DESC);

CREATE TABLE topups (
    id          text        PRIMARY KEY,
    customer_id text        NOT NULL,
    amount_rp   bigint      NOT NULL,
    method      text        NOT NULL,
    status      text        NOT NULL,
    created_at  timestamptz NOT NULL,
    expires_at  timestamptz NOT NULL,
    paid_at     timestamptz,
    reference   text        NOT NULL,
    version     bigint      NOT NULL
);
CREATE INDEX topups_customer_idx ON topups (customer_id);
CREATE INDEX topups_created_idx ON topups (created_at DESC, id DESC);
