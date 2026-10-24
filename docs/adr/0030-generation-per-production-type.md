# 0030: Generation per production type (A75)

- Status: accepted
- Date: 2026-10-24

## Context
M1.5 adds realised generation per production type (document type A75, process type A16) for every bidding zone. Responses from the live API for all 49 zones (2026-10-23): 47 zones publish it, GB and UA_IPS do not. A75 uses the same `GL_MarketDocument` as A65, with a production type per time series (`MktPSRType/psrType`, codes B01–B25; B01–B20 and B25 seen) and a direction: `inBiddingZone_Domain` for what a type feeds into the zone, `outBiddingZone_Domain` for what it takes out, mostly pumped storage (B10). Quantities have up to six decimals and are never negative. One day of all zones is about 39,000 values, 4,800 of them consumption.

## Options and decisions
| Topic | Options | Decision |
|---|---|---|
| Generation and consumption | One table with `direction` in the key vs generation only | **One table** `generation`, primary key (bidding_zone, psr_type, direction, delivery_start, resolution), `quantity_mw numeric(15, 6)`. Nothing is dropped; the pumped-storage net is a dbt concern. |
| Window | 7 days every 15 minutes like load vs 2 days | **7 days every 15 minutes** (`INGEST_GENERATION_*`). About 310,000 rows per pass across all zones, nearly all no-ops in the upsert. |
| Production type | Code with a reference table vs the name in every row | **Code** (`B04`), with the names in a `psr_type` reference table. The parser rejects unknown codes; Postgres enforces them with a foreign key. |

## Consequences
- `entsoe.PSRTypes` is the source of the code list; the Postgres and ClickHouse `psr_type` tables are created from it by migrations, and integration tests keep all three equal. `psr_type` is not streamed through CDC.
- A65 and A75 now share the GL decoder and the period expander (`decodeGL`, `expandQuantityPeriod`), as planned when A65 got its own parser.
- 47 more jobs: 138 in total, about 138 requests per 15 minutes against a limit of 400 per minute. Generation responses for a 7-day window are a few hundred kilobytes per zone.
- Rollout order matters for CDC: Debezium does not snapshot a table added to `table.include.list` of a running connector, and it drops changes of tables outside the list. The connector config therefore has to include `generation` before ingest writes to it: migration (table and publication), then connector config, then ingest.
- The synthetic API serves A75 for every zone (a fixed subset of production types per zone, pumped storage with consumption), so the fake stack keeps working.
