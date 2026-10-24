# 0033: Blackbox probe for the Streamlit dashboard

- Status: accepted
- Date: 2026-10-24

## Context
Every service in the stack is on the platform overview in Grafana through Prometheus' `up` metric, except the Streamlit dashboard (ADR 0032): Streamlit has no `/metrics`. Its state should be visible next to the other services and alert when it is down.

## Options and decisions
| Topic | Options | Decision |
|---|---|---|
| Metrics source | blackbox_exporter probing `/_stcore/health` vs a `prometheus_client` endpoint inside the app | **blackbox_exporter** (`prom/blackbox-exporter:v0.29.1`), module `http_2xx`: no change to the app, and the same probe later covers other HTTP services without metrics (Apicurio, Connect, Grafana). It yields `probe_success` and `probe_duration_seconds`, not page-level metrics. |
| Alert | `DashboardDown` as a warning vs a panel only | **Warning** after 5 minutes. The dashboard is not on the data path, so it is not critical. |

## Consequences
- Prometheus job `dashboard` scrapes the exporter with the probe target as `instance`. `probe_success` is the app's state; `up{job="dashboard"}` only says the exporter answered, so the alert fires on either `probe_success == 0` or `up == 0`. Rule tests in `observability/prometheus/tests/dashboard_test.yml`.
- The platform overview shows the dashboard's state (with a text label, not color alone) and probe duration.
- CI runs the dashboard's ruff and unit tests in a `dashboard` job (`make test-dashboard`, uv installs exactly `uv.lock`); the page smoke tests skip there for lack of ClickHouse.
