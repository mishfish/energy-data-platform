# 0021: Synthetic ENTSO-E API for local runs and failure demos

- Status: accepted
- Date: 2026-10-15

## Context
The ingestion path (ADR 0015–0017, 0019, 0020) is unit- and integration-tested against fixtures, but the stack cannot run end to end without an ENTSO-E API token. Even with a token, the real API does not give reproducible revisions or failures, and the CDC and failure-handling demos later in the roadmap depend on both. Static fixtures do not help here: they carry fixed delivery dates, while the ingest window moves with the current day.

## Options and decisions
| Topic | Options | Decision |
|---|---|---|
| Implementation | Go service in `cmd/fakeentsoe` vs WireMock with templated responses | **Go service.** Documents are generated for any requested window; templating 96–100 points per day for arbitrary dates is impractical in WireMock, and WireMock would add a JVM image. Built with the shared Dockerfile (ADR 0008), no new dependency. |
| Failure injection | Runtime admin endpoint vs environment variables at startup | **Admin endpoint** `PUT/GET/DELETE /admin/fault` (`status`, `delay`, `retry_after`), so a failure can be switched on and off during a demo without restarting anything. |
| Compose wiring | Profile `fake` started explicitly vs fake as the default upstream | **Explicit**: `make up-fake`. Plain `make up` always talks to the real API. |
| Data isolation | Separate compose project vs the same project | **Separate project** `energy-data-platform-fake` with its own volumes, so synthetic rows never reach the real Postgres or, later, the real Kafka topics. |

## Behaviour
- A44 for any zone with `in_Domain = out_Domain`: whole CET/CEST delivery days overlapping the window, one time series per day, curve type A03. A delivery day is published at 13:00 local time the day before.
- A65 with `processType=A16`: slots of the window that ended at least one hour ago, curve type A01. A window reaching into today returns a partial day.
- An empty result is a 999 acknowledgement with HTTP 200, as the real API sometimes answers. Unsupported requests get a plain 400, missing token a 401.
- Values are deterministic per zone and slot. Time is split into revision epochs (`FAKE_ENTSOE_REVISION_INTERVAL`, default 1 h); in each epoch a published slot younger than seven days is revised with probability `FAKE_ENTSOE_REVISION_RATE` (default 0.02). Every request at the same time sees the same values, and a revision stays until the next one.
- `createdDateTime` is the request time, as on the real API.

## Consequences
- The M1 demo and the coming CDC demos run without a token and with controllable revisions and failures.
- Compared with the live API on 2026-10-20: the A44 shape matches; real A65 uses curve type A03 (the fake sends A01) and publishes load about 35 minutes after a slot ends (the fake assumes one hour). Neither difference affects ingest.
- The fake encodes our understanding of the API. A contract test drives the real `entsoe.Client` against it, but it cannot catch a difference between the fake and the real API; the first live responses must be compared with it and with the fixtures.
- The fake stack shares host ports with the main one, so only one of them runs at a time.
- Every new document type that ingest fetches needs a generator here as well.
