-- +goose Up
-- Current state of the CDC tables (ADR 0024, 0026). Every change event is inserted as a row;
-- ReplacingMergeTree keeps the row with the highest LSN per key, and drops the key when that row
-- has is_deleted = 1. Exact reads before background merges need FINAL.
-- +goose StatementBegin
CREATE TABLE day_ahead_price (
    bidding_zone      LowCardinality(String),
    delivery_start    DateTime64(3, 'UTC'),
    resolution_us     Int64,           -- market time unit in microseconds, as Debezium sends intervals (ADR 0022)
    price_eur_mwh     Decimal(10, 2),
    source_created_at DateTime64(3, 'UTC'),
    lsn               UInt64,          -- version: LSN of the change in Postgres
    is_deleted        UInt8
)
ENGINE = ReplacingMergeTree(lsn, is_deleted)
PARTITION BY toYYYYMM(delivery_start)
ORDER BY (bidding_zone, delivery_start, resolution_us)
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE actual_load (
    bidding_zone      LowCardinality(String),
    delivery_start    DateTime64(3, 'UTC'),
    resolution_us     Int64,
    load_mw           Decimal(12, 3),
    source_created_at DateTime64(3, 'UTC'),
    lsn               UInt64,
    is_deleted        UInt8
)
ENGINE = ReplacingMergeTree(lsn, is_deleted)
PARTITION BY toYYYYMM(delivery_start)
ORDER BY (bidding_zone, delivery_start, resolution_us)
-- +goose StatementEnd

-- +goose Down
DROP TABLE actual_load;
DROP TABLE day_ahead_price;
