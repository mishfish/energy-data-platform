package main

import (
	"encoding/xml"
	"fmt"
	"math"
	"slices"
	"strconv"
	"time"
)

const (
	intervalLayout = "2006-01-02T15:04Z"
	createdLayout  = "2006-01-02T15:04:05Z"
	senderEIC      = "10X1001A1001A450"
)

type timeInterval struct {
	Start string `xml:"start"`
	End   string `xml:"end"`
}

func newInterval(start, end time.Time) timeInterval {
	return timeInterval{start.UTC().Format(intervalLayout), end.UTC().Format(intervalLayout)}
}

type header struct {
	MRID            string `xml:"mRID"`
	RevisionNumber  int    `xml:"revisionNumber"`
	Type            string `xml:"type"`
	ProcessType     string `xml:"process.processType,omitempty"`
	Sender          string `xml:"sender_MarketParticipant.mRID"`
	Receiver        string `xml:"receiver_MarketParticipant.mRID"`
	CreatedDateTime string `xml:"createdDateTime"`
}

func newHeader(docType, processType string, now time.Time) header {
	return header{
		MRID:            fmt.Sprintf("fake-%s-%d", docType, now.UnixNano()),
		RevisionNumber:  1,
		Type:            docType,
		ProcessType:     processType,
		Sender:          senderEIC,
		Receiver:        senderEIC,
		CreatedDateTime: now.UTC().Format(createdLayout),
	}
}

type publicationDocument struct {
	XMLName xml.Name `xml:"Publication_MarketDocument"`
	Xmlns   string   `xml:"xmlns,attr"`
	header
	Interval   timeInterval  `xml:"period.timeInterval"`
	TimeSeries []priceSeries `xml:"TimeSeries"`
}

type priceSeries struct {
	MRID      int         `xml:"mRID"`
	InDomain  string      `xml:"in_Domain.mRID"`
	OutDomain string      `xml:"out_Domain.mRID"`
	Currency  string      `xml:"currency_Unit.name"`
	PriceUnit string      `xml:"price_Measure_Unit.name"`
	Sequence  string      `xml:"classificationSequence_AttributeInstanceComponent.position,omitempty"`
	CurveType string      `xml:"curveType"`
	Period    pricePeriod `xml:"Period"`
}

type pricePeriod struct {
	Interval   timeInterval `xml:"timeInterval"`
	Resolution string       `xml:"resolution"`
	Points     []pricePoint `xml:"Point"`
}

type pricePoint struct {
	Position int    `xml:"position"`
	Amount   string `xml:"price.amount"`
}

type glDocument struct {
	XMLName xml.Name `xml:"GL_MarketDocument"`
	Xmlns   string   `xml:"xmlns,attr"`
	header
	Interval   timeInterval `xml:"time_Period.timeInterval"`
	TimeSeries []loadSeries `xml:"TimeSeries"`
}

type loadSeries struct {
	MRID              int          `xml:"mRID"`
	BusinessType      string       `xml:"businessType"`
	ObjectAggregation string       `xml:"objectAggregation"`
	OutBiddingZone    string       `xml:"outBiddingZone_Domain.mRID"`
	QuantityUnit      string       `xml:"quantity_Measure_Unit.name"`
	CurveType         string       `xml:"curveType"`
	Periods           []loadPeriod `xml:"Period"`
}

// genSeries is an A75 time series: generation goes in, consumption comes out of the zone.
type genSeries struct {
	MRID              int          `xml:"mRID"`
	BusinessType      string       `xml:"businessType"`
	ObjectAggregation string       `xml:"objectAggregation"`
	InBiddingZone     string       `xml:"inBiddingZone_Domain.mRID,omitempty"`
	OutBiddingZone    string       `xml:"outBiddingZone_Domain.mRID,omitempty"`
	QuantityUnit      string       `xml:"quantity_Measure_Unit.name"`
	CurveType         string       `xml:"curveType"`
	PSRType           string       `xml:"MktPSRType>psrType"`
	Periods           []loadPeriod `xml:"Period"`
}

type genDocument struct {
	XMLName xml.Name `xml:"GL_MarketDocument"`
	Xmlns   string   `xml:"xmlns,attr"`
	header
	Interval   timeInterval `xml:"time_Period.timeInterval"`
	TimeSeries []genSeries  `xml:"TimeSeries"`
}

type loadPeriod struct {
	Interval   timeInterval `xml:"timeInterval"`
	Resolution string       `xml:"resolution"`
	Points     []loadPoint  `xml:"Point"`
}

type loadPoint struct {
	Position int    `xml:"position"`
	Quantity string `xml:"quantity"`
}

type acknowledgementDocument struct {
	XMLName         xml.Name `xml:"Acknowledgement_MarketDocument"`
	Xmlns           string   `xml:"xmlns,attr"`
	MRID            string   `xml:"mRID"`
	CreatedDateTime string   `xml:"createdDateTime"`
	Reason          struct {
		Code string `xml:"code"`
		Text string `xml:"text"`
	} `xml:"Reason"`
}

