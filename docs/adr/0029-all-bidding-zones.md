# 0029: All bidding zones

- Status: accepted
- Date: 2026-10-24
- Supersedes: ADR 0014 (prices as integer cents) and the scale part of ADR 0019 (load in kW)

## Context
Ingestion covered NL only. Extending it to every bidding zone answers the first half of open decision 10 (which zones to ingest). Responses for 49 candidate zones from the live API (2026-10-23) broke three assumptions of the NL-only design:
- Load comes with up to six decimals (watt precision) in AL, BG, CH, DE_LU, DK_1/2, LT, ME, MK, SI and XK; ADR 0019 allowed three.
- Prices come with more than two decimals: DK_1 sends float32 artefacts (`173.869995`), IE_SEM three decimals (`173.745`); ADR 0014 allowed two.
- AT, DE_LU, DK_1 and ES publish several price sequences for the same zone and day, numbered by `classificationSequence_AttributeInstanceComponent.position`. They look like different auctions (one ES sequence has 44 slots, partly negative); which one is the coupled day-ahead price is not stated in the document.

No data at all for GB; no prices for BA, CY and UA_IPS (UAH, not EUR); no load for IE_SEM and UA_IPS.

## Options and decisions
| Topic | Options | Decision |
|---|---|---|
| Load scale | Watts (MW × 10⁶) vs rounding to kW | **Watts**: `QuantityW int64`, `numeric(15, 6)`. Stored exactly as published; the no-op guard stays exact. |
| Price scale | Six decimals as published vs rounding to cents | **Six decimals**: `PriceMicros int64` (millionths of EUR/MWh), `numeric(16, 6)`. Rounding would lose IE_SEM's real third decimal; DK_1 artefacts are cleaned downstream (dbt) if at all. |
| Price sequences | Keep all, sequence in the key vs keep position 1 only | **Keep all**: `sequence` in the primary key (0 when the document has a single sequence). Choosing the coupled price is a modelling decision for dbt, not a guess in the parser. |
| Zone selection | A zone list per dataset vs every zone always with a currency column | **A list per dataset**: `INGEST_DAY_AHEAD_ZONES` (45 zones) and `INGEST_ACTUAL_LOAD_ZONES` (46) default to the zones with EUR data in the probe; names resolve to EIC codes from `internal/entsoe/zones.go`. |
| Jobs | One job per zone and dataset vs one job per dataset looping over zones | **Per zone and dataset**, ~90 jobs. A slow or failing zone does not delay the others. First runs are spread over the interval (job *i* of *N* starts after *i*·interval/*N*). |
| Metrics and alerts | `zone` label with an expected-jobs gauge vs per-dataset aggregates | **`zone` label** on runs, rows and last success, plus `ingest_job_info{dataset,zone}`. `IngestStale` fires per zone; `IngestNeverSucceeded` is `job_info unless last_success`, so no rule lists datasets or zones; `IngestDown` (`up == 0`) covers a process that is not scraped. The duration histogram keeps only `dataset` and `result`. |

## Consequences
- Migrations: Postgres `00004` (sequence in the primary key, wider numerics), ClickHouse `00002` (sequence appended to the sorting key, wider decimals). With `decimal.handling.mode=string` the wider numerics do not change the Avro types; the new `sequence` field and the new key field are additions with a default, which BACKWARD compatibility accepts.
- Kafka keys of `day_ahead_price` change: events before and after the migration have different key schemas. The sink keys on the row content, not on the Kafka key, so this does not affect ClickHouse.
- Deploy order: ClickHouse migration and sink before the Postgres migration, so no event with a sequence reaches a sink that would drop it. Locally compose starts both together; the sink only sees the new field after Postgres has it.
- About 90 requests per 15 minutes against a limit of 400 per minute.
- The weather half of open decision 10 (coordinates per zone) stays open.
