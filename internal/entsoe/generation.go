package entsoe

import (
	"errors"
	"fmt"
	"io"
	"time"
)

const documentTypeGeneration = "A75"

// Directions of a generation value (ADR 0030). A75 reports what a production type feeds into the
// zone (inBiddingZone) and, for types that also draw power such as pumped storage, what it takes out
// (outBiddingZone).
const (
	DirectionGeneration  = "generation"
	DirectionConsumption = "consumption"
)

// PSRTypes are the production types of the Transparency Platform, by code (ADR 0030). The same
// list is the psr_type reference table in Postgres and ClickHouse; integration tests keep them equal.
var PSRTypes = map[string]string{
	"B01": "Biomass",
	"B02": "Fossil Brown coal/Lignite",
	"B03": "Fossil Coal-derived gas",
	"B04": "Fossil Gas",
	"B05": "Fossil Hard coal",
	"B06": "Fossil Oil",
	"B07": "Fossil Oil shale",
	"B08": "Fossil Peat",
	"B09": "Geothermal",
	"B10": "Hydro Pumped Storage",
	"B11": "Hydro Run-of-river and poundage",
	"B12": "Hydro Water Reservoir",
	"B13": "Marine",
	"B14": "Nuclear",
	"B15": "Other renewable",
	"B16": "Solar",
	"B17": "Waste",
	"B18": "Wind Offshore",
	"B19": "Wind Onshore",
	"B20": "Other",
	"B21": "AC Link",
	"B22": "DC Link",
	"B23": "Substation",
	"B24": "Transformer",
	"B25": "Energy storage",
}

// Generation is the realised generation (or consumption) of one production type in one market time
// unit and bidding zone.
type Generation struct {
	BiddingZone     string        // EIC code
	PSRType         string        // production type code, see PSRTypes
	Direction       string        // DirectionGeneration or DirectionConsumption
	DeliveryStart   time.Time     // start of the market time unit, UTC
	Resolution      time.Duration // length of the market time unit
	QuantityW       int64         // average power over the market time unit in watts (ADR 0029)
	SourceCreatedAt time.Time     // createdDateTime of the document, UTC
}

// ParseGeneration reads a realised generation per production type document (A75, process type A16)
// and returns one value per production type, direction and market time unit, with the positions
// omitted by curve type A03 filled in. Gaps between periods stay gaps.
//
// An acknowledgement is returned as *AcknowledgementError; use errors.Is(err, ErrNoData) to detect the
// "no data" case.
func ParseGeneration(r io.Reader) ([]Generation, error) {
	doc, err := decodeGL(r)
	if err != nil {
		return nil, err
	}
	switch {
	case doc.Type != documentTypeGeneration:
		return nil, fmt.Errorf("document type %q, want %q", doc.Type, documentTypeGeneration)
	case doc.ProcessType != processTypeRealised:
		return nil, fmt.Errorf("process type %q, want %q", doc.ProcessType, processTypeRealised)
	}
	createdAt, err := parseTime(doc.CreatedDateTime)
	if err != nil {
		return nil, fmt.Errorf("createdDateTime: %w", err)
	}
	if len(doc.TimeSeries) == 0 {
		return nil, errors.New("generation document without time series")
	}

	type key struct {
		zone, psrType, direction string
		start                    time.Time
		resolution               time.Duration
	}
	seen := make(map[key]struct{})

	var out []Generation
	for i, ts := range doc.TimeSeries {
		zone, direction, err := checkGenerationTimeSeries(ts)
		if err != nil {
			return nil, fmt.Errorf("time series %d: %w", i+1, err)
		}
		for _, p := range ts.Periods {
			slots, err := expandQuantityPeriod(p, ts.CurveType)
			if err != nil {
				return nil, fmt.Errorf("time series %d, period %s: %w", i+1, p.Start, err)
			}
			for _, q := range slots {
				// Two values for one table key in a single document would make the upsert ambiguous.
				k := key{zone, ts.PSRType, direction, q.start, q.resolution}
				if _, dup := seen[k]; dup {
					return nil, fmt.Errorf("time series %d: duplicate %s %s for zone %s at %s (%s)",
						i+1, ts.PSRType, direction, zone, q.start.Format(time.RFC3339), q.resolution)
				}
				seen[k] = struct{}{}
				out = append(out, Generation{
					BiddingZone:     zone,
					PSRType:         ts.PSRType,
					Direction:       direction,
					DeliveryStart:   q.start,
					Resolution:      q.resolution,
					QuantityW:       q.watts,
					SourceCreatedAt: createdAt,
				})
			}
		}
	}
	return out, nil
}

// checkGenerationTimeSeries validates a time series and returns its zone and direction. Exactly one
// of the two domains must be set.
func checkGenerationTimeSeries(ts glTimeSeries) (zone, direction string, err error) {
	switch {
	case ts.InBiddingZone != "" && ts.OutBiddingZone != "":
		return "", "", errors.New("both inBiddingZone_Domain and outBiddingZone_Domain are set")
	case ts.InBiddingZone != "":
		zone, direction = ts.InBiddingZone, DirectionGeneration
	case ts.OutBiddingZone != "":
		zone, direction = ts.OutBiddingZone, DirectionConsumption
	default:
		return "", "", errors.New("missing inBiddingZone_Domain.mRID and outBiddingZone_Domain.mRID")
	}
	if _, ok := PSRTypes[ts.PSRType]; !ok {
		return "", "", fmt.Errorf("unknown production type %q", ts.PSRType)
	}
	switch {
	case ts.QuantityUnit != quantityUnitMW:
		return "", "", fmt.Errorf("quantity measure unit %q, want %s", ts.QuantityUnit, quantityUnitMW)
	case ts.CurveType != curveSequentialFixed && ts.CurveType != curveVariableSized:
		return "", "", fmt.Errorf("unsupported curve type %q", ts.CurveType)
	case len(ts.Periods) == 0:
		return "", "", errors.New("no periods")
	}
	return zone, direction, nil
}
