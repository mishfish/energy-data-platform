package entsoe

import (
	"strings"
	"testing"
)

func TestParseZones(t *testing.T) {
	zones, err := ParseZones("NL, DE_LU,NO_5")
	if err != nil {
		t.Fatalf("ParseZones: %v", err)
	}
	want := []Zone{{"NL", "10YNL----------L"}, {"DE_LU", "10Y1001A1001A82H"}, {"NO_5", "10Y1001A1001A48H"}}
	if len(zones) != len(want) {
		t.Fatalf("got %v, want %v", zones, want)
	}
	for i := range want {
		if zones[i] != want[i] {
			t.Errorf("zone %d = %v, want %v", i, zones[i], want[i])
		}
	}

	for list, wantErr := range map[string]string{
		"NL,,BE":           "empty zone name",
		"NL,XX":            `unknown bidding zone "XX"`,
		"NL,BE,NL":         `zone "NL" listed twice`,
		"10YNL----------L": "unknown bidding zone",
	} {
		if _, err := ParseZones(list); err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Errorf("ParseZones(%q) error = %v, want it to contain %q", list, err, wantErr)
		}
	}
}

func TestBiddingZonesAreUnique(t *testing.T) {
	seen := map[string]string{}
	for name, eic := range BiddingZones {
		if other, dup := seen[eic]; dup {
			t.Errorf("EIC %s is mapped by %s and %s", eic, name, other)
		}
		if len(eic) != 16 {
			t.Errorf("zone %s: EIC %q is not 16 characters", name, eic)
		}
		seen[eic] = name
	}
}
