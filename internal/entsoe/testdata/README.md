# ENTSO-E test fixtures

Synthetic responses shaped after real Transparency Platform API output for bidding zone NL (`10YNL----------L`). Values are generated, not real market prices. Regenerating them changes the expected values below.

## Files

- `a44_nl_pt15m.xml`: day-ahead prices (A44) for delivery day 2026-10-02 (CEST), PT15M resolution.
- `a44_nl_pt15m_revised.xml`: the same delivery day with a later `createdDateTime` and changed values. Use it for the UPDATE path and the stale-write guard (ADR 0012).
- `a44_nl_pt15m_dst_end.xml`: delivery day 2026-10-25, the end of CEST, so the day has 25 hours and 100 quarter-hour positions.
- `acknowledgement_no_data.xml`: what the API returns when there is no data for the requested interval (reason code 999). The real API has returned it with HTTP 200 as well as 4xx, so the parser must check the root element, not only the status code.

## Quirks the parser has to handle

- `curveType` is `A03` (variable-sized blocks): a position is omitted when its price equals the previous position's. Omitted positions inherit the last price, including trailing positions up to the end of the `Period`.
- Interval timestamps have no seconds (`2026-10-01T22:00Z`), while `createdDateTime` has them (`2026-10-01T11:02:37Z`).
- Prices can be negative (4 negative slots in `a44_nl_pt15m.xml`).
- Delivery days are defined in local time (CET/CEST) but every timestamp in the document is UTC.

## Expected values after expanding A03

Slot start = `Period.timeInterval.start + (position - 1) * resolution`.

| File | Period (UTC) | createdDateTime | `<Point>`s | Slots | Sum of prices | Min | Max | Trailing omitted |
|---|---|---|---|---|---|---|---|---|
| `a44_nl_pt15m.xml` | 2026-10-01T22:00Z – 2026-10-02T22:00Z | 2026-10-01T11:02:37Z | 75 | 96 | 6780.94 | -4.67 @ 2026-10-02T11:30:00Z | 115.61 @ 2026-10-02T06:30:00Z | 94–96 = 68.71 |
| `a44_nl_pt15m_revised.xml` | 2026-10-01T22:00Z – 2026-10-02T22:00Z | 2026-10-02T09:15:04Z | 75 | 96 | 6789.25 | -4.99 @ 2026-10-02T10:45:00Z | 122.45 @ 2026-10-02T07:00:00Z | 94–96 = 68.71 |
| `a44_nl_pt15m_dst_end.xml` | 2026-10-24T22:00Z – 2026-10-25T23:00Z | 2026-10-24T11:03:12Z | 76 | 100 | 7095.21 | -27.18 @ 2026-10-25T11:30:00Z | 117.74 @ 2026-10-25T05:30:00Z | 98–100 = 75.00 |

`a44_nl_pt15m_revised.xml` differs from `a44_nl_pt15m.xml` in exactly 2 slots:
  - position 37 (2026-10-02T07:00:00Z): 109.95 → 122.45
  - position 52 (2026-10-02T10:45:00Z): -0.80 → -4.99
