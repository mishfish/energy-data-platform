package main

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// calibrate derives profiles.json from the real data in the ClickHouse of the main stack. It runs
// by hand (`make fake-calibrate`) after enough live data has accumulated; the output is committed.
func calibrate(ctx context.Context, args []string, getenv func(string) string) error {
	fs := flag.NewFlagSet("calibrate", flag.ContinueOnError)
	out := fs.String("out", "cmd/fakeentsoe/profiles.json", "where to write the profiles")
	chURL := fs.String("clickhouse", "http://localhost:8123", "HTTP endpoint of the main stack's ClickHouse")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}
	ch := clickhouseHTTP{
		url:      *chURL,
		user:     cmp.Or(getenv("CLICKHOUSE_USER"), "energy"),
		password: cmp.Or(getenv("CLICKHOUSE_PASSWORD"), "energy"),
		database: cmp.Or(getenv("CLICKHOUSE_DB"), "energy"),
	}

	queries := map[string]string{
		"price": `SELECT bidding_zone, toString(sequence), toUnixTimestamp(delivery_start), intDiv(resolution_us, 60000000),
			toString(price_eur_mwh), toUnixTimestamp(source_created_at) FROM day_ahead_price FINAL`,
		"load": `SELECT bidding_zone, '', toUnixTimestamp(delivery_start), intDiv(resolution_us, 60000000),
			toString(load_mw), toUnixTimestamp(source_created_at) FROM actual_load FINAL`,
		"generation": `SELECT bidding_zone, concat(psr_type, '/', direction), toUnixTimestamp(delivery_start), intDiv(resolution_us, 60000000),
			toString(quantity_mw), toUnixTimestamp(source_created_at) FROM generation FINAL`,
	}
	set := profileSet{CalibratedFrom: map[string]string{}, Zones: map[string]zoneProfile{}}
	for _, dataset := range []string{"price", "load", "generation"} {
		rows, err := ch.rows(ctx, queries[dataset])
		if err != nil {
			return fmt.Errorf("%s: %w", dataset, err)
		}
		if len(rows) == 0 {
			return fmt.Errorf("%s: no rows in ClickHouse", dataset)
		}
		set.CalibratedFrom[dataset] = span(rows)
		for zone, zoneRows := range groupBy(rows, func(r obsRow) string { return r.zone }) {
			z := set.Zones[zone]
			switch dataset {
			case "price":
				z.Price = fitPrice(zoneRows)
			case "load":
				z.Load = fitLoad(zoneRows)
			case "generation":
				z.Generation = fitGeneration(zoneRows)
			}
			set.Zones[zone] = z
		}
	}

	body, err := marshalProfiles(set)
	if err != nil {
		return err
	}
	if _, err := parseProfiles(body); err != nil {
		return fmt.Errorf("generated profiles do not load: %w", err)
	}
	if err := os.WriteFile(*out, body, 0o644); err != nil { //nolint:gosec // committed source file
		return fmt.Errorf("write %s: %w", *out, err)
	}
	fmt.Printf("wrote %d zones to %s\n", len(set.Zones), *out)
	return nil
}

// obsRow is one observed value: a slot of a zone and series (sequence, or psr type and direction).
type obsRow struct {
	zone, key string
	start     time.Time
	resMin    int
	value     float64
	decimals  int
	lag       time.Duration // source_created_at after the end of the slot
}

type clickhouseHTTP struct {
	url, user, password, database string
}

func (c clickhouseHTTP) rows(ctx context.Context, query string) ([]obsRow, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.url+"/?database="+url.QueryEscape(c.database), strings.NewReader(query+" FORMAT TabSeparated"))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("X-ClickHouse-User", c.user)
	req.Header.Set("X-ClickHouse-Key", c.password)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query ClickHouse: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("ClickHouse: %s: %s", resp.Status, bytes.TrimSpace(msg))
	}

	var rows []obsRow
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		f := strings.Split(sc.Text(), "\t")
		if len(f) != 6 {
			return nil, fmt.Errorf("unexpected row %q", sc.Text())
		}
		start, err1 := strconv.ParseInt(f[2], 10, 64)
		res, err2 := strconv.Atoi(f[3])
		value, err3 := strconv.ParseFloat(f[4], 64)
		created, err4 := strconv.ParseInt(f[5], 10, 64)
		if err := cmp.Or(err1, err2, err3, err4); err != nil {
			return nil, fmt.Errorf("row %q: %w", sc.Text(), err)
		}
		decimals := 0
		if i := strings.IndexByte(f[4], '.'); i >= 0 {
			decimals = len(strings.TrimRight(f[4][i+1:], "0"))
		}
		end := time.Unix(start, 0).Add(time.Duration(res) * time.Minute)
		rows = append(rows, obsRow{
			zone: f[0], key: f[1], start: time.Unix(start, 0).UTC(), resMin: res,
			value: value, decimals: decimals, lag: time.Unix(created, 0).Sub(end),
		})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read ClickHouse response: %w", err)
	}
	return rows, nil
}

