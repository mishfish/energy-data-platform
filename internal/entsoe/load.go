package entsoe

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/mishfish/energy-data-platform/internal/fixedpoint"
)

const (
	documentTypeLoad    = "A65"
	processTypeRealised = "A16"
	quantityUnitMW      = "MAW"
)

// ActualLoad is the realised total load of one market time unit in one bidding zone.
type ActualLoad struct {
	BiddingZone     string        // EIC code
	DeliveryStart   time.Time     // start of the market time unit, UTC
	Resolution      time.Duration // length of the market time unit
	QuantityW       int64         // average load over the market time unit in watts (ADR 0029)
	SourceCreatedAt time.Time     // createdDateTime of the document, UTC
}

type glDocument struct {
	Type            string         `xml:"type"`
	ProcessType     string         `xml:"process.processType"`
	CreatedDateTime string         `xml:"createdDateTime"`
	TimeSeries      []glTimeSeries `xml:"TimeSeries"`
}

type glTimeSeries struct {
	InBiddingZone  string     `xml:"inBiddingZone_Domain.mRID"`  // A75: generation
	OutBiddingZone string     `xml:"outBiddingZone_Domain.mRID"` // A65: load; A75: consumption
	PSRType        string     `xml:"MktPSRType>psrType"`         // A75 only
	QuantityUnit   string     `xml:"quantity_Measure_Unit.name"`
	CurveType      string     `xml:"curveType"`
	Periods        []glPeriod `xml:"Period"`
}

type glPeriod struct {
	Start      string    `xml:"timeInterval>start"`
	End        string    `xml:"timeInterval>end"`
	Resolution string    `xml:"resolution"`
	Points     []glPoint `xml:"Point"`
}

type glPoint struct {
	Position int    `xml:"position"`
	Quantity string `xml:"quantity"`
}

// ParseActualLoad reads a realised total load document (A65, process type A16) and returns one value
// per market time unit, with the positions omitted by curve type A03 filled in. Values are in document
// order. A gap between periods or time series is kept as a gap: no value is synthesized for it.
//
// An acknowledgement is returned as *AcknowledgementError; use errors.Is(err, ErrNoData) to detect the
// "no data" case.
func ParseActualLoad(r io.Reader) ([]ActualLoad, error) {
	doc, err := decodeGL(r)
	if err != nil {
		return nil, err
	}
	return actualLoad(doc)
}

// decodeGL reads a GL_MarketDocument (A65, A75), or returns the acknowledgement sent instead.
func decodeGL(r io.Reader) (glDocument, error) {
	dec := xml.NewDecoder(r)
	root, err := rootElement(dec)
	if err != nil {
		return glDocument{}, err
	}

	switch root.Name.Local {
	case "GL_MarketDocument":
		var doc glDocument
		if err := dec.DecodeElement(&doc, &root); err != nil {
			return glDocument{}, fmt.Errorf("decode load document: %w", err)
		}
		return doc, nil
	case "Acknowledgement_MarketDocument":
		return glDocument{}, decodeAcknowledgement(dec, root)
	default:
		return glDocument{}, fmt.Errorf("unexpected root element %q", root.Name.Local)
	}
}

func actualLoad(doc glDocument) ([]ActualLoad, error) {
	switch {
	case doc.Type != documentTypeLoad:
		return nil, fmt.Errorf("document type %q, want %q", doc.Type, documentTypeLoad)
	case doc.ProcessType != processTypeRealised:
		return nil, fmt.Errorf("process type %q, want %q", doc.ProcessType, processTypeRealised)
	}
	createdAt, err := parseTime(doc.CreatedDateTime)
	if err != nil {
		return nil, fmt.Errorf("createdDateTime: %w", err)
	}
	if len(doc.TimeSeries) == 0 {
		return nil, errors.New("load document without time series")
	}

	type key struct {
		zone       string
		start      time.Time
		resolution time.Duration
	}
	seen := make(map[key]struct{})

	var loads []ActualLoad
	for i, ts := range doc.TimeSeries {
		if err := checkLoadTimeSeries(ts); err != nil {
			return nil, fmt.Errorf("time series %d: %w", i+1, err)
		}
		for _, p := range ts.Periods {
			expanded, err := expandQuantityPeriod(p, ts.CurveType)
			if err != nil {
				return nil, fmt.Errorf("time series %d, period %s: %w", i+1, p.Start, err)
			}
			for _, q := range expanded {
				l := ActualLoad{
					BiddingZone:     ts.OutBiddingZone,
					DeliveryStart:   q.start,
					Resolution:      q.resolution,
					QuantityW:       q.watts,
					SourceCreatedAt: createdAt,
				}
				// Two values for one table key in a single document would make the upsert ambiguous.
				k := key{l.BiddingZone, l.DeliveryStart, l.Resolution}
				if _, dup := seen[k]; dup {
					return nil, fmt.Errorf("time series %d: duplicate load for zone %s at %s (%s)",
						i+1, k.zone, k.start.Format(time.RFC3339), k.resolution)
				}
				seen[k] = struct{}{}
				loads = append(loads, l)
			}
		}
	}
	return loads, nil
}

func checkLoadTimeSeries(ts glTimeSeries) error {
	switch {
	case ts.OutBiddingZone == "":
		return errors.New("missing outBiddingZone_Domain.mRID")
	case ts.QuantityUnit != quantityUnitMW:
		return fmt.Errorf("quantity measure unit %q, want %s", ts.QuantityUnit, quantityUnitMW)
	case ts.CurveType != curveSequentialFixed && ts.CurveType != curveVariableSized:
		return fmt.Errorf("unsupported curve type %q", ts.CurveType)
	case len(ts.Periods) == 0:
		return errors.New("no periods")
	}
	return nil
}

// quantitySlot is one position of a GL period: a market time unit and its quantity in watts.
type quantitySlot struct {
	start      time.Time
	resolution time.Duration
	watts      int64
}

// expandQuantityPeriod returns one slot per position of a GL period (A65, A75), with the positions
// omitted by curve type A03 filled in from the previous one.
func expandQuantityPeriod(p glPeriod, curveType string) ([]quantitySlot, error) {
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

	out := make([]quantitySlot, 0, slots)
	var watts int64
	next := 0 // index of the first point not consumed yet
	for pos := 1; pos <= slots; pos++ {
		switch {
		case next < len(p.Points) && p.Points[next].Position == pos:
			watts, err = parseWatts(p.Points[next].Quantity)
			if err != nil {
				return nil, fmt.Errorf("position %d: %w", pos, err)
			}
			next++
		case pos == 1 || curveType != curveVariableSized:
			return nil, fmt.Errorf("missing position %d", pos)
		}
		out = append(out, quantitySlot{start: start.Add(time.Duration(pos-1) * resolution), resolution: resolution, watts: watts})
	}
	if next < len(p.Points) {
		return nil, fmt.Errorf("point position %d is out of order or outside 1..%d", p.Points[next].Position, slots)
	}
	return out, nil
}

// Scale of load quantities: numeric(15, 6) in MW, i.e. watts (ADR 0029).
const (
	loadDecimals = 6
	loadMaxWhole = 9
)

// parseWatts converts a quantity in MW such as "13663.5" or "47823.885889" to watts without going
// through a float. Quantities that do not fit numeric(15, 6) or are negative are rejected: total
// load cannot be negative, so a sign means the source format changed.
func parseWatts(s string) (int64, error) {
	v, ok := fixedpoint.Parse(s, loadDecimals, loadMaxWhole, false)
	if !ok {
		return 0, fmt.Errorf("invalid quantity %q", s)
	}
	return v, nil
}
