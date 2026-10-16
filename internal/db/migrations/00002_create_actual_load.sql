-- +goose Up
-- Current state of ENTSO-E realised total load (document type A65, process type A16), one row per
-- zone, slot and resolution. Same layout and revision semantics as day_ahead_price (ADR 0012, 0020).
CREATE TABLE actual_load (
    bidding_zone      text           NOT NULL, -- EIC code, e.g. 10YNL----------L
    delivery_start    timestamptz    NOT NULL, -- start of the market time unit, UTC
    resolution        interval       NOT NULL, -- market time unit
    load_mw           numeric(12, 3) NOT NULL, -- average load over the market time unit (ADR 0019)
    source_created_at timestamptz    NOT NULL, -- createdDateTime of the ENTSO-E document that carried this value
    ingested_at       timestamptz    NOT NULL DEFAULT now(),
    updated_at        timestamptz    NOT NULL DEFAULT now(),
    PRIMARY KEY (bidding_zone, delivery_start, resolution),
    CONSTRAINT actual_load_resolution_check
        CHECK (resolution IN (interval '15 minutes', interval '30 minutes', interval '60 minutes')),
    CONSTRAINT actual_load_load_mw_check CHECK (load_mw >= 0)
);

-- +goose Down
DROP TABLE actual_load;
