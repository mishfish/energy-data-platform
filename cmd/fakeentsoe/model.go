package main

import (
	"encoding/binary"
	"hash/fnv"
	"math"
	"math/rand/v2"
	"strconv"
	"time"
	_ "time/tzdata" // the runtime image has no zoneinfo, and delivery days are defined in CET/CEST
)

const (
	// Day-ahead prices of a delivery day are published at 13:00 local time the day before,
	// i.e. 11 hours before the delivery day starts.
	pricePublicationLead = 11 * time.Hour
	// Revisions move a value by up to this share of it.
	priceRevisionShare    = 0.05
	quantityRevisionShare = 0.03
)

var amsterdam = mustLoadLocation("Europe/Amsterdam")

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err) // embedded tzdata makes this unreachable
	}
	return loc
}

// model generates deterministic values shaped by the calibrated zone profiles. A value depends only
// on the zone, the slot and the revision epoch that last changed it, so every response agrees on a
// slot until the slot is revised.
type model struct {
	// Time is split into epochs of this length; in each epoch a published slot younger than
	// revisionHorizon is revised with probability revisionRate.
	revisionInterval time.Duration
	revisionRate     float64
	revisionHorizon  time.Duration
	profiles         *profileSet
}

func (m model) zone(zone string) zoneProfile {
	if m.profiles == nil {
		return zoneProfile{}
	}
	return m.profiles.Zones[zone]
}

// price returns the day-ahead price of a slot in EUR/MWh for the i-th sequence of the profile.
// Prices drop on windy days; the wind regime is largely shared between zones, as weather systems
// are, which also correlates the prices of neighbouring markets.
func (m model) price(zone string, p *priceProfile, i int, slot, now time.Time) float64 {
	day := deliveryDayStart(slot)
	dayShift := p.DaySD * (-0.8*windRegime(zone, day) + 0.6*normal("price-day", zone, day))
	v := p.at(slot) + dayShift + p.SlotSD*noise("price", zone, slot, p.Jitter)
	v = widen(p.curve).clamp(v)
	if i > 0 {
		seq := p.Sequences[i]
		// Auctions for the same day track each other, so the difference drifts hour by hour.
		v += seq.Offset + seq.OffsetSD*smoothNoise("price-sequence-"+strconv.Itoa(seq.Position), zone, slot)
	}
	return m.revise("price", zone, slot, pricePublished(slot), now, v, priceRevisionShare)
}

// load returns the realised total load of a slot in MW.
func (m model) load(zone string, p *loadProfile, slot, now time.Time) float64 {
	c := p.Weekday
	if wd := slot.In(amsterdam).Weekday(); wd == time.Saturday || wd == time.Sunday {
		c = p.Weekend
	}
	day := deliveryDayStart(slot)
	v := c.at(slot) + c.DaySD*normal("load-day", zone, day) + c.SlotSD*noise("load", zone, slot, c.Jitter)
	v = max(widen(c).clamp(v), 0)
	return m.revise("load", zone, slot, published(slot, p.ResolutionMin, p.LagMin), now, v, quantityRevisionShare)
}

// generation returns the output (or, for consumption, the intake) of a production type in MW. Day
// and slot spreads scale with the daily shape, so solar stays at zero at night.
func (m model) generation(zone string, p *genProfile, s genSeriesProfile, slot, now time.Time) float64 {
	stream := "generation-" + s.PSRType + "-" + s.Direction
	day := deliveryDayStart(slot)
	var dayFactor float64
	switch s.PSRType {
	case "B18", "B19": // wind offshore, onshore
		dayFactor = windRegime(zone, day)
	default:
		dayFactor = normal(stream+"-day", zone, day)
	}
	base := s.at(slot)
	avg := meanHourly(s.Hourly)
	if avg <= 0 || base <= 0 {
		return 0
	}
	scale := base / avg
	v := base + scale*(s.DaySD*dayFactor+s.SlotSD*noise(stream, zone, slot, s.Jitter))
	v = max(widen(s.curve).clamp(v), 0)
	return m.revise(stream, zone, slot, published(slot, p.ResolutionMin, p.LagMin), now, v, quantityRevisionShare)
}

// revise applies the latest revision of a slot, if any, as a relative change of up to share.
func (m model) revise(stream, zone string, slot, publishedAt, now time.Time, v, share float64) float64 {
	rev := m.lastRevision(stream, zone, slot, publishedAt, now)
	if rev < 0 {
		return v
	}
	delta := (2*seed(stream+"-revision", zone, slot, rev).Float64() - 1) * share * math.Abs(v)
	return v + delta
}

