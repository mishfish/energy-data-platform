// Package entsoe parses responses of the ENTSO-E Transparency Platform API.
package entsoe

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/mishfish/energy-data-platform/internal/fixedpoint"
)

const (
	documentTypeDayAheadPrices = "A44"
	reasonCodeNoData           = "999"

	// curveSequentialFixed (A01) carries every position; curveVariableSized (A03) omits a position
	// whose value equals the previous one.
	curveSequentialFixed = "A01"
	curveVariableSized   = "A03"
)

// ErrNoData matches an AcknowledgementError that reports no data for the requested interval.
var ErrNoData = errors.New("entsoe: no matching data")

// AcknowledgementError is an Acknowledgement_MarketDocument returned instead of the requested document.
type AcknowledgementError struct {
	Code string
	Text string
}

func (e *AcknowledgementError) Error() string {
	return fmt.Sprintf("entsoe: acknowledgement %s: %s", e.Code, e.Text)
}

// Is reports whether target is ErrNoData and the acknowledgement carries the "no data" reason code.
func (e *AcknowledgementError) Is(target error) bool {
	return target == ErrNoData && e.Code == reasonCodeNoData
}

// DayAheadPrice is the price of one market time unit in one bidding zone.
type DayAheadPrice struct {
	BiddingZone     string        // EIC code
	DeliveryStart   time.Time     // start of the market time unit, UTC
	Resolution      time.Duration // length of the market time unit
	Sequence        int           // classificationSequence position; 0 when the document has a single sequence (ADR 0029)
	PriceMicros     int64         // millionths of EUR per MWh, can be negative (ADR 0029)
	SourceCreatedAt time.Time     // createdDateTime of the document, UTC
}

// Market time units accepted by the day_ahead_price table.
var resolutions = map[string]time.Duration{
	"PT15M": 15 * time.Minute,
	"PT30M": 30 * time.Minute,
	"PT60M": 60 * time.Minute,
}

type publicationDocument struct {
	Type            string       `xml:"type"`
	CreatedDateTime string       `xml:"createdDateTime"`
	TimeSeries      []timeSeries `xml:"TimeSeries"`
}

type timeSeries struct {
	InDomain  string   `xml:"in_Domain.mRID"`
	Sequence  string   `xml:"classificationSequence_AttributeInstanceComponent.position"`
	Currency  string   `xml:"currency_Unit.name"`
	PriceUnit string   `xml:"price_Measure_Unit.name"`
	CurveType string   `xml:"curveType"`
	Periods   []period `xml:"Period"`
}

type period struct {
	Start      string  `xml:"timeInterval>start"`
	End        string  `xml:"timeInterval>end"`
	Resolution string  `xml:"resolution"`
	Points     []point `xml:"Point"`
}

type point struct {
	Position int    `xml:"position"`
	Price    string `xml:"price.amount"`
}

type acknowledgementDocument struct {
	Reasons []struct {
		Code string `xml:"code"`
		Text string `xml:"text"`
	} `xml:"Reason"`
}

// ParseDayAheadPrices reads a day-ahead price document (A44) and returns one price per market time
// unit, with the positions omitted by curve type A03 filled in. Prices are in document order.
//
// The API answers "no data" with an Acknowledgement_MarketDocument, sometimes with HTTP 200, so the
// root element decides how the body is read. An acknowledgement is returned as *AcknowledgementError;
// use errors.Is(err, ErrNoData) to detect the "no data" case.
func ParseDayAheadPrices(r io.Reader) ([]DayAheadPrice, error) {
	dec := xml.NewDecoder(r)
	root, err := rootElement(dec)
	if err != nil {
		return nil, err
	}

	switch root.Name.Local {
	case "Publication_MarketDocument":
		var doc publicationDocument
		if err := dec.DecodeElement(&doc, &root); err != nil {
			return nil, fmt.Errorf("decode publication document: %w", err)
		}
		return dayAheadPrices(doc)
	case "Acknowledgement_MarketDocument":
		return nil, decodeAcknowledgement(dec, root)
	default:
		return nil, fmt.Errorf("unexpected root element %q", root.Name.Local)
	}
}

// decodeAcknowledgement reads the acknowledgement document that starts at root and returns it as
// *AcknowledgementError, or a plain error when it cannot be decoded.
func decodeAcknowledgement(dec *xml.Decoder, root xml.StartElement) error {
	var doc acknowledgementDocument
	if err := dec.DecodeElement(&doc, &root); err != nil {
		return fmt.Errorf("decode acknowledgement document: %w", err)
	}
	if len(doc.Reasons) == 0 {
		return errors.New("acknowledgement document without a reason")
	}
	return &AcknowledgementError{Code: doc.Reasons[0].Code, Text: doc.Reasons[0].Text}
}

func rootElement(dec *xml.Decoder) (xml.StartElement, error) {
	for {
		tok, err := dec.Token()
		if err != nil {
			return xml.StartElement{}, fmt.Errorf("read root element: %w", err)
		}
		if start, ok := tok.(xml.StartElement); ok {
			return start, nil
		}
	}
}

