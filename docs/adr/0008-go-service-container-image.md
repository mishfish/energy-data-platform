# 0008: Go service container image

- Status: accepted
- Date: 2026-09-29

## Context
The Go services (starting with `ingest`) run in the local compose stack next to Postgres, Kafka and Prometheus, so Prometheus scrapes them by service name and compose healthchecks cover them. They need a runtime image. The build stage is `golang:1.27` with `CGO_ENABLED=0` in either case.

## Options
1. `gcr.io/distroless/static-debian12:nonroot`: about 2 MB, no shell or package manager, nonroot by default. Without curl/wget, the compose healthcheck needs a `healthcheck` subcommand in every binary, and debugging needs an ephemeral container.
2. `alpine`: about 8 MB, with a shell and busybox `wget`. The healthcheck is a plain HTTP probe and `docker exec sh` works for debugging. The attack surface is larger, packages need updates, and the nonroot user is created by hand.

## Decision
Option 2, `alpine:3.24`. One shared multi-stage `Dockerfile` at the repo root builds any service via `--build-arg SERVICE=<name>` (`./cmd/<name>`). The compose healthcheck probes `/readyz`.

## Consequences
- Debugging during failure-scenario demos is simpler (shell, wget, `/proc` via busybox tools).
- The image has to follow Alpine updates. A move to distroless later needs a healthcheck subcommand in each service.
- `make up` rebuilds service images (`docker compose up -d --build`).
