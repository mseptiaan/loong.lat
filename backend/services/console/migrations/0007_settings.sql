-- Projection of loong.settings.v1 (compacted, one record per settings key: "ai", "dispatch"). A key that never
-- arrived has no row and reads as its default.
CREATE TABLE settings (
    key        text        PRIMARY KEY,
    value      jsonb       NOT NULL,
    version    bigint      NOT NULL,
    updated_at timestamptz NOT NULL
);
