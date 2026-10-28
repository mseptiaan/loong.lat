-- backoffice-api's own tables. Sessions, outbox and inbox come from pkg/platform and pkg/eventbus.

-- Admin accounts. The password is stored only as an argon2id hash; the email is unique ignoring case.
CREATE TABLE admins (
    id            text        PRIMARY KEY,
    email         text        NOT NULL,
    name          text        NOT NULL,
    password_hash text        NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX admins_email_key ON admins (lower(email));

-- Fleet master data. version counts the changes announced on the compacted fleet topic.
CREATE TABLE vehicles (
    id               text             PRIMARY KEY,
    type             text             NOT NULL CHECK (type IN ('motor', 'blindvan')),
    plate            text             NOT NULL,
    model            text             NOT NULL,
    max_weight_kg    double precision NOT NULL,
    max_volume_m3    double precision NOT NULL,
    max_dimension_cm double precision NOT NULL,
    fare             jsonb            NOT NULL,
    status           text             NOT NULL CHECK (status IN ('aktif', 'servis', 'nonaktif')),
    version          bigint           NOT NULL DEFAULT 1,
    CONSTRAINT vehicles_plate_key UNIQUE (plate)
);

-- Driver master data plus the state driver-api reports. The PIN is never stored here: only its hash leaves,
-- in driver.provisioned / driver.updated.
--   version          counts master-data changes (our own driver.provisioned / driver.updated).
--   presence_version is driver-api's counter for driver.online / driver.offline; older events are dropped.
--   lat, lng         the latest known fix, recorded at position_at (older fixes are dropped).
CREATE TABLE drivers (
    id               text             PRIMARY KEY,
    name             text             NOT NULL,
    phone            text             NOT NULL,
    vehicle_id       text,
    rating           double precision NOT NULL DEFAULT 5,
    online           boolean          NOT NULL DEFAULT false,
    lat              double precision,
    lng              double precision,
    position_at      timestamptz,
    last_seen        timestamptz,
    joined_at        timestamptz      NOT NULL,
    version          bigint           NOT NULL DEFAULT 1,
    presence_version bigint           NOT NULL DEFAULT 0,
    CONSTRAINT drivers_phone_key UNIQUE (phone),
    CONSTRAINT drivers_position_pair CHECK ((lat IS NULL) = (lng IS NULL))
);
CREATE UNIQUE INDEX drivers_vehicle_key ON drivers (vehicle_id) WHERE vehicle_id IS NOT NULL;

-- Settings groups, one row each. The rows exist from the start so an update always has one to lock; version
-- counts changes (the settings topic is compacted).
CREATE TABLE settings (
    key        text        PRIMARY KEY,
    value      jsonb       NOT NULL,
    version    bigint      NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO settings (key, value) VALUES
    ('ai',       '{"vision": true, "voice": true, "intent": true, "copilot": true}'),
    ('dispatch', '{"auto": true}');

-- Customer names from customer.registered / customer.updated, for plan rows and route.assigned.
CREATE TABLE customers (
    id      text   PRIMARY KEY,
    name    text   NOT NULL,
    version bigint NOT NULL
);

-- Confirmed plans, as received in plan.confirmed and kept current by dispatch and execution events. `plan`
-- holds the contracts.Plan without its routes, which live in `routes`; the columns repeat what queries filter on.
CREATE TABLE plans (
    id            text        PRIMARY KEY,
    code          text        NOT NULL,
    user_id       text        NOT NULL,
    status        text        NOT NULL,
    schedule_mode text        NOT NULL,
    plan          jsonb       NOT NULL,
    created_at    timestamptz NOT NULL,
    updated_at    timestamptz NOT NULL
);
CREATE INDEX plans_status_idx ON plans (status, updated_at DESC);
CREATE INDEX plans_user_idx ON plans (user_id);

-- Routes of those plans with their assignment. The two versions order the two event families about a route:
--   assign_version is ours: it counts route.assigned / route.unassigned.
--   exec_version   is driver-api's counter for the execution events (started, arrived, completed, ...).
CREATE TABLE routes (
    id             text        PRIMARY KEY,
    plan_id        text        NOT NULL REFERENCES plans (id) ON DELETE CASCADE,
    seq            integer     NOT NULL,
    vehicle_type   text        NOT NULL,
    status         text        NOT NULL,
    driver_id      text,
    vehicle_id     text,
    start_at       timestamptz NOT NULL,
    route          jsonb       NOT NULL,
    assign_version bigint      NOT NULL DEFAULT 0,
    exec_version   bigint      NOT NULL DEFAULT 0,
    UNIQUE (plan_id, seq)
);
CREATE INDEX routes_waiting_idx ON routes (start_at) WHERE status = 'menunggu_driver';
CREATE INDEX routes_active_idx ON routes (driver_id) WHERE status IN ('ditugaskan', 'berjalan');
