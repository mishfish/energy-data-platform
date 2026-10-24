-- +goose Up
-- Generation per production type (ADR 0030), same CDC table shape as the others (ADR 0024, 0026).
-- +goose StatementBegin
CREATE TABLE generation (
    bidding_zone      LowCardinality(String),
    psr_type          LowCardinality(String),
    direction         LowCardinality(String), -- generation or consumption
    delivery_start    DateTime64(3, 'UTC'),
    resolution_us     Int64,
    quantity_mw       Decimal(15, 6),
    source_created_at DateTime64(3, 'UTC'),
    lsn               UInt64,
    is_deleted        UInt8
)
ENGINE = ReplacingMergeTree(lsn, is_deleted)
PARTITION BY toYYYYMM(delivery_start)
ORDER BY (bidding_zone, psr_type, direction, delivery_start, resolution_us)
-- +goose StatementEnd

-- Static production type names for queries and dbt; kept equal to entsoe.PSRTypes by a test.
-- +goose StatementBegin
CREATE TABLE psr_type (
    code String,
    name String
)
ENGINE = MergeTree
ORDER BY code
-- +goose StatementEnd

-- +goose StatementBegin
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
    ('B25', 'Energy storage')
-- +goose StatementEnd

-- +goose Down
DROP TABLE psr_type;
DROP TABLE generation;
