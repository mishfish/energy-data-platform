package entsoe

import (
	"fmt"
	"slices"
	"strings"
)

// BiddingZones maps short names to the EIC codes of the bidding zones on the Transparency
// Platform. Names are used in configuration and as the "zone" metric label.
var BiddingZones = map[string]string{
	"AL":      "10YAL-KESH-----5",
	"AT":      "10YAT-APG------L",
	"BA":      "10YBA-JPCC-----D",
	"BE":      "10YBE----------2",
	"BG":      "10YCA-BULGARIA-R",
	"CH":      "10YCH-SWISSGRIDZ",
	"CY":      "10YCY-1001A0003J",
	"CZ":      "10YCZ-CEPS-----N",
	"DE_LU":   "10Y1001A1001A82H",
	"DK_1":    "10YDK-1--------W",
	"DK_2":    "10YDK-2--------M",
	"EE":      "10Y1001A1001A39I",
	"ES":      "10YES-REE------0",
	"FI":      "10YFI-1--------U",
	"FR":      "10YFR-RTE------C",
	"GB":      "10YGB----------A",
	"GR":      "10YGR-HTSO-----Y",
	"HR":      "10YHR-HEP------M",
	"HU":      "10YHU-MAVIR----U",
	"IE_SEM":  "10Y1001A1001A59C",
	"IT_CALA": "10Y1001C--00096J",
	"IT_CNOR": "10Y1001A1001A70O",
	"IT_CSUD": "10Y1001A1001A71M",
	"IT_NORD": "10Y1001A1001A73I",
	"IT_SARD": "10Y1001A1001A74G",
	"IT_SICI": "10Y1001A1001A75E",
	"IT_SUD":  "10Y1001A1001A788",
	"LT":      "10YLT-1001A0008Q",
	"LV":      "10YLV-1001A00074",
	"ME":      "10YCS-CG-TSO---S",
	"MK":      "10YMK-MEPSO----8",
	"NL":      "10YNL----------L",
	"NO_1":    "10YNO-1--------2",
	"NO_2":    "10YNO-2--------T",
	"NO_3":    "10YNO-3--------J",
	"NO_4":    "10YNO-4--------9",
	"NO_5":    "10Y1001A1001A48H",
	"PL":      "10YPL-AREA-----S",
	"PT":      "10YPT-REN------W",
	"RO":      "10YRO-TEL------P",
	"RS":      "10YCS-SERBIATSOV",
	"SE_1":    "10Y1001A1001A44P",
	"SE_2":    "10Y1001A1001A45N",
	"SE_3":    "10Y1001A1001A46L",
	"SE_4":    "10Y1001A1001A47J",
	"SI":      "10YSI-ELES-----O", //nolint:misspell // ELES is the Slovenian TSO
	"SK":      "10YSK-SEPS-----K",
	"UA_IPS":  "10Y1001C--000182",
	"XK":      "10Y1001C--00100H",
}

// Zone is a bidding zone by name and EIC code.
type Zone struct {
	Name string
	EIC  string
}

// ParseZones resolves a comma-separated list of zone names, in the given order. Unknown and
// repeated names are rejected.
func ParseZones(list string) ([]Zone, error) {
	var zones []Zone
	var seen []string
	for _, name := range strings.Split(list, ",") {
		name = strings.TrimSpace(name)
		eic, ok := BiddingZones[name]
		switch {
		case name == "":
			return nil, fmt.Errorf("empty zone name in %q", list)
		case !ok:
			return nil, fmt.Errorf("unknown bidding zone %q", name)
		case slices.Contains(seen, name):
			return nil, fmt.Errorf("zone %q listed twice", name)
		}
		seen = append(seen, name)
		zones = append(zones, Zone{Name: name, EIC: eic})
	}
	return zones, nil
}
