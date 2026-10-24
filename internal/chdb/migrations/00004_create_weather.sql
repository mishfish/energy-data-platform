-- +goose Up
-- Hourly weather per point (ADR 0031), same CDC table shape as the others (ADR 0024, 0026).
-- +goose StatementBegin
CREATE TABLE weather (
    bidding_zone            LowCardinality(String),
    point                   LowCardinality(String),
    valid_from              DateTime64(3, 'UTC'),
    temperature_2m_c        Nullable(Decimal(7, 2)),
    wind_speed_100m_ms      Nullable(Decimal(7, 2)),
    shortwave_radiation_wm2 Nullable(Decimal(7, 2)),
    cloud_cover_pct         Nullable(Decimal(7, 2)),
    source_created_at       DateTime64(3, 'UTC'),
    lsn                     UInt64,
    is_deleted              UInt8
)
ENGINE = ReplacingMergeTree(lsn, is_deleted)
PARTITION BY toYYYYMM(valid_from)
ORDER BY (bidding_zone, point, valid_from)
-- +goose StatementEnd

-- +goose Down
DROP TABLE weather;
