-- +goose Up
-- Realised generation per production type (A75, ADR 0030), current state per zone, production type,
-- direction and slot, with the same revision semantics as day_ahead_price and actual_load (ADR 0012).

-- Production types of the Transparency Platform. Kept equal to entsoe.PSRTypes by an integration test.
CREATE TABLE psr_type (
    code text PRIMARY KEY CHECK (code ~ '^B[0-9]{2}$'),
    name text NOT NULL
);
INSERT INTO psr_type (code, name) VALUES
    ('B01', 'Biomass'),
    ('B02', 'Fossil Brown coal/Lignite'),
    ('B03', 'Fossil Coal-derived gas'),
    ('B04', 'Fossil Gas'),
    ('B05', 'Fossil Hard coal'),
    ('B06', 'Fossil Oil'),
    ('B07', 'Fossil Oil shale'),
    ('B08', 'Fossil Peat'),
    ('B09', 'Geothermal'),
    ('B10', 'Hydro Pumped Storage'),
    ('B11', 'Hydro Run-of-river and poundage'),
    ('B12', 'Hydro Water Reservoir'),
    ('B13', 'Marine'),
    ('B14', 'Nuclear'),
    ('B15', 'Other renewable'),
    ('B16', 'Solar'),
    ('B17', 'Waste'),
    ('B18', 'Wind Offshore'),
    ('B19', 'Wind Onshore'),
    ('B20', 'Other'),
    ('B21', 'AC Link'),
    ('B22', 'DC Link'),
    ('B23', 'Substation'),
    ('B24', 'Transformer'),
    ('B25', 'Energy storage');

CREATE TABLE generation (
    bidding_zone      text           NOT NULL, -- EIC code
    psr_type          text           NOT NULL REFERENCES psr_type (code),
    direction         text           NOT NULL, -- generation (inBiddingZone) or consumption (outBiddingZone)
    delivery_start    timestamptz    NOT NULL,
    resolution        interval       NOT NULL,
    quantity_mw       numeric(15, 6) NOT NULL, -- average power over the market time unit (ADR 0029)
    source_created_at timestamptz    NOT NULL,
    ingested_at       timestamptz    NOT NULL DEFAULT now(),
    updated_at        timestamptz    NOT NULL DEFAULT now(),
    PRIMARY KEY (bidding_zone, psr_type, direction, delivery_start, resolution),
    CONSTRAINT generation_direction_check CHECK (direction IN ('generation', 'consumption')),
    CONSTRAINT generation_resolution_check
        CHECK (resolution IN (interval '15 minutes', interval '30 minutes', interval '60 minutes')),
    CONSTRAINT generation_quantity_check CHECK (quantity_mw >= 0)
);

-- CDC like the other tables (ADR 0022); psr_type is static reference data and is not streamed.
ALTER TABLE generation REPLICA IDENTITY FULL;
ALTER PUBLICATION energy_cdc ADD TABLE generation;

-- +goose Down
-- Removing the table from the publication breaks a running connector that still includes it.
ALTER PUBLICATION energy_cdc DROP TABLE generation;
DROP TABLE generation;
DROP TABLE psr_type;
