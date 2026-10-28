-- Display codes are PL-<n>, counting from 2001.
CREATE SEQUENCE plan_code_seq START WITH 2001;

-- A plan is the wire snapshot (contracts.Plan) plus what the console tracks beside it. status and updated_at
-- repeat fields of the snapshot so they can be filtered and ordered; the plans package writes both together.
CREATE TABLE plans (
    id               text        PRIMARY KEY,
    code             text        NOT NULL UNIQUE,
    customer_id      text        NOT NULL REFERENCES customers (id),
    status           text        NOT NULL,
    -- Commercial version: rises with every commercial change (confirm today; cancel and reschedule later).
    version          bigint      NOT NULL DEFAULT 0,
    -- Hash of the draft input the stored option set was computed from; '' while there is no option set.
    draft_input_hash text        NOT NULL DEFAULT '',
    -- How the optimizer produced the option set (engine, road source, proof); NULL without one.
    optimizer        jsonb,
    -- Per route: the last applied assign_version (loong.dispatch.v1) and exec_version (loong.execution.v1).
    route_versions   jsonb       NOT NULL DEFAULT '{}',
    snapshot         jsonb       NOT NULL,
    created_at       timestamptz NOT NULL,
    updated_at       timestamptz NOT NULL
);
CREATE INDEX plans_customer_idx ON plans (customer_id, updated_at DESC);
