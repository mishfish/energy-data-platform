# 0001: Scope and dataset

- Status: superseded by [0003](0003-platform-engineering-focus.md) (the ENTSO-E dataset choice still stands); the weather source is superseded by [0009](0009-weather-source-open-meteo.md)
- Date: 2026-09-29

## Context
The project needs a public data source that updates regularly and has enough structure for a real dimensional model. A follow-up MLOps project will consume this platform's output.

## Decision
- Sources: ENTSO-E Transparency Platform (European day-ahead prices, load, generation per production type) and KNMI weather observations.
- Batch ingestion. Change Data Capture was considered and dropped from the core scope: both sources are APIs, not OLTP databases, so CDC would have to be simulated.
- The focus is Data Engineering: ingestion, dbt star schema, data quality, orchestration, IaC, CI/CD. ML is limited to a batch forecast written back to the warehouse.
- The marts are treated as a data product with dbt contracts, so the MLOps project can depend on them.

## Consequences
- Energy data is operationally relevant (TSOs, utilities, trading), and the data has real-world quirks such as DST gaps and changes in market time unit.
- ENTSO-E API access requires a manually issued token, which may take a few days.
- CDC stays a possible stretch goal.