// lastRevision returns the latest epoch in which the slot was revised, or -1 if it never was.
// Only epochs that start after publication and before the slot leaves the revision horizon count.
func (m model) lastRevision(stream, zone string, slot, publishedAt, now time.Time) int64 {
	if m.revisionRate <= 0 || m.revisionInterval <= 0 {
		return -1
	}
	first := m.epoch(publishedAt) + 1
	last := m.epoch(minTime(now, slot.Add(m.revisionHorizon)))
	for e := last; e >= first; e-- {
		if seed(stream+"-revised", zone, slot, e).Float64() < m.revisionRate {
			return e
		}
	}
	return -1
}

func (m model) epoch(t time.Time) int64 {
	return t.UnixNano() / int64(m.revisionInterval)
}

// missing reports whether a slot of a series is never published, which the real platform does for
// some zones (DK load misses about a quarter of its hours).
func missing(stream, zone string, slot time.Time, rate float64) bool {
	return rate > 0 && seed(stream+"-missing", zone, slot, 0).Float64() < rate
}

// windRegime is a standard normal per zone and delivery day: mostly shared across zones and
// persistent from one day to the next.
func windRegime(zone string, day time.Time) float64 {
	prev := deliveryDayStart(day.Add(-time.Hour))
	shared := (normal("wind", "", day) + normal("wind", "", prev)) / math.Sqrt2
	local := (normal("wind", zone, day) + normal("wind", zone, prev)) / math.Sqrt2
	return 0.8*shared + 0.6*local
}

// noiseKnots is the spacing of the slow part of the noise: real deviations from the daily shape
// (weather, outages) last for hours.
const noiseKnots = 3 * time.Hour

// noise is a standard normal per slot: a share jitter changes from slot to slot, the rest drifts
// smoothly, so consecutive slots move together as they do on the real platform.
func noise(stream, zone string, slot time.Time, jitter float64) float64 {
	jitter = min(max(jitter, 0), 1)
	return math.Sqrt(1-jitter*jitter)*smoothNoise(stream, zone, slot) + jitter*normal(stream, zone, slot)
}

// smoothNoise interpolates standard normals drawn every noiseKnots, scaled back to unit variance.
func smoothNoise(stream, zone string, slot time.Time) float64 {
	knot := slot.Truncate(noiseKnots)
	f := float64(slot.Sub(knot)) / float64(noiseKnots)
	a, b := normal(stream+"-knot", zone, knot), normal(stream+"-knot", zone, knot.Add(noiseKnots))
	return (a*(1-f) + b*f) / math.Sqrt((1-f)*(1-f)+f*f)
}

func normal(stream, zone string, t time.Time) float64 {
	return seed(stream, zone, t, 0).NormFloat64()
}

// widen loosens the observed range a little, so a short calibration window does not cap every
// day at the extremes it happened to see.
func widen(c curve) curve {
	pad := 0.1 * (c.Max - c.Min)
	c.Min, c.Max = c.Min-pad, c.Max+pad
	return c
}

func meanHourly(h [24]float64) float64 {
	var s float64
	for _, v := range h {
		s += v
	}
	return s / 24
}

func pricePublished(slot time.Time) time.Time {
	return deliveryDayStart(slot).Add(-pricePublicationLead)
}

// published returns when a realised slot becomes available: lag minutes after it ends.
func published(slot time.Time, resMin, lagMin int) time.Time {
	return slot.Add(time.Duration(resMin+lagMin) * time.Minute)
}

// deliveryDayStart returns the start of the CET/CEST day that contains t, in UTC.
func deliveryDayStart(t time.Time) time.Time {
	local := t.In(amsterdam)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, amsterdam).UTC()
}

// nextDeliveryDay returns the start of the delivery day after the one starting at dayStart.
func nextDeliveryDay(dayStart time.Time) time.Time {
	local := dayStart.In(amsterdam)
	return time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, amsterdam).UTC()
}

func localHour(t time.Time) float64 {
	local := t.In(amsterdam)
	return float64(local.Hour()) + float64(local.Minute())/60
}

// seed returns a generator that depends only on its arguments.
func seed(stream, zone string, slot time.Time, epoch int64) *rand.Rand {
	h := fnv.New64a()
	_, _ = h.Write([]byte(stream))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(zone))
	_, _ = h.Write(binary.BigEndian.AppendUint64(nil, uint64(slot.Unix()))) //nolint:gosec // bit pattern only
	return rand.New(rand.NewPCG(h.Sum64(), uint64(epoch)))                  //nolint:gosec // synthetic data, not security
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
