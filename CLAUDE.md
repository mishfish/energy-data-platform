# Energy Data Platform

A data platform focused on platform engineering: reliable ingestion, CDC, delivery guarantees, observability, failure handling and IaC. Data modeling is not the focus. Domain: European electricity market data from the ENTSO-E Transparency Platform (day-ahead prices, actual load, generation per production type, and forecasts), joined with Open-Meteo weather (ADR 0009). ENTSO-E revises published values after the fact, so the source produces real UPDATEs and not only appends; this is the main reason CDC is meaningful here.

> Status: repositioned on 2026-09-29 (platform focus, ENTSO-E domain), draft. Decisions still open are listed under **Open decisions**; when one is made, write an ADR and update this file. M0 scaffolding is in place (ADR 0007, 0008). `make test` and `make lint` pass. Current milestone: M1, ENTSO-E day-ahead prices for NL into Postgres (see `docs/roadmap.md`). Done: the A44 parser and the HTTP client with retries (`internal/entsoe`, ADR 0015), the batch upsert (`db.UpsertDayAheadPrices`, ADR 0016), and the ticker-driven job with metrics (`internal/ingest`, ADR 0017) wired into `cmd/ingest`. The freshness alerts (`observability/prometheus/rules/ingest.yml`, 45-minute threshold, unit-tested with promtool) and the Ingestion Grafana dashboard are in place. Alertmanager is now in the compose stack (ADR 0018) with a null receiver: alerts are grouped/deduplicated at `:9093` and visible in Grafana, but nothing is delivered externally yet. Live API run done on 2026-10-20 (token in `.env`): the main stack ingests real NL prices and load end to end into ClickHouse, matching Postgres exactly. M1 demo done on the synthetic stack. All bidding zones since 2026-10-24 (ADR 0029): one job per zone and dataset, `zone` metric label, price sequences in the key, six-decimal prices and load. Generation per production type (A75) for 47 zones in `generation` with a `psr_type` reference table (ADR 0030). Hourly Open-Meteo weather for 47 zones, 3–5 points each, in `weather` (ADR 0031). M1.5 is complete. M1.5 in progress: realised total load for NL (A65/A16) is parsed, stored in `actual_load` and ingested by the generic `ingest.Job[T]` (ADR 0019, 0020). Without a token the whole stack runs against a synthetic ENTSO-E API (`cmd/fakeentsoe`, `make up-fake`, ADR 0021) in a separate compose project. M2a in progress: Debezium 3.7 on Kafka Connect streams `day_ahead_price` and `actual_load` into `energy.public.*` topics (JSON converter, publication `energy_cdc` from migration 00003, slot `debezium_energy`, ADR 0022); M2b: postgres_exporter, `max_slot_wal_keep_size=2GB`, CDC alerts (`rules/cdc.yml`, promtool-tested), CDC dashboard and runbook `docs/runbooks/cdc-connector-stopped.md` (ADR 0023). M3 infra in place (ADR 0024–0027): ClickHouse 26.8 LTS with `ReplacingMergeTree(lsn, is_deleted)` tables applied by `sink-migrate` (goose, `internal/chdb`), Apicurio Registry 3.3 on its own Postgres database, Apicurio converters enabled in Connect. The connector emits Avro (fake stack switched over with a re-snapshot). The sink (`cmd/sink`, `internal/sink`, ADR 0028; written by Claude at the owner's request) consumes with confluent-kafka-go, decodes with the avrov2 serde and writes ClickHouse with commit-after-write; ClickHouse `FINAL` matches Postgres on the fake stack. The sink integration test (testcontainers: Kafka, Apicurio, ClickHouse) covers the ADR 0026 failure scenarios. Not done: DLQ, Grafana panels for the sink.

## Working rules for Claude
- **The owner writes the core logic**: CDC handling, ingestion-service internals, delivery guarantees (retries, idempotency, offset/LSN handling, at-least-once vs exactly-once). For this code, review it, point out problems and suggest approaches. **Do not write the implementation unless explicitly asked.**
- Claude may write boilerplate: Terraform, CI config, docker-compose, Makefiles, Grafana dashboards, Prometheus/alert rules, test scaffolding.
- **Before any architectural choice**, present 2 options with trade-offs and let the owner decide. Never pick silently, including "small" choices such as a library, a serialization format or a runtime version.
- Record every decision as a short ADR in `docs/adr/NNNN-kebab-title.md` with sections Context / Options / Decision / Consequences. Superseded ADRs are marked as such, not deleted.
- Go is the primary language. Python is used only where it is clearly the better tool (dbt, model training) and only by decision.

## Language
- Code, comments, commit messages, docs: **English**.

## Architecture (target)
```
ENTSO-E API (prices, load, gen) ──┐
Open-Meteo API (weather) ─────────┴─► Go ingestion service ─► Postgres (source of truth, logical replication)
                                                                  │  CDC: Debezium on Kafka Connect
                                                                  ▼
                                                           Kafka (KRaft)
                                                                  │  Go sink (idempotent)
                                                                  ▼
                                                     ClickHouse ─► dbt models ─► price/load forecast API
Observability: Prometheus + Grafana + Alertmanager across all components
```
`A | B` = open decision. Cross-cutting concerns, each documented in an ADR or runbook:
- **Freshness and lag**: ingestion freshness per source, replication lag and slot WAL retention, consumer lag, end-to-end latency.
- **Failure handling**: retries with backoff, idempotent writes at every hop, backfill and re-snapshot, poison messages / DLQ, schema evolution from source to sink.
- **Delivery semantics**: at-least-once vs exactly-once, with trade-offs documented and failure scenarios demonstrated.

## Stack
- **Go** (1.27 installed locally; the version is pinned in `go.mod`): services and tooling. Format with gofmt/goimports, lint with golangci-lint.
- **Postgres** with `wal_level=logical` as the operational store. Access via `pgx/v5` + `pgxpool` directly (ADR 0011); schema migrations with goose, SQL only, embedded in the binary (`internal/db/migrations`, ADR 0010), applied by the one-shot `ingest migrate` compose service before `ingest` starts (ADR 0013). Prices live in a current-state table (ADR 0012), written with a single `unnest` batch upsert (ADR 0016). In Go, prices are `int64` micro-EUR and load `int64` watts (ADR 0029, superseding 0014).
- **Kafka** (KRaft) as the broker (ADR 0005). **Debezium** on Kafka Connect for CDC (ADR 0004, 0006).
- **Analytical store**: ClickHouse, `ReplacingMergeTree` versioned by LSN (ADR 0024); events in Avro via Apicurio Registry (ADR 0025); at-least-once delivery with an idempotent sink (ADR 0026).
- **Observability**: Prometheus, Grafana (dashboards as code), Alertmanager.
- **Infra**: docker compose for the local stack (compose file at repo root), Terraform, GitHub Actions; Kubernetes optional.
- **Python (secondary only)**: 3.13 + uv + ruff + pytest. Never `pip install`. A PostToolUse hook runs ruff on edited `.py` files.

## Repo layout (planned)
```
cmd/<binary>/            # main packages (ingest, sink, ...)
internal/                # non-exported Go packages
deploy/terraform/        # IaC
deploy/k8s/              # optional
observability/           # prometheus rules, alertmanager, grafana dashboards
analytics/               # dbt + model (secondary)
docs/adr/                # decisions
docs/runbooks/           # failure scenarios and how to recover
docs/roadmap.md          # milestones
```

## Commands
```bash
make build        # go build ./...
make test         # go test -race ./...
make test-int     # go test -race -tags=integration ./...   (testcontainers-go)
make test-rules   # promtool test rules (Prometheus alert rule unit tests, needs Docker)
make lint         # golangci-lint run
make up / down    # docker compose up -d / docker compose down  (never -v without asking)
make up-fake / down-fake   # same stack against the synthetic ENTSO-E API, separate compose project
make fault f='{"status":503}' / fault-clear   # inject or clear a fake API failure
make migrate      # apply schema migrations (one-shot compose service)
docker compose run --rm sink-migrate   # apply ClickHouse migrations
# make test/lint need cgo (gcc): the sink uses confluent-kafka-go
docker compose ps / logs -f <svc>
```

## Conventions
- Go layout: binaries in `cmd/`, packages in `internal/`. No `pkg/` unless something is meant to be imported externally.
- `context.Context` is the first parameter on anything that does I/O. Services shut down gracefully on SIGTERM (drain, commit offsets/LSN, then exit).
- Logging uses `log/slog` (JSON). Metrics use `prometheus/client_golang` on `/metrics` and follow Prometheus naming (`_total`, `_seconds`). Every service exposes `/healthz` and `/readyz`.
- Wrap errors with `%w`. No panics outside `main`.
- Unit tests are table-driven. Integration tests run against real Postgres and the real broker via testcontainers-go, behind the `integration` build tag.
- Config comes from environment variables. Document every variable in `.env.example`. Never commit `.env`.
- Keep functions small and avoid speculative abstractions.
- Every bug fix or feature gets a test. Run `make test` and `make lint` before declaring work done.
- Commit messages follow Conventional Commits (`feat:`, `fix:`, `chore:`, `docs:`, `test:`, `refactor:`) and stay small and focused.

## Guardrails
- Ask before: `docker compose down -v`, dropping DBs/topics/datasets/buckets, **dropping replication slots or publications**, **resetting consumer-group offsets**, deleting data dirs, and any `terraform apply`/`destroy` against real cloud. (Git commit rules live in ~/.claude/CLAUDE.md.)
- Don't modify `go.sum` or `uv.lock` by hand.

## Open decisions
Each needs an ADR once decided.
1. ~~CDC implementation~~: decided, Debezium on Kafka Connect (ADR 0004, 0006).
2. ~~Broker~~: decided, Kafka in KRaft mode (ADR 0005).
3. ~~Analytical store~~: decided, ClickHouse (ADR 0024).
4. ~~Event serialization and schema registry~~: decided, Avro with Apicurio Registry (ADR 0025).
5. ~~Delivery semantics target~~: decided, at-least-once with an idempotent sink (ADR 0026).
6. Cloud and runtime: GCP vs AWS, and a VM with compose vs Kubernetes.
7. Serving the model: Go vs FastAPI.
8. Batch orchestration (backfill, dbt runs): scheduled Go jobs / k8s CronJob vs Airflow.
9. ~~Weather source~~: decided, Open-Meteo (ADR 0009).
10. ~~Weather-to-market mapping~~: decided, every zone with data (ADR 0029) and 3–5 raw points per zone, weighted in dbt (ADR 0031).