// priceDocument returns the A44 document for every delivery day that overlaps [start, end) and is
// published at now, one time series per day and sequence as the real API does. ok is false when the
// zone has no prices or no day qualifies.
func (m model) priceDocument(zone string, start, end, now time.Time) (doc publicationDocument, ok bool) {
	p := m.zone(zone).Price
	if p == nil {
		return publicationDocument{}, false
	}
	res := time.Duration(p.ResolutionMin) * time.Minute
	order := sequenceOrder(p.Sequences)
	var series []priceSeries
	for day := deliveryDayStart(start); day.Before(end); day = nextDeliveryDay(day) {
		if pricePublished(day).After(now) {
			break
		}
		next := nextDeliveryDay(day)
		for _, i := range order {
			seq := p.Sequences[i]
			period, ok := m.pricePeriod(zone, p, i, day, next, res, now)
			if !ok {
				continue
			}
			ts := priceSeries{
				MRID: len(series) + 1, InDomain: zone, OutDomain: zone,
				Currency: "EUR", PriceUnit: "MWH", CurveType: "A03", Period: period,
			}
			if seq.Position > 0 {
				ts.Sequence = strconv.Itoa(seq.Position)
			}
			series = append(series, ts)
		}
	}
	if len(series) == 0 {
		return publicationDocument{}, false
	}
	return publicationDocument{
		Xmlns:      "urn:iec62325.351:tc57wg16:451-3:publicationdocument:7:3",
		header:     newHeader("A44", "", now),
		Interval:   newInterval(start, end),
		TimeSeries: series,
	}, true
}

// pricePeriod returns the slots of one delivery day that the i-th sequence covers.
func (m model) pricePeriod(zone string, p *priceProfile, i int, day, next time.Time, res time.Duration, now time.Time) (pricePeriod, bool) {
	seq := p.Sequences[i]
	var (
		points      []pricePoint
		first, last time.Time
		prev        string
	)
	for slot := day; slot.Before(next); slot = slot.Add(res) {
		if h := slot.In(amsterdam).Hour(); h < seq.FirstHour || h >= seq.LastHour {
			continue
		}
		if first.IsZero() {
			first = slot
		}
		last = slot.Add(res)
		amount := formatPrice(m.price(zone, p, i, slot, now), seq.Decimals)
		// Curve type A03 omits a position whose price equals the previous one.
		if len(points) > 0 && amount == prev {
			continue
		}
		points = append(points, pricePoint{Position: int(slot.Sub(first)/res) + 1, Amount: amount})
		prev = amount
	}
	if len(points) == 0 {
		return pricePeriod{}, false
	}
	return pricePeriod{Interval: newInterval(first, last), Resolution: resolutionCode(res), Points: points}, true
}

// sequenceOrder returns the indexes of the sequences by position, the order of the real documents.
func sequenceOrder(seqs []priceSequence) []int {
	order := make([]int, len(seqs))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return seqs[a].Position - seqs[b].Position })
	return order
}

// loadDocument returns the A65/A16 document for the slots in [start, end) that are published at now.
// The data ends at the last published slot, so a window reaching into today gives a partial day.
func (m model) loadDocument(zone string, start, end, now time.Time) (doc glDocument, ok bool) {
	p := m.zone(zone).Load
	if p == nil {
		return glDocument{}, false
	}
	res := time.Duration(p.ResolutionMin) * time.Minute
	first, last := publishedSlots(start, end, res, func(slot time.Time) bool {
		return !published(slot, p.ResolutionMin, p.LagMin).After(now)
	})
	periods := quantityPeriods(first, last, res,
		func(slot time.Time) bool { return missing("load", zone, slot, p.MissingRate) },
		func(slot time.Time) string { return formatQuantity(m.load(zone, p, slot, now), p.Decimals) })
	if len(periods) == 0 {
		return glDocument{}, false
	}
	return glDocument{
		Xmlns:    "urn:iec62325.351:tc57wg16:451-6:generationloaddocument:3:0",
		header:   newHeader("A65", "A16", now),
		Interval: newInterval(start, end),
		TimeSeries: []loadSeries{{
			MRID: 1, BusinessType: "A04", ObjectAggregation: "A01", OutBiddingZone: zone,
			QuantityUnit: "MAW", CurveType: "A03", Periods: periods,
		}},
	}, true
}

