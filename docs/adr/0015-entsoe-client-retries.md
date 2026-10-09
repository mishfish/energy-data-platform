# 0015: ENTSO-E client retries with a hand-written backoff

- Status: accepted
- Date: 2026-10-10

## Context
The ingestion service calls the ENTSO-E Transparency Platform API on a schedule. The API fails transiently (5xx, timeouts, connection resets) and rate-limits per token: exceeding the limit is answered with 429 and can end in a temporary ban. It also answers "no data yet" with an acknowledgement document, sometimes with HTTP 200 and sometimes with 4xx, which is an expected outcome and not a failure. Retrying inside one fetch smooths over short outages; anything longer is covered by the next scheduled run, since fetches are idempotent (ADR 0012).

## Options
1. Hand-written retry loop in the client: capped exponential backoff with full jitter, a few dozen lines, stops on context cancellation. No dependency, and the classification of what is retried sits next to the response handling it depends on.
2. `cenkalti/backoff`: ready-made jitter and max-elapsed-time handling. A new direct dependency, while the part that actually needs thought (which responses are transient) still has to be written by hand.

## Decision
Option 1. The policy lives in `internal/entsoe/client.go`:
- Up to 5 attempts per fetch, 30 s timeout per request.
- Delay before attempt `n+1` is uniform in `[0, min(30 s, 500 ms * 2^(n-1)))`, raised to `Retry-After` when the server sends one.
- Retried: transport errors, per-request timeouts, 429, 5xx.
- Not retried: acknowledgement documents (including "no data"), other 4xx, unparsable 200 bodies, and a `Retry-After` longer than 30 s.

## Consequences
- A fetch blocks for at most about 4.5 minutes in the worst case (5 request timeouts of 30 s plus 4 waits of up to 30 s when the server sends `Retry-After`; about 2.6 minutes without it); the scheduler interval must stay above that or skip overlapping runs.
- A long `Retry-After` (a rate-limit ban) ends the fetch immediately with the 429 error, so the scheduler, not the client, decides when to try again. The freshness alert covers a ban that lasts.
- The API token travels as a query parameter, so the client strips the URL from transport errors before returning them.
- There is no circuit breaker and no client-side rate limiter. One zone and one document type stay far below the limit; revisit when M1.5 adds sources and backfills.
