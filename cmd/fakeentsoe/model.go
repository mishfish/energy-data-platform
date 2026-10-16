package main

import (
	"encoding/binary"
	"hash/fnv"
	"math"
	"math/rand/v2"
	"time"
	_ "time/tzdata" // the runtime image has no zoneinfo, and delivery days are defined in CET/CEST
)

const (
	slotLength = 15 * time.Minute
	// Realised load of a slot becomes available this long after the slot ends.
	loadPublicationLag = time.Hour
	// Day-ahead prices of a delivery day are published at 13:00 local time the day before,
	// i.e. 11 hours before the delivery day starts.
	pricePublicationLead = 11 * time.Hour
)

var amsterdam = mustLoadLocation("Europe/Amsterdam")

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err) // embedded tzdata makes this unreachable
	}
	return loc
}

// model generates deterministic synthetic values. A value depends only on the zone, the slot and the
// revision epoch that last changed it, so every response agrees on a slot until the slot is revised.
type model struct {
	// Time is split into epochs of this length; in each epoch a published slot younger than
	// revisionHorizon is revised with probability revisionRate.
	revisionInterval time.Duration
	revisionRate     float64
	revisionHorizon  time.Duration
}

// priceCents returns the day-ahead price of a slot in EUR cents per MWh as seen at now.
func (m model) priceCents(zone string, slot, now time.Time) int64 {
	// A third of the hours trade as one hourly block, which gives the A03 curve something to compress.
	base := slot
	if seed("price-block", zone, slot.Truncate(time.Hour), 0).IntN(3) == 0 {
		base = slot.Truncate(time.Hour)
	}
	r := seed("price", zone, base, 0)
	h := localHour(base)
	eur := 75 + 30*bump(h, 8, 2) + 45*bump(h, 19, 2) - 70*bump(h, 13, 2.5) + 16*r.Float64() - 8
	if wd := base.In(amsterdam).Weekday(); wd == time.Saturday || wd == time.Sunday {
		eur -= 25 // lower weekend demand pushes midday solar hours below zero
	}
	cents := int64(math.Round(eur * 100))

	if rev := m.lastRevision("price", zone, slot, pricePublished(slot), now); rev >= 0 {
		cents += seed("price-revision", zone, slot, rev).Int64N(3001) - 1500
	}
	return cents
}

// loadKW returns the realised total load of a slot in kW as seen at now.
func (m model) loadKW(zone string, slot, now time.Time) int64 {
	r := seed("load", zone, slot, 0)
	h := localHour(slot)
	mw := 11000 + 3000*bump(h, 9, 3) + 3500*bump(h, 18.5, 2.5) - 1500*bump(h, 3.5, 2.5) + 500*r.Float64() - 250
	// TSOs publish between zero and three decimals; vary the granularity the same way.
	granularity := []int64{1000, 1000, 500, 250, 10, 1}[r.IntN(6)]
	kw := int64(mw*1000) / granularity * granularity

	if rev := m.lastRevision("load", zone, slot, loadPublished(slot), now); rev >= 0 {
		kw += seed("load-revision", zone, slot, rev).Int64N(800_001) - 400_000
	}
	return max(kw, 0)
}

// lastRevision returns the latest epoch in which the slot was revised, or -1 if it never was.
// Only epochs that start after publication and before the slot leaves the revision horizon count.
func (m model) lastRevision(dataset, zone string, slot, published, now time.Time) int64 {
	if m.revisionRate <= 0 || m.revisionInterval <= 0 {
		return -1
	}
	first := m.epoch(published) + 1
	last := m.epoch(minTime(now, slot.Add(m.revisionHorizon)))
	for e := last; e >= first; e-- {
		if seed(dataset+"-revised", zone, slot, e).Float64() < m.revisionRate {
			return e
		}
	}
	return -1
}

func (m model) epoch(t time.Time) int64 {
	return t.UnixNano() / int64(m.revisionInterval)
}

func pricePublished(slot time.Time) time.Time {
	return deliveryDayStart(slot).Add(-pricePublicationLead)
}

func loadPublished(slot time.Time) time.Time {
	return slot.Add(slotLength + loadPublicationLag)
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

// bump is a Gaussian bell with peak 1 at mu, used to shape daily profiles.
func bump(h, mu, sigma float64) float64 {
	return math.Exp(-(h - mu) * (h - mu) / (2 * sigma * sigma))
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
