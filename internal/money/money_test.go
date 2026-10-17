package money

import (
	"errors"
	"math"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		in      string
		want    int64
		wantErr error
	}{
		{"50000", 5_000_000, nil},
		{"50000.5", 5_000_050, nil},
		{"50000.50", 5_000_050, nil},
		{"0.01", 1, nil},
		{"5,000,000", 500_000_000, nil},
		{"1_000", 100_000, nil},
		{"  42 ", 4_200, nil},
		{"007", 700, nil},
		{"999999999999999.99", MaxCents, nil},

		{"", 0, ErrInvalid},
		{"0", 0, ErrInvalid},
		{"0.00", 0, ErrInvalid},
		{"-20", 0, ErrInvalid},
		{"+20", 0, ErrInvalid},
		{"1e5", 0, ErrInvalid},
		{"abc", 0, ErrInvalid},
		{"Rp5000", 0, ErrInvalid},
		{".5", 0, ErrInvalid},
		{"5.", 0, ErrInvalid},
		{"1.2.3", 0, ErrInvalid},
		{"1.234", 0, ErrTooManyDecs},
		{"1000000000000000", 0, ErrTooLarge},
		{"99999999999999999999999", 0, ErrTooLarge},
	}
	for _, tt := range tests {
		got, err := Parse(tt.in)
		if !errors.Is(err, tt.wantErr) {
			t.Errorf("Parse(%q) error = %v, want %v", tt.in, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("Parse(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestFormat(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0.00"},
		{1, "0.01"},
		{50, "0.50"},
		{100, "1.00"},
		{100_000, "1,000.00"},
		{2_500_050, "25,000.50"},
		{500_000_000, "5,000,000.00"},
		{-100_000, "-1,000.00"},
		{-5, "-0.05"},
		{MaxCents, "999,999,999,999,999.99"},
		{math.MinInt64, "-92,233,720,368,547,758.08"},
	}
	for _, tt := range tests {
		if got := Format(tt.in); got != tt.want {
			t.Errorf("Format(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
