-- The work list of the AI check of delivery photos: one row per drop-off that was proved with a photo. The row is
-- written in the transaction that stores the proof (so a stop that is done with a photo always has its job) and
-- worked off by the proofs worker, which calls the model outside any transaction. A redelivered stop.completed
-- lands on the key and adds nothing.
CREATE TABLE proof_jobs (
    plan_id    text        NOT NULL REFERENCES plans (id),
    route_id   text        NOT NULL,
    stop_id    text        NOT NULL,
    -- pending: waiting or being worked on; done: the analysis is stored; skipped: nothing to do (the stop or its
    -- photo is gone, it has an analysis already, or the admin switched the vision feature off); failed: the photo
    -- cannot be analysed, or the model gave up after the last attempt.
    state      text        NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'done', 'skipped', 'failed')),
    -- Counted when the job is claimed, so an attempt that crashed the worker still counts.
    attempts   integer     NOT NULL DEFAULT 0,
    -- When a pending job is due. Claiming moves it forward by the lease: a worker that died holding the job lets it
    -- come back when the lease runs out; a failed attempt moves it forward by the backoff.
    next_at    timestamptz NOT NULL DEFAULT now(),
    -- Why the last attempt did not work. Never holds the photo.
    last_error text        NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (plan_id, stop_id)
);
CREATE INDEX proof_jobs_due_idx ON proof_jobs (next_at) WHERE state = 'pending';
