// Package money parses and formats amounts stored as int64 minor units (cents).
package money

import (
	"errors"
	"strconv"
	"strings"
)

// MaxCents is the largest accepted amount: 999,999,999,999,999.99.
const MaxCents int64 = 99_999_999_999_999_999

const maxWholeDigits = 15

var (
	ErrInvalid     = errors.New("Amount must be a positive number, e.g. 25000 or 25000.50")
	ErrTooManyDecs = errors.New("Amount can have at most 2 decimals, e.g. 25000.50")
	ErrTooLarge    = errors.New("Amount is too large (max 999,999,999,999,999.99)")
)

// Parse converts user input such as "5,000,000", "25000.5" or "1_000" into cents.
// The result is always > 0.
func Parse(s string) (int64, error) {
	s = strings.NewReplacer(",", "", "_", "").Replace(strings.TrimSpace(s))
	whole, frac, hasDot := strings.Cut(s, ".")
	if !isDigits(whole) || (hasDot && !isDigits(frac)) {
		return 0, ErrInvalid
	}
	if len(frac) > 2 {
		return 0, ErrTooManyDecs
	}
	if len(strings.TrimLeft(whole, "0")) > maxWholeDigits {
		return 0, ErrTooLarge
	}

	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, ErrInvalid
	}
	f := int64(0)
	if frac != "" {
		f, _ = strconv.ParseInt(frac, 10, 64)
		if len(frac) == 1 {
			f *= 10
		}
	}

	cents := w*100 + f
	if cents <= 0 {
		return 0, ErrInvalid
	}
	return cents, nil
}

// Format renders cents as "5,000,000.00"; negative values get a leading "-".
func Format(cents int64) string {
	neg := cents < 0
	u := uint64(cents)
	if neg {
		u = -u
	}

	digits := strconv.FormatUint(u/100, 10)
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	for i, d := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(d)
	}
	b.WriteByte('.')
	rem := u % 100
	b.WriteByte(byte('0' + rem/10))
	b.WriteByte(byte('0' + rem%10))
	return b.String()
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
