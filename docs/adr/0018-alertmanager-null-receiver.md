# 0018: Alertmanager with a null receiver

- Status: accepted
- Date: 2026-10-10

## Context
The architecture (CLAUDE.md) always called for Prometheus + Grafana + Alertmanager, but only Prometheus and Grafana were wired into the compose stack through M1. Alerts (ADR for `IngestStale` / `IngestNeverSucceeded`, `observability/prometheus/rules/ingest.yml`) were visible only in the Prometheus UI, with no grouping, deduplication, or dedicated alert view.

## Options and decisions
| Topic | Options | Decision |
|---|---|---|
| Alertmanager image | `prom/alertmanager` (official) vs none (keep alerts Prometheus-only) | **`prom/alertmanager:v0.28.1`**, added to `docker-compose.yml` |
| Default receiver | `null` receiver (groups/dedupes, no delivery) vs a real webhook (e.g. webhook.site) as a stand-in | **`null` receiver** — consistent with local-first dev (ADR 0002/0003): no external service or secret to configure before a real notification target (email/Slack/PagerDuty) is chosen |

## Consequences
- Prometheus now has an `alerting.alertmanagers` block pointing at `alertmanager:9093`; firing alerts are visible, grouped, and deduplicated at `:9093` and in Grafana (added as an `alertmanager`-type datasource) rather than only in the Prometheus UI.
- No one is actually notified yet. Choosing and wiring a real receiver is a separate decision, deferred until there's an actual on-call target.
- New volume `alertmanager-data` and env var `ALERTMANAGER_PORT` (default `9093`), documented in `.env.example`.
