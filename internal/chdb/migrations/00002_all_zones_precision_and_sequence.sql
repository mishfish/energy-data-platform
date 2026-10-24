-- +goose Up
-- All bidding zones (ADR 0029): price sequences per zone and day, six decimals for prices and load.
-- ClickHouse only allows a new column at the end of the sorting key, and only in the same ALTER that
-- adds it; the order differs from the Postgres primary key, the set of columns is the same.
-- Events written before Postgres had the column carry no sequence and land as 0, which is what
-- Postgres backfilled.
-- +goose StatementBegin
ALTER TABLE day_ahead_price
    ADD COLUMN sequence UInt8 AFTER bidding_zone, -- implicit default 0; an explicit DEFAULT is not allowed in the sorting key
    MODIFY ORDER BY (bidding_zone, delivery_start, resolution_us, sequence)
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE day_ahead_price MODIFY COLUMN price_eur_mwh Decimal(16, 6)
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE actual_load MODIFY COLUMN load_mw Decimal(15, 6)
-- +goose StatementEnd

-- +goose Down
-- The sequence column cannot leave the sorting key again; only the scales are reverted.
-- +goose StatementBegin
ALTER TABLE actual_load MODIFY COLUMN load_mw Decimal(12, 3)
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE day_ahead_price MODIFY COLUMN price_eur_mwh Decimal(10, 2)
-- +goose StatementEnd
