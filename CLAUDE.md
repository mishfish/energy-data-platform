# Energy Data Platform

A data platform focused on platform engineering: reliable ingestion, CDC, delivery guarantees, observability, failure handling and IaC. Data modeling is not the focus. Domain: European electricity market data from the ENTSO-E Transparency Platform (day-ahead prices, actual load, generation per production type, and forecasts), joined with Open-Meteo weather (ADR 0009). ENTSO-E revises published values after the fact, so the source produces real UPDATEs and not only appends; this is the main reason CDC is meaningful here.

> Status: repositioned on 2026-09-29 (platform focus, ENTSO-E domain), draft. Decisions still open are listed under **Open decisions**; when one is made, write an ADR and update this file. M0 scaffolding is in place (ADR 0007, 0008). `make test` and `make lint` pass. Current milestone: M1, ENTSO-E day-ahead prices for NL into Postgres (see `docs/roadmap.md`). Done: the A44 parser and the HTTP client with retries (`internal/entsoe`, ADR 0015), the batch upsert (`db.UpsertDayAheadPrices`, ADR 0016), and the ticker-driven job with metrics (`internal/ingest`, ADR 0017) wired into `cmd/ingest`. The freshness alerts (`observability/prometheus/rules/ingest.yml`, 45-minute threshold, unit-tested with promtool) and the Ingestion Grafana dashboard are in place. Alertmanager is now in the compose stack (ADR 0018) with a null receiver: alerts are grouped/deduplicated at `:9093` and visible in Grafana, but nothing is delivered externally yet. Not done: a run against the live API (blocked on an ENTSO-E API token) and the M1 demo.

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
                                                     BigQuery  |  ClickHouse ─► dbt models ─► price/load forecast API
Observability: Prometheus + Grafana + Alertmanager across all components
```
`A | B` = open decision. Cross-cutting concerns, each documented in an ADR or runbook:
- **Freshness and lag**: ingestion freshness per source, replication lag and slot WAL retention, consumer lag, end-to-end latency.
- **Failure handling**: retries with backoff, idempotent writes at every hop, backfill and re-snapshot, poison messages / DLQ, schema evolution from source to sink.
- **Delivery semantics**: at-least-once vs exactly-once, with trade-offs documented and failure scenarios demonstrated.

## Stack
- **Go** (1.27 installed locally; the version is pinned in `go.mod`): services and tooling. Format with gofmt/goimports, lint with golangci-lint.
- **Postgres** with `wal_level=logical` as the operational store. Access via `pgx/v5` + `pgxpool` directly (ADR 0011); schema migrations with goose, SQL only, embedded in the binary (`internal/db/migrations`, ADR 0010), applied by the one-shot `ingest migrate` compose service before `ingest` starts (ADR 0013). Prices live in a current-state table (ADR 0012), written with a single `unnest` batch upsert (ADR 0016). In Go, prices are `int64` cents (ADR 0014).
- **Kafka** (KRaft) as the broker (ADR 0005). **Debezium** on Kafka Connect for CDC (ADR 0004, 0006).
- **Analytical store**: see Open decisions.
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
make migrate      # apply schema migrations (one-shot compose service)
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
3. Analytical store: BigQuery vs ClickHouse.
4. Event serialization and schema registry: Avro vs Protobuf (JSON Schema as a fallback).
5. Delivery semantics target: at-least-once + idempotent sink vs exactly-once (Kafka transactions / sink-side commits).
6. Cloud and runtime: GCP vs AWS, and a VM with compose vs Kubernetes.
7. Serving the model: Go vs FastAPI.
8. Batch orchestration (backfill, dbt runs): scheduled Go jobs / k8s CronJob vs Airflow.
9. ~~Weather source~~: decided, Open-Meteo (ADR 0009).
10. Weather-to-market mapping: which bidding zones to ingest, and representative coordinates per zone (single point vs weighted average of several).
