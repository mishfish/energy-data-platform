// Package fixedpoint converts decimal strings to scaled integers without going through a float, so
// values from the sources stay exact from parser to database (ADR 0014, 0029).
package fixedpoint

import (
	"strconv"
	"strings"
)

// Parse converts a plain decimal string such as "74.78", "-4.67" or "13663.505" to an integer with
// the given number of implied decimals. It accepts digits, at most one '.', and a leading '-' if
// signed; at most maxWhole digits before the point and decimals after it. maxWhole+decimals must
// stay below 19 digits. Anything else, including rounding, is rejected.
func Parse(s string, decimals, maxWhole int, signed bool) (int64, bool) {
	unsigned, negative := strings.CutPrefix(s, "-")
	if negative && !signed {
		return 0, false
	}
	whole, frac, hasPoint := strings.Cut(unsigned, ".")
	if !isDigits(whole) || len(whole) > maxWhole || hasPoint != isDigits(frac) || len(frac) > decimals {
		return 0, false
	}
	v, err := strconv.ParseInt(whole+frac+strings.Repeat("0", decimals-len(frac)), 10, 64)
	if err != nil {
		return 0, false
	}
	if negative {
		v = -v
	}
	return v, true
}

// isDigits reports whether s is a non-empty string of ASCII digits.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
