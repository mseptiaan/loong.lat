-- Drivers: master data projected from loong.driver.v1 (provisioned/updated) next to what this service owns:
-- presence, the latest position and the trip count.
CREATE TABLE drivers (
    id               text             PRIMARY KEY,
    name             text             NOT NULL,
    -- digits only, 0 instead of 62 (drivers.NormalizePhone): the login key
    phone            text             NOT NULL UNIQUE,
    pin_hash         text             NOT NULL,
    vehicle_id       text,
    status           text             NOT NULL DEFAULT 'offline' CHECK (status IN ('online', 'offline', 'bertugas')),
    lat              double precision,
    lng              double precision,
    -- when the stored position was recorded: a fix is only taken if it is newer
    position_at      timestamptz,
    last_seen        timestamptz,
    joined_at        timestamptz      NOT NULL,
    completed_trips  integer          NOT NULL DEFAULT 0,
    -- driver.version of the master data last applied
    version          bigint           NOT NULL,
    -- counts driver.online/offline, the AggregateVersion of this service's own presence events
    presence_version bigint           NOT NULL DEFAULT 0,
    CHECK ((lat IS NULL) = (lng IS NULL))
);
CREATE INDEX drivers_vehicle_idx ON drivers (vehicle_id);

-- Vehicles projected from loong.fleet.v1; a removed vehicle stays as a row without a snapshot so that a late
-- upsert of an older version cannot bring it back.
CREATE TABLE vehicles (
    id      text   PRIMARY KEY,
    vehicle jsonb,
    version bigint NOT NULL
);

-- The tasks: routes dispatch gave to a driver, which this service then executes. route is the contracts.Route
-- snapshot including the server-side delivery codes (never sent to the app). The columns after it are the
-- summary the history and earnings read without parsing the snapshot.
CREATE TABLE routes (
    id             text             PRIMARY KEY,
    plan_id        text             NOT NULL,
    plan_code      text             NOT NULL,
    customer_name  text             NOT NULL,
    -- NULL once dispatch took the route back; the row stays to remember assign_version
    driver_id      text             REFERENCES drivers (id),
    status         text             NOT NULL CHECK (status IN ('menunggu_driver', 'ditugaskan', 'berjalan', 'selesai')),
    route          jsonb            NOT NULL,
    -- routes.assign_version of dispatch: assigned/unassigned older than this are dropped
    assign_version bigint           NOT NULL,
    -- one per execution event published for the route
    exec_version   bigint           NOT NULL DEFAULT 0,
    cost_rp        bigint           NOT NULL,
    distance_km    double precision NOT NULL,
    duration_min   double precision NOT NULL,
    stop_count     integer          NOT NULL,
    finished_at    timestamptz,
    updated_at     timestamptz      NOT NULL
);
CREATE INDEX routes_driver_idx ON routes (driver_id, status) WHERE driver_id IS NOT NULL;
