# 0009: Weather source: Open-Meteo

- Status: accepted
- Date: 2026-09-30
- Supersedes: the KNMI weather source in [0001](0001-scope-and-dataset.md) and [0003](0003-platform-engineering-focus.md)

## Context
ENTSO-E covers the European market, while KNMI only covers the Netherlands. The weather source has to cover the same bidding zones as the market data. The ingestion service is written in Go, and development time is limited.

## Options
1. Open-Meteo: REST/JSON, no API key, Europe-wide coverage. Forecast and historical (archive, based on ERA5) endpoints. Forecasts are recomputed on every model run, so stored values receive real UPDATEs. Free only for non-commercial use (about 10k calls per day), no SLA, and it is an aggregator, not the primary source.
2. Copernicus ERA5 via the CDS API: primary reanalysis source, with real revisions (preliminary ERA5T is replaced by final ERA5 after 2-3 months). Requests are asynchronous (submit, poll, download), data lags by about 5 days, output is gridded GRIB/NetCDF with weak Go library support, and an account and key are required.

## Decision
Option 1, Open-Meteo. The Go ingestion service calls the JSON API directly.

## Consequences
- No weather credentials: `KNMI_API_KEY` is removed from `.env.example`.
- Weather data is per coordinate, while ENTSO-E data is per bidding zone. The mapping from zone to representative points (and any aggregation) still has to be decided.
- The free tier's rate limit bounds backfill speed; backfills have to be batched and throttled.
- Open-Meteo data is licensed CC BY 4.0, so attribution is required wherever it is shown.
- If an SLA or the primary source is needed later, ERA5 can be added behind the same ingestion interface.