func dayAheadPrices(doc publicationDocument) ([]DayAheadPrice, error) {
	if doc.Type != documentTypeDayAheadPrices {
		return nil, fmt.Errorf("document type %q, want %q", doc.Type, documentTypeDayAheadPrices)
	}
	createdAt, err := parseTime(doc.CreatedDateTime)
	if err != nil {
		return nil, fmt.Errorf("createdDateTime: %w", err)
	}
	if len(doc.TimeSeries) == 0 {
		return nil, errors.New("publication document without time series")
	}

	type key struct {
		zone       string
		sequence   int
		start      time.Time
		resolution time.Duration
	}
	seen := make(map[key]struct{})

	var prices []DayAheadPrice
	for i, ts := range doc.TimeSeries {
		if err := checkTimeSeries(ts); err != nil {
			return nil, fmt.Errorf("time series %d: %w", i+1, err)
		}
		sequence, err := parseSequence(ts.Sequence)
		if err != nil {
			return nil, fmt.Errorf("time series %d: %w", i+1, err)
		}
		for _, p := range ts.Periods {
			expanded, err := expandPeriod(p, ts.CurveType)
			if err != nil {
				return nil, fmt.Errorf("time series %d, period %s: %w", i+1, p.Start, err)
			}
			for _, price := range expanded {
				price.BiddingZone = ts.InDomain
				price.Sequence = sequence
				price.SourceCreatedAt = createdAt
				// Two values for one table key in a single document would make the upsert ambiguous.
				k := key{price.BiddingZone, price.Sequence, price.DeliveryStart, price.Resolution}
				if _, dup := seen[k]; dup {
					return nil, fmt.Errorf("time series %d: duplicate price for zone %s, sequence %d at %s (%s)",
						i+1, k.zone, k.sequence, k.start.Format(time.RFC3339), k.resolution)
				}
				seen[k] = struct{}{}
				prices = append(prices, price)
			}
		}
	}
	return prices, nil
}

func checkTimeSeries(ts timeSeries) error {
	switch {
	case ts.InDomain == "":
		return errors.New("missing in_Domain.mRID")
	case ts.Currency != "EUR":
		return fmt.Errorf("currency %q, want EUR", ts.Currency)
	case ts.PriceUnit != "MWH":
		return fmt.Errorf("price measure unit %q, want MWH", ts.PriceUnit)
	case ts.CurveType != curveSequentialFixed && ts.CurveType != curveVariableSized:
		return fmt.Errorf("unsupported curve type %q", ts.CurveType)
	case len(ts.Periods) == 0:
		return errors.New("no periods")
	}
	return nil
}

// expandPeriod returns one price per position of the period, with only the time, resolution and
// price fields set.
func expandPeriod(p period, curveType string) ([]DayAheadPrice, error) {
	start, err := parseTime(p.Start)
	if err != nil {
		return nil, fmt.Errorf("interval start: %w", err)
	}
	end, err := parseTime(p.End)
	if err != nil {
		return nil, fmt.Errorf("interval end: %w", err)
	}
	resolution, ok := resolutions[p.Resolution]
	if !ok {
		return nil, fmt.Errorf("unsupported resolution %q", p.Resolution)
	}
	span := end.Sub(start)
	if span <= 0 || span%resolution != 0 {
		return nil, fmt.Errorf("interval %s/%s is not a positive multiple of %s", p.Start, p.End, p.Resolution)
	}
	slots := int(span / resolution)

	prices := make([]DayAheadPrice, 0, slots)
	var micros int64
	next := 0 // index of the first point not consumed yet
	for pos := 1; pos <= slots; pos++ {
		switch {
		case next < len(p.Points) && p.Points[next].Position == pos:
			micros, err = parsePriceMicros(p.Points[next].Price)
			if err != nil {
				return nil, fmt.Errorf("position %d: %w", pos, err)
			}
			next++
		case pos == 1 || curveType != curveVariableSized:
			return nil, fmt.Errorf("missing position %d", pos)
		}
		prices = append(prices, DayAheadPrice{
			DeliveryStart: start.Add(time.Duration(pos-1) * resolution),
			Resolution:    resolution,
			PriceMicros:   micros,
		})
	}
	if next < len(p.Points) {
		return nil, fmt.Errorf("point position %d is out of order or outside 1..%d", p.Points[next].Position, slots)
	}
	return prices, nil
}

// parseTime accepts timestamps with and without seconds: intervals come as 2026-10-01T22:00Z,
// createdDateTime as 2026-10-01T11:02:37Z.
func parseTime(s string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02T15:04Z07:00", time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid timestamp %q", s)
}

// parseSequence reads the classificationSequence position of a time series. Documents with several
// price sequences for one zone and day (several auctions) number them from 1; a document with a
// single sequence omits the element, which is stored as 0.
func parseSequence(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 99 {
		return 0, fmt.Errorf("invalid classificationSequence position %q", s)
	}
	return n, nil
}

// Scale of day-ahead prices: numeric(16, 6), i.e. micro-EUR per MWh (ADR 0029).
const (
	priceDecimals = 6
	priceMaxWhole = 10
)

// parsePriceMicros converts a decimal amount such as "74.78", "-4.67" or "173.869995" to millionths
// without going through a float. Amounts that do not fit numeric(16, 6) are rejected, not rounded.
func parsePriceMicros(s string) (int64, error) {
	v, ok := fixedpoint.Parse(s, priceDecimals, priceMaxWhole, true)
	if !ok {
		return 0, fmt.Errorf("invalid price amount %q", s)
	}
	return v, nil
}