// generationDocument returns the A75/A16 document for the published slots in [start, end), one
// time series per production type and direction.
func (m model) generationDocument(zone string, start, end, now time.Time) (doc genDocument, ok bool) {
	p := m.zone(zone).Generation
	if p == nil {
		return genDocument{}, false
	}
	res := time.Duration(p.ResolutionMin) * time.Minute
	first, last := publishedSlots(start, end, res, func(slot time.Time) bool {
		return !published(slot, p.ResolutionMin, p.LagMin).After(now)
	})

	var series []genSeries
	for _, s := range p.Series {
		stream := "generation-" + s.PSRType + "-" + s.Direction
		periods := quantityPeriods(first, last, res,
			func(slot time.Time) bool { return missing(stream, zone, slot, s.MissingRate) },
			func(slot time.Time) string { return formatQuantity(m.generation(zone, p, s, slot, now), p.Decimals) })
		if len(periods) == 0 {
			continue
		}
		ts := genSeries{
			MRID: len(series) + 1, BusinessType: "A01", ObjectAggregation: "A08",
			QuantityUnit: "MAW", CurveType: "A03", PSRType: s.PSRType, Periods: periods,
		}
		if s.Direction == "consumption" {
			ts.OutBiddingZone = zone
		} else {
			ts.InBiddingZone = zone
		}
		series = append(series, ts)
	}
	if len(series) == 0 {
		return genDocument{}, false
	}
	return genDocument{
		Xmlns:      "urn:iec62325.351:tc57wg16:451-6:generationloaddocument:3:0",
		header:     newHeader("A75", "A16", now),
		Interval:   newInterval(start, end),
		TimeSeries: series,
	}, true
}

// publishedSlots returns [first, last): the slots on the resolution grid from start on, up to end
// or the first slot not yet published.
func publishedSlots(start, end time.Time, res time.Duration, isPublished func(time.Time) bool) (first, last time.Time) {
	first = start.Truncate(res)
	if first.Before(start) {
		first = first.Add(res)
	}
	last = first
	for last.Before(end) && isPublished(last) {
		last = last.Add(res)
	}
	return first, last
}

// quantityPeriods returns the A03 periods for the slots in [first, last). A missing slot ends a
// period, so gaps stay gaps instead of repeating the previous value.
func quantityPeriods(first, last time.Time, res time.Duration, isMissing func(time.Time) bool, value func(time.Time) string) []loadPeriod {
	var (
		periods []loadPeriod
		cur     *loadPeriod
		start   time.Time
		prev    string
	)
	closePeriod := func(end time.Time) {
		if cur != nil {
			cur.Interval = newInterval(start, end)
			periods = append(periods, *cur)
			cur = nil
		}
	}
	for slot := first; slot.Before(last); slot = slot.Add(res) {
		if isMissing(slot) {
			closePeriod(slot)
			continue
		}
		if cur == nil {
			cur, start, prev = &loadPeriod{Resolution: resolutionCode(res)}, slot, ""
		}
		q := value(slot)
		// Curve type A03 omits a position whose quantity equals the previous one.
		if len(cur.Points) > 0 && q == prev {
			continue
		}
		cur.Points = append(cur.Points, loadPoint{Position: int(slot.Sub(start)/res) + 1, Quantity: q})
		prev = q
	}
	closePeriod(last)
	return periods
}

func resolutionCode(res time.Duration) string {
	return fmt.Sprintf("PT%dM", int(res.Minutes()))
}

// formatPrice renders a price with the decimals the zone publishes. Six decimals stand for the
// float32 artefacts some TSOs send: 173.87 goes out as "173.869995".
func formatPrice(eur float64, decimals int) string {
	if decimals >= 6 {
		cents := math.Round(eur * 100)
		micros := int64(math.Round(float64(float32(cents/100)) * 1e6))
		return formatFixed(micros, 6)
	}
	return formatQuantity(eur, max(decimals, 2))
}

// formatQuantity renders a value rounded to the given decimals.
func formatQuantity(v float64, decimals int) string {
	return formatFixed(int64(math.Round(v*math.Pow10(decimals))), decimals)
}

func noDataAcknowledgement(item, zone string, start, end, now time.Time) acknowledgementDocument {
	ack := acknowledgementDocument{
		Xmlns:           "urn:iec62325.351:tc57wg16:451-1:acknowledgementdocument:7:0",
		MRID:            fmt.Sprintf("fake-ack-%d", now.UnixNano()),
		CreatedDateTime: now.UTC().Format(createdLayout),
	}
	ack.Reason.Code = "999"
	ack.Reason.Text = fmt.Sprintf("No matching data found for Data item %s (%s) and interval %s/%s.",
		item, zone, start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339))
	return ack
}

// formatFixed renders an integer amount with the given number of implied decimals, dropping
// trailing zeros like the real API does ("13663.5", "13837", "-4.67").
func formatFixed(v int64, decimals int) string {
	sign := ""
	if v < 0 {
		sign, v = "-", -v
	}
	scale := int64(1)
	for range decimals {
		scale *= 10
	}
	s := sign + strconv.FormatInt(v/scale, 10)
	frac := v % scale
	if frac == 0 {
		return s
	}
	digits := fmt.Sprintf("%0*d", decimals, frac)
	for digits[len(digits)-1] == '0' {
		digits = digits[:len(digits)-1]
	}
	return s + "." + digits
}
