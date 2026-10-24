package main

import (
	"encoding/xml"
	"fmt"
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
	MRID              int        `xml:"mRID"`
	BusinessType      string     `xml:"businessType"`
	ObjectAggregation string     `xml:"objectAggregation"`
	OutBiddingZone    string     `xml:"outBiddingZone_Domain.mRID"`
	QuantityUnit      string     `xml:"quantity_Measure_Unit.name"`
	CurveType         string     `xml:"curveType"`
	Period            loadPeriod `xml:"Period"`
}

// genSeries is an A75 time series: generation goes in, consumption comes out of the zone.
type genSeries struct {
	MRID              int        `xml:"mRID"`
	BusinessType      string     `xml:"businessType"`
	ObjectAggregation string     `xml:"objectAggregation"`
	InBiddingZone     string     `xml:"inBiddingZone_Domain.mRID,omitempty"`
	OutBiddingZone    string     `xml:"outBiddingZone_Domain.mRID,omitempty"`
	QuantityUnit      string     `xml:"quantity_Measure_Unit.name"`
	CurveType         string     `xml:"curveType"`
	PSRType           string     `xml:"MktPSRType>psrType"`
	Period            loadPeriod `xml:"Period"`
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
// published at now, one time series per day as the real API does. ok is false when no day qualifies.
func (m model) priceDocument(zone string, start, end, now time.Time) (doc publicationDocument, ok bool) {
	var series []priceSeries
	for day := deliveryDayStart(start); day.Before(end); day = nextDeliveryDay(day) {
		if pricePublished(day).After(now) {
			break
		}
		next := nextDeliveryDay(day)
		var points []pricePoint
		last := int64(0)
		for pos, slot := 1, day; slot.Before(next); pos, slot = pos+1, slot.Add(slotLength) {
			cents := m.priceCents(zone, slot, now)
			// Curve type A03 omits a position whose price equals the previous one.
			if pos > 1 && cents == last {
				continue
			}
			points = append(points, pricePoint{Position: pos, Amount: formatFixed(cents, 2)})
			last = cents
		}
		series = append(series, priceSeries{
			MRID: len(series) + 1, InDomain: zone, OutDomain: zone,
			Currency: "EUR", PriceUnit: "MWH", CurveType: "A03",
			Period: pricePeriod{Interval: newInterval(day, next), Resolution: "PT15M", Points: points},
		})
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

// loadDocument returns the A65/A16 document for the slots in [start, end) that are published at now.
// The period ends at the last published slot, so a window reaching into today gives a partial day.
func (m model) loadDocument(zone string, start, end, now time.Time) (doc glDocument, ok bool) {
	first := start.Truncate(slotLength)
	if first.Before(start) {
		first = first.Add(slotLength)
	}
	var points []loadPoint
	slot := first
	for ; slot.Before(end) && !loadPublished(slot).After(now); slot = slot.Add(slotLength) {
		points = append(points, loadPoint{Position: len(points) + 1, Quantity: formatFixed(m.loadKW(zone, slot, now), 3)})
	}
	if len(points) == 0 {
		return glDocument{}, false
	}
	return glDocument{
		Xmlns:    "urn:iec62325.351:tc57wg16:451-6:generationloaddocument:3:0",
		header:   newHeader("A65", "A16", now),
		Interval: newInterval(start, end),
		TimeSeries: []loadSeries{{
			MRID: 1, BusinessType: "A04", ObjectAggregation: "A01", OutBiddingZone: zone,
			QuantityUnit: "MAW", CurveType: "A01",
			Period: loadPeriod{Interval: newInterval(first, slot), Resolution: "PT15M", Points: points},
		}},
	}, true
}

// generationDocument returns the A75/A16 document for the published slots in [start, end), one
// time series per production type and direction, published with the same lag as load.
func (m model) generationDocument(zone string, start, end, now time.Time) (doc genDocument, ok bool) {
	first := start.Truncate(slotLength)
	if first.Before(start) {
		first = first.Add(slotLength)
	}
	last := first
	for last.Before(end) && !loadPublished(last).After(now) {
		last = last.Add(slotLength)
	}
	if !last.After(first) {
		return genDocument{}, false
	}

	var series []genSeries
	for _, psrType := range generationTypes(zone) {
		directions := []string{"generation"}
		if psrType == "B10" {
			directions = append(directions, "consumption")
		}
		for _, direction := range directions {
			var points []loadPoint
			for slot := first; slot.Before(last); slot = slot.Add(slotLength) {
				points = append(points, loadPoint{Position: len(points) + 1, Quantity: formatFixed(m.generationKW(zone, psrType, direction, slot, now), 3)})
			}
			ts := genSeries{
				MRID: len(series) + 1, BusinessType: "A01", ObjectAggregation: "A08",
				QuantityUnit: "MAW", CurveType: "A01", PSRType: psrType,
				Period: loadPeriod{Interval: newInterval(first, last), Resolution: "PT15M", Points: points},
			}
			if direction == "generation" {
				ts.InBiddingZone = zone
			} else {
				ts.OutBiddingZone = zone
			}
			series = append(series, ts)
		}
	}
	return genDocument{
		Xmlns:      "urn:iec62325.351:tc57wg16:451-6:generationloaddocument:3:0",
		header:     newHeader("A75", "A16", now),
		Interval:   newInterval(start, end),
		TimeSeries: series,
	}, true
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
