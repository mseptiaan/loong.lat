-- Phase 2 (docs/phase2.md): two-phase plan cancel, delay detection and the settings projection.

-- A route held by a plan cancel in flight cannot be started; the hold is the id of the cancel request. A route
-- the cancel committed is closed as cancelled: it stays as a row (a late route.assigned must not bring it back)
-- but is no task any more. delay_stop/delay_step remember the last route.delayed published, so that a sweep
-- after a restart does not publish it again: the stop the delay was measured at and its step (5-minute units).
ALTER TABLE routes
    ADD COLUMN hold         text,
    ADD COLUMN cancelled_at timestamptz,
    ADD COLUMN delay_stop   text,
    ADD COLUMN delay_step   integer NOT NULL DEFAULT 0;
CREATE INDEX routes_hold_idx ON routes (hold) WHERE hold IS NOT NULL;
CREATE INDEX routes_running_idx ON routes (id) WHERE status = 'berjalan';

-- A vote for a route this service has not heard of yet (its route.assigned is still on the way): the vote was
-- "accepted", so the assignment is ignored when it arrives. They are deleted when the request is aborted and
-- kept when it commits.
CREATE TABLE route_tombstones (
    route_id   text        NOT NULL,
    request_id text        NOT NULL,
    plan_id    text        NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (route_id, request_id)
);
CREATE INDEX route_tombstones_request_idx ON route_tombstones (request_id);

-- What dispatch said about a tombstoned route while the tombstone stood (the newest event only): ignored for now,
-- but if the cancel is aborted the assignment must not be lost, because dispatch will not send it again.
-- assigned is the route.assigned payload, or NULL when the newest event was an unassignment. A commit deletes it.
CREATE TABLE parked_assignments (
    route_id    text        PRIMARY KEY,
    plan_id     text        NOT NULL,
    version     bigint      NOT NULL,
    event_id    text        NOT NULL,
    occurred_at timestamptz NOT NULL,
    assigned    jsonb
);
CREATE INDEX parked_assignments_plan_idx ON parked_assignments (plan_id);

-- The settings of loong.settings.v1, the latest version of each group. Nothing here until back office has
-- announced one: readers fall back to the defaults.
CREATE TABLE settings (
    key        text        PRIMARY KEY,
    value      jsonb       NOT NULL,
    version    bigint      NOT NULL,
    updated_at timestamptz NOT NULL
);
