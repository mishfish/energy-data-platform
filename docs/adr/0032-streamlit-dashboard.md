# 0032: Streamlit dashboard on ClickHouse

- Status: accepted
- Date: 2026-10-24

## Context
The platform loads prices, load, generation and weather for every bidding zone into ClickHouse (ADR 0024–0031), but nothing reads the analytical store yet besides tests. A small analytics front end shows that the data is usable and gives a data-freshness view that complements the run-freshness alerts in Grafana. The project uses Python only where it is clearly the better tool and only by decision; this is that decision: Streamlit is the quickest way to an interactive data app, and nothing comparable exists in Go.

## Options and decisions
| Topic | Options | Decision |
|---|---|---|
| Data source | ClickHouse vs Postgres | **ClickHouse**, always with `FINAL`. Analytical queries stay off the operational database, and the app exercises the whole CDC path. |
| Runtime | Compose service vs local `uv run` only | **Compose service** `dashboard` on :8501, Python 3.13 with dependencies from `uv.lock`, non-root, health from `/_stcore/health`. |
| ClickHouse client | `clickhouse-connect` vs `clickhouse-driver` | **`clickhouse-connect`** (official, HTTP, `query_df` to pandas). |
| Pages | — | Prices across Europe, Zone in detail, Weather and demand, Data health. |

## Design rules
- Charts are Altair (shipped with Streamlit, no extra dependency) and follow a written method: one axis per chart, thin marks, tooltips on every mark and a crosshair on lines, fixed categorical slots per entity, a table under every chart. The eight categorical colors are a validated palette (colorblind separation checked by script) with separate steps for light and dark; the app follows the viewer's theme.
- 21 production types fold into eight groups for stacked charts (`energy_dashboard/generation.py`); more series than slots are never given generated colors.
- Prices of zones with several sequences show the lowest one. Which sequence is the coupled day-ahead price stays a modelling decision for dbt (ADR 0029); the page says so.
- Data health measures how far the newest stored slot reaches relative to now, per dataset and zone. `source_created_at` cannot serve for that, since the upsert keeps the first document of the current value.

## Consequences
- A Python project lives under `analytics/dashboard` (uv, ruff, pytest); `make test-dashboard` runs it. Page smoke tests (Streamlit AppTest) run against ClickHouse when the stack is up and skip otherwise. CI does not run the Python tests yet.
- Zone names and production-type groups exist in Python too; tests compare them with the Go sources.
- On the first look the data-health page showed AL, BG and MK publishing load and generation 5 to 15 hours late: runs succeed, so `IngestStale` is quiet, which is what this page is for.