func fitPrice(rows []obsRow) *priceProfile {
	bySeq := groupBy(rows, func(r obsRow) string { return r.key })
	// The sequence with the most slots is the reference the others are compared with.
	keys := sortedKeys(bySeq)
	slices.SortStableFunc(keys, func(a, b string) int { return cmp.Compare(len(bySeq[b]), len(bySeq[a])) })
	ref := bySeq[keys[0]]
	refBySlot := map[time.Time]float64{}
	for _, r := range ref {
		refBySlot[r.start] = r.value
	}

	p := &priceProfile{ResolutionMin: modeRes(rows), curve: fitCurve(ref)}
	for i, key := range keys {
		seqRows := bySeq[key]
		pos, _ := strconv.Atoi(key)
		seq := priceSequence{Position: pos, Decimals: maxDecimals(seqRows)}
		seq.FirstHour, seq.LastHour = hourCoverage(seqRows)
		if i > 0 {
			var diffs []float64
			for _, r := range seqRows {
				if v, ok := refBySlot[r.start]; ok {
					diffs = append(diffs, r.value-v)
				}
			}
			if len(diffs) == 0 {
				diffs = []float64{mean(values(seqRows)) - mean(values(ref))}
			}
			seq.Offset, seq.OffsetSD = round3(mean(diffs)), round3(stddev(diffs))
		}
		p.Sequences = append(p.Sequences, seq)
	}
	return p
}

func fitLoad(rows []obsRow) *loadProfile {
	weekend := func(r obsRow) bool {
		wd := r.start.In(amsterdam).Weekday()
		return wd == time.Saturday || wd == time.Sunday
	}
	var wk, we []obsRow
	for _, r := range rows {
		if weekend(r) {
			we = append(we, r)
		} else {
			wk = append(wk, r)
		}
	}
	p := &loadProfile{
		ResolutionMin: modeRes(rows), Decimals: maxDecimals(rows),
		LagMin: minLagMin(rows), MissingRate: missingRate(rows),
	}
	if len(wk) == 0 {
		wk = we
	}
	if len(we) == 0 {
		we = wk
	}
	p.Weekday, p.Weekend = fitCurve(wk), fitCurve(we)
	return p
}

func fitGeneration(rows []obsRow) *genProfile {
	p := &genProfile{ResolutionMin: modeRes(rows), Decimals: maxDecimals(rows), LagMin: minLagMin(rows)}
	bySeries := groupBy(rows, func(r obsRow) string { return r.key })
	for _, key := range sortedKeys(bySeries) {
		psr, dir, _ := strings.Cut(key, "/")
		p.Series = append(p.Series, genSeriesProfile{
			PSRType: psr, Direction: dir, MissingRate: missingRate(bySeries[key]), curve: fitCurve(bySeries[key]),
		})
	}
	return p
}

// fitCurve splits the values into an hourly profile, a per-day offset and a per-slot residual.
func fitCurve(rows []obsRow) curve {
	type acc struct {
		sum float64
		n   int
	}
	dayHour := map[string]*[24]acc{}
	for _, r := range rows {
		day := r.start.In(amsterdam).Format(time.DateOnly)
		if dayHour[day] == nil {
			dayHour[day] = &[24]acc{}
		}
		a := &dayHour[day][r.start.In(amsterdam).Hour()]
		a.sum += r.value
		a.n++
	}
	hourMean := func(a acc) float64 { return a.sum / float64(a.n) }

	var c curve
	var have [24]bool
	for h := range 24 {
		var means []float64
		for _, hours := range dayHour {
			if hours[h].n > 0 {
				means = append(means, hourMean(hours[h]))
			}
		}
		if len(means) > 0 {
			c.Hourly[h], have[h] = mean(means), true
		}
	}
	fillMissingHours(&c.Hourly, have)

	offsets := map[string]float64{}
	for day, hours := range dayHour {
		var diffs []float64
		for h, a := range hours {
			if a.n > 0 {
				diffs = append(diffs, hourMean(a)-c.Hourly[h])
			}
		}
		offsets[day] = mean(diffs)
	}
	if len(offsets) > 1 {
		c.DaySD = stddev(mapValues(offsets))
	}
	residual := map[time.Time]float64{}
	residuals := make([]float64, 0, len(rows))
	for _, r := range rows {
		v := r.value - c.at(r.start) - offsets[r.start.In(amsterdam).Format(time.DateOnly)]
		residual[r.start] = v
		residuals = append(residuals, v)
	}
	c.SlotSD = stddev(residuals)
	c.Jitter = jitter(rows, residual, c.SlotSD)
	c.Min, c.Max = slices.Min(values(rows)), slices.Max(values(rows))

	for h := range c.Hourly {
		c.Hourly[h] = round3(c.Hourly[h])
	}
	c.DaySD, c.SlotSD, c.Min, c.Max = round3(c.DaySD), round3(c.SlotSD), round3(c.Min), round3(c.Max)
	return c
}

