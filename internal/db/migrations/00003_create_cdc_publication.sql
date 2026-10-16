-- +goose Up
-- CDC source for Debezium (ADR 0022). The publication lists the streamed tables explicitly; Debezium
-- runs with publication.autocreate.mode=disabled, so a table enters CDC only through a migration.
-- REPLICA IDENTITY FULL puts the whole old row into UPDATE events, so a revision carries both values.
ALTER TABLE day_ahead_price REPLICA IDENTITY FULL;
ALTER TABLE actual_load REPLICA IDENTITY FULL;
CREATE PUBLICATION energy_cdc FOR TABLE day_ahead_price, actual_load;

-- +goose Down
-- Dropping the publication breaks a running connector; stop it first.
DROP PUBLICATION energy_cdc;
ALTER TABLE actual_load REPLICA IDENTITY DEFAULT;
ALTER TABLE day_ahead_price REPLICA IDENTITY DEFAULT;
