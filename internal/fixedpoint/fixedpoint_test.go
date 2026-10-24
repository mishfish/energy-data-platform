package fixedpoint

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		in       string
		decimals int
		maxWhole int
		signed   bool
		want     int64
		ok       bool
	}{
		{"74.78", 2, 8, true, 7478, true},
		{"-4.67", 2, 8, true, -467, true},
		{"-4.67", 2, 8, false, 0, false},
		{"12.4", 2, 5, true, 1240, true},
		{"0", 6, 9, false, 0, true},
		{"173.869995", 6, 10, true, 173869995, true},
		{"1.234", 2, 8, true, 0, false},
		{"123456", 2, 5, true, 0, false},
		{"", 2, 5, true, 0, false},
		{"75.", 2, 5, true, 0, false},
		{".5", 2, 5, true, 0, false},
		{"+5", 2, 5, true, 0, false},
		{"--5", 2, 5, true, 0, false},
		{"1e3", 2, 5, true, 0, false},
		{" 1", 2, 5, true, 0, false},
	}
	for _, tt := range tests {
		got, ok := Parse(tt.in, tt.decimals, tt.maxWhole, tt.signed)
		if ok != tt.ok || got != tt.want {
			t.Errorf("Parse(%q, %d, %d, %t) = %d, %t; want %d, %t", tt.in, tt.decimals, tt.maxWhole, tt.signed, got, ok, tt.want, tt.ok)
		}
	}
}