// jitter estimates the share of the residual spread that is independent from slot to slot: for
// white noise consecutive differences have sd √2·σ, for a slow drift close to zero.
func jitter(rows []obsRow, residual map[time.Time]float64, sd float64) float64 {
	if sd == 0 {
		return 0
	}
	var steps []float64
	for _, r := range rows {
		if prev, ok := residual[r.start.Add(-time.Duration(r.resMin)*time.Minute)]; ok {
			steps = append(steps, residual[r.start]-prev)
		}
	}
	if len(steps) < 2 {
		return 1
	}
	return round3(min(math.Sqrt(mean(squares(steps))/2)/sd, 1))
}

func squares(xs []float64) []float64 {
	out := make([]float64, len(xs))
	for i, x := range xs {
		out[i] = x * x
	}
	return out
}

// fillMissingHours copies the nearest observed hour into hours without data.
func fillMissingHours(hourly *[24]float64, have [24]bool) {
	for h := range 24 {
		if have[h] {
			continue
		}
		for d := 1; d < 24; d++ {
			if have[(h+d)%24] {
				hourly[h] = hourly[(h+d)%24]
				break
			}
			if have[(h-d+24)%24] {
				hourly[h] = hourly[(h-d+24)%24]
				break
			}
		}
	}
}

// hourCoverage returns the hours of the day a sequence covers, as [first, last).
func hourCoverage(rows []obsRow) (first, last int) {
	first, last = 24, 0
	for _, r := range rows {
		h := r.start.In(amsterdam).Hour()
		first, last = min(first, h), max(last, h+1)
	}
	return first, last
}

// minLagMin is the shortest observed delay between the end of a slot and the time ingest saw it,
// the best available estimate of the publication lag (ingest polls every 15 minutes).
func minLagMin(rows []obsRow) int {
	lag := rows[0].lag
	for _, r := range rows {
		lag = min(lag, r.lag)
	}
	return max(int(lag.Minutes()), 0)
}

// missingRate is the share of slots absent between the first and the last slot of each series,
// counted on the resolution the zone mostly publishes.
func missingRate(rows []obsRow) float64 {
	var expected, got int
	for _, series := range groupBy(rows, func(r obsRow) string { return r.key }) {
		first, last := series[0].start, series[0].start
		for _, r := range series {
			first, last = minTime(first, r.start), maxTime(last, r.start)
		}
		expected += int(last.Sub(first)/(time.Duration(modeRes(series))*time.Minute)) + 1
		got += len(series)
	}
	if expected == 0 || got >= expected {
		return 0
	}
	return round3(float64(expected-got) / float64(expected))
}

func modeRes(rows []obsRow) int {
	counts := map[int]int{}
	for _, r := range rows {
		counts[r.resMin]++
	}
	best := 0
	for res, n := range counts {
		if n > counts[best] || (n == counts[best] && res < best) {
			best = res
		}
	}
	return best
}

func maxDecimals(rows []obsRow) int {
	d := 0
	for _, r := range rows {
		d = max(d, r.decimals)
	}
	return d
}

func span(rows []obsRow) string {
	first, last := rows[0].start, rows[0].start
	for _, r := range rows {
		first, last = minTime(first, r.start), maxTime(last, r.start)
	}
	return first.Format(time.DateOnly) + "/" + last.Format(time.DateOnly)
}

// marshalProfiles renders indented JSON with number arrays kept on one line.
func marshalProfiles(set profileSet) ([]byte, error) {
	body, err := json.MarshalIndent(set, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode profiles: %w", err)
	}
	numberArray := regexp.MustCompile(`\[[-0-9.,\s]+\]`)
	body = numberArray.ReplaceAllFunc(body, func(m []byte) []byte {
		return []byte(strings.ReplaceAll(strings.Join(strings.Fields(string(m)), ""), ",", ", "))
	})
	return append(body, '\n'), nil
}

func groupBy(rows []obsRow, key func(obsRow) string) map[string][]obsRow {
	out := map[string][]obsRow{}
	for _, r := range rows {
		out[key(r)] = append(out[key(r)], r)
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func values(rows []obsRow) []float64 {
	out := make([]float64, len(rows))
	for i, r := range rows {
		out[i] = r.value
	}
	return out
}

func mapValues(m map[string]float64) []float64 {
	out := make([]float64, 0, len(m))
	for _, k := range sortedKeys(m) {
		out = append(out, m[k])
	}
	return out
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

func stddev(xs []float64) float64 {
	if len(xs) < 2 {
		return 0
	}
	m := mean(xs)
	var s float64
	for _, x := range xs {
		s += (x - m) * (x - m)
	}
	return math.Sqrt(s / float64(len(xs)))
}

func round3(x float64) float64 {
	return math.Round(x*1000) / 1000
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
