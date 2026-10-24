# 0031: Hourly weather per zone point from Open-Meteo

- Status: accepted
- Date: 2026-10-24

## Context
ADR 0009 chose Open-Meteo as the weather source. Open decision 10 left open how weather maps onto bidding zones once the zones were chosen (ADR 0029). The forecast API returns, per location, hourly values for past days (analysis) and the coming days (forecast) in one request of up to several locations; values snap to the model grid, have at most two decimals, and need no API key. A probe of all 47 zones with 3–5 points each (2026-10-24) returned 37,800 rows without a null.

## Options and decisions
| Topic | Options | Decision |
|---|---|---|
| Points per zone | 3–5 raw points vs one point per zone | **3–5 points** per zone (`internal/openmeteo/points.go`): load centres, wind and solar regions, offshore points where relevant. Stored raw; weighting into a zone value is a dbt concern and can change without reloading. |
| Source | Forecast API with `past_days` vs the ERA5 archive | **Forecast API**: 7 days back and 2 days ahead in one request. Past values settle and change, which yields real UPDATEs like ENTSO-E; the forecast is input for the price model. ERA5 lags about five days. |
| Resolution | Hourly vs 15 minutes | **Hourly**, native for all of Europe; market data joins on the hour in dbt. |
| Variables | Energy-relevant vs a wider set | `temperature_2m` (demand), `wind_speed_100m` (wind at hub height, m/s), `shortwave_radiation` (solar), `cloud_cover`. Adding one later is a schema change through CDC. |
| Numbers | Fixed-point decimal strings vs float64 | **Fixed-point**: JSON numbers are read as strings and parsed by `internal/fixedpoint` into hundredths, stored as `numeric(7, 2)`, NULL where the API returns null. One convention for all sources (ADR 0014, 0029). |
| Table shape | Wide (a column per variable) vs long (variable, value) | **Wide**: `weather` keyed by (bidding_zone, point, valid_from). |
| Forecast vintages | Current state vs every model run | **Current state**: each new run that changes an hour is an UPDATE. Earlier runs survive only in the CDC topic for its retention; a backtest that needs "what was known when" requires keeping them elsewhere. |

## Consequences
- 47 more jobs, hourly; one request per zone covers its points. About 1,100 requests a day, well under Open-Meteo's free limit of 10,000.
- The weather API has no document timestamp; `source_created_at` is the fetch time, which keeps the stale-write guard working.
- `IngestStale` now derives its threshold from the job interval (`ingest_job_interval_seconds`, three missed runs): 45 minutes for the 15-minute market jobs, 3 hours for hourly weather. First runs of all jobs are spread over the shortest interval, so the hourly jobs also start within 15 minutes and `IngestNeverSucceeded` stays quiet on restarts.
- The sink carries a list of columns per table instead of one amount field; weather values are `Nullable(Decimal(7, 2))` in ClickHouse.
- The fake stack (ADR 0021) fetches real weather: Open-Meteo needs no token, and `OPEN_METEO_BASE_URL` can point elsewhere.
- Open decision 10 is closed.
