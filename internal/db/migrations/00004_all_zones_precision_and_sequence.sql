-- +goose Up
-- All bidding zones (ADR 0029). Prices keep the precision TSOs publish (up to six decimals; DK_1
-- sends float32 artefacts), load is stored in MW with six decimals, and some zones publish several
-- day-ahead price sequences (auctions) per day, told apart by their classificationSequence position.
-- Debezium picks the new key and value schemas up with the next change; the registry checks them
-- against BACKWARD compatibility (ADR 0025).
ALTER TABLE day_ahead_price ADD COLUMN sequence smallint NOT NULL DEFAULT 0;
ALTER TABLE day_ahead_price DROP CONSTRAINT day_ahead_price_pkey;
ALTER TABLE day_ahead_price ADD PRIMARY KEY (bidding_zone, sequence, delivery_start, resolution);
ALTER TABLE day_ahead_price ALTER COLUMN price_eur_mwh TYPE numeric(16, 6);
ALTER TABLE actual_load ALTER COLUMN load_mw TYPE numeric(15, 6);

-- +goose Down
-- Fails while a zone has more than one sequence, or values with more decimals than the old scale.
ALTER TABLE actual_load ALTER COLUMN load_mw TYPE numeric(12, 3);
ALTER TABLE day_ahead_price ALTER COLUMN price_eur_mwh TYPE numeric(10, 2);
ALTER TABLE day_ahead_price DROP CONSTRAINT day_ahead_price_pkey;
ALTER TABLE day_ahead_price ADD PRIMARY KEY (bidding_zone, delivery_start, resolution);
ALTER TABLE day_ahead_price DROP COLUMN sequence;
