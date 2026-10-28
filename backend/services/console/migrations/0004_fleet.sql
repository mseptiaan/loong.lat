-- Projection of loong.fleet.v1: the vehicles back office offers, which quotes are priced with. A removed
-- vehicle stays as a tombstone so a late, older upsert cannot bring it back.
CREATE TABLE fleet_vehicles (
    id         text        PRIMARY KEY,
    vehicle    jsonb       NOT NULL,
    status     text        NOT NULL,
    removed    boolean     NOT NULL DEFAULT false,
    version    bigint      NOT NULL,
    updated_at timestamptz NOT NULL
);
