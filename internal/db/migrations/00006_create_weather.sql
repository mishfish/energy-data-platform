-- +goose Up
-- Hourly weather per representative point of a bidding zone from the Open-Meteo forecast API
-- (ADR 0009, 0031). Current state: past hours are analysis, future hours the latest forecast; every
-- new model run that changes a value is an UPDATE. A value the API returns as null is NULL.
CREATE TABLE weather (
    bidding_zone            text          NOT NULL, -- EIC code
    point                   text          NOT NULL, -- point name, see openmeteo.ZonePoints
    valid_from              timestamptz   NOT NULL, -- start of the hour, UTC
    temperature_2m_c        numeric(7, 2),
    wind_speed_100m_ms      numeric(7, 2),
    shortwave_radiation_wm2 numeric(7, 2),
    cloud_cover_pct         numeric(7, 2),
    source_created_at       timestamptz   NOT NULL, -- fetch time; the API has no document timestamp
    ingested_at             timestamptz   NOT NULL DEFAULT now(),
    updated_at              timestamptz   NOT NULL DEFAULT now(),
    PRIMARY KEY (bidding_zone, point, valid_from)
);

ALTER TABLE weather REPLICA IDENTITY FULL;
ALTER PUBLICATION energy_cdc ADD TABLE weather;

-- +goose Down
-- Removing the table from the publication breaks a running connector that still includes it.
ALTER PUBLICATION energy_cdc DROP TABLE weather;
DROP TABLE weather;
