# ENTSO-E test fixtures

Synthetic responses shaped after real Transparency Platform API output for bidding zone NL (`10YNL----------L`). Values are generated, not real market prices. Regenerating them changes the expected values below.

## Files

- `a44_nl_pt15m.xml`: day-ahead prices (A44) for delivery day 2026-10-02 (CEST), PT15M resolution.
- `a44_nl_pt15m_revised.xml`: the same delivery day with a later `createdDateTime` and changed values. Use it for the UPDATE path and the stale-write guard (ADR 0012).
- `a44_nl_pt15m_dst_end.xml`: delivery day 2026-10-25, the end of CEST, so the day has 25 hours and 100 quarter-hour positions.
- `a65_nl_pt15m.xml`: actual total load (A65, process type A16 realised) for delivery day 2026-10-02, PT15M, every position present.
- `a65_nl_pt15m_revised.xml`: the same day fetched a week later, with a later `createdDateTime` and two revised values. Use it for the UPDATE path.
- `a65_nl_pt15m_partial.xml`: the same day fetched at 12:58Z on the day itself. The document interval covers the whole day, but the `Period` ends at the last published market time unit (2026-10-02T12:00Z, 56 positions). Values equal the first 56 slots of `a65_nl_pt15m.xml`.
- `a65_nl_pt15m_gap.xml`: the same day with an hour missing at the source (2026-10-02T04:00Z–05:00Z). The gap splits the data into two `TimeSeries`; the parser must return 92 slots and must not fill the gap.
- `a65_nl_pt15m_dst_end.xml`: delivery day 2026-10-25 (end of CEST), 100 positions.
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

## Actual load (A65)

Load documents use a different root and field names than A44:

| | A44 prices | A65 load |
|---|---|---|
| Root element | `Publication_MarketDocument` | `GL_MarketDocument` |
| Bidding zone | `in_Domain.mRID` | `outBiddingZone_Domain.mRID` |
| Value | `price.amount` (EUR/MWH) | `quantity` (`MAW`, megawatts) |
| Process type | none | `process.processType`, `A16` = realised |

Quantities are carried as `int64` kW (ADR 0019): `13663.5` MW is 13663500 kW. The fixtures mix values with 0 to 3 decimals. All A65 fixtures use `curveType` `A01`, so every position in a `Period` is present.

| File | createdDateTime | Slots | Sum (kW) | Min (kW) | Max (kW) | First slot | Last slot |
|---|---|---|---|---|---|---|---|
| `a65_nl_pt15m.xml` | 2026-10-03T06:14:52Z | 96 | 1258023747 | 10659000 @ 2026-10-02T17:30:00Z | 15581560 @ 2026-10-02T10:30:00Z | 2026-10-01T22:00:00Z | 2026-10-02T21:45:00Z |
| `a65_nl_pt15m_revised.xml` | 2026-10-09T08:12:44Z | 96 | 1258301413 | 10659000 @ 2026-10-02T17:30:00Z | 15581560 @ 2026-10-02T10:30:00Z | 2026-10-01T22:00:00Z | 2026-10-02T21:45:00Z |
| `a65_nl_pt15m_partial.xml` | 2026-10-02T12:58:03Z | 56 | 764992059 | 11876000 @ 2026-10-02T04:15:00Z | 15581560 @ 2026-10-02T10:30:00Z | 2026-10-01T22:00:00Z | 2026-10-02T11:45:00Z |
| `a65_nl_pt15m_gap.xml` | 2026-10-03T06:15:30Z | 92 | 1209537187 | 10659000 @ 2026-10-02T17:30:00Z | 15581560 @ 2026-10-02T10:30:00Z | 2026-10-01T22:00:00Z | 2026-10-02T21:45:00Z |
| `a65_nl_pt15m_dst_end.xml` | 2026-10-26T06:20:11Z | 100 | 1312494489 | 10883390 @ 2026-10-25T16:30:00Z | 15471964 @ 2026-10-25T10:45:00Z | 2026-10-24T22:00:00Z | 2026-10-25T22:45:00Z |

`a65_nl_pt15m_revised.xml` differs from `a65_nl_pt15m.xml` in exactly 2 slots:
  - position 41 (2026-10-02T08:00:00Z): 14046500 → 13987250 kW
  - position 70 (2026-10-02T15:15:00Z): 11667084 → 12004000 kW
