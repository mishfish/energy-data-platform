-- +goose Up
-- Current state of ENTSO-E day-ahead prices (document type A44), one row per zone, slot and resolution.
-- Revisions overwrite rows in place, so CDC carries real UPDATEs (ADR 0012).
CREATE TABLE day_ahead_price (
    bidding_zone      text           NOT NULL, -- EIC code, e.g. 10YNL----------L
    delivery_start    timestamptz    NOT NULL, -- start of the market time unit, UTC
    resolution        interval       NOT NULL, -- market time unit: PT60M before the 15-minute MTU switch, PT15M after
    price_eur_mwh     numeric(10, 2) NOT NULL, -- can be negative
    source_created_at timestamptz    NOT NULL, -- createdDateTime of the ENTSO-E document that carried this value
    ingested_at       timestamptz    NOT NULL DEFAULT now(),
    updated_at        timestamptz    NOT NULL DEFAULT now(),
    PRIMARY KEY (bidding_zone, delivery_start, resolution),
    CONSTRAINT day_ahead_price_resolution_check
        CHECK (resolution IN (interval '15 minutes', interval '30 minutes', interval '60 minutes'))
);

-- +goose Down
DROP TABLE day_ahead_price;
