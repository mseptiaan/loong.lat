-- The two-phase cancel of a confirmed plan, coordinated here. One row per attempt; a plan has at most one attempt
-- in flight (the plan's pendingAction says the same). The votes of driver-api arrive as events, are counted per
-- route and decide the attempt: all accepted commits (plan.cancelled), one rejected or the deadline aborts it.
CREATE TABLE plan_cancel_requests (
    request_id   text        PRIMARY KEY,
    plan_id      text        NOT NULL REFERENCES plans (id),
    -- 'customer' or 'admin' (contracts.CancelBy*).
    requested_by text        NOT NULL CHECK (requested_by IN ('customer', 'admin')),
    -- The admin's name for an attempt that came from back office.
    requester    text        NOT NULL DEFAULT '',
    -- What the requester wrote; empty when they gave no reason.
    reason       text        NOT NULL DEFAULT '',
    -- The requester's language ('en' or 'id'): the default reason of the plan is written in it.
    locale       text        NOT NULL DEFAULT 'en',
    -- Every route of the plan: each must accept.
    route_ids    text[]      NOT NULL,
    status       text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'committed', 'aborted')),
    requested_at timestamptz NOT NULL,
    -- After this a pending attempt is aborted by the sweeper: someone did not answer.
    deadline     timestamptz NOT NULL,
    finished_at  timestamptz
);
CREATE UNIQUE INDEX plan_cancel_requests_pending_key ON plan_cancel_requests (plan_id) WHERE status = 'pending';
CREATE INDEX plan_cancel_requests_deadline_idx ON plan_cancel_requests (deadline) WHERE status = 'pending';

-- One vote per (attempt, route): a redelivered vote lands on the key and counts once.
CREATE TABLE plan_cancel_votes (
    request_id text    NOT NULL REFERENCES plan_cancel_requests (request_id),
    route_id   text    NOT NULL,
    accepted   boolean NOT NULL,
    PRIMARY KEY (request_id, route_id)
);
