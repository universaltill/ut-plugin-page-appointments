// Package money is a faithful, trimmed copy of the Universal Till core money
// type.
//
// Source: universal-till/internal/money/money.go at commit a9cec47af (that
// package is internal/, so a plugin module cannot import it). Money is an
// amount in integer minor units. It is a named int64, so the compiler stops it
// mixing with unrelated integers, and it marshals to and from JSON as a bare
// integer.
//
// Not copied on purpose: MulDiv (int64 product overflows for large amounts;
// the engine uses a 128-bit path) and MulQty (no floats near money).
package money

import (
	"database/sql/driver"
	"fmt"
	"strconv"
)

// Money is a monetary amount in integer minor units.
type Money int64

// Zero is the additive identity.
const Zero Money = 0

// FromMinor wraps a raw minor-unit value (use at external DTO boundaries).
func FromMinor(v int64) Money { return Money(v) }

// Minor returns the raw minor-unit value (use at external DTO boundaries).
func (m Money) Minor() int64 { return int64(m) }

// Add returns m + n.
func (m Money) Add(n Money) Money { return m + n }

// Sub returns m - n.
func (m Money) Sub(n Money) Money { return m - n }

// Neg returns -m.
func (m Money) Neg() Money { return -m }

// Scale multiplies by an integer count (e.g. a whole quantity).
func (m Money) Scale(n int64) Money { return Money(int64(m) * n) }

// IsZero reports whether the amount is exactly zero.
func (m Money) IsZero() bool { return m == 0 }

// IsPositive reports whether the amount is greater than zero.
func (m Money) IsPositive() bool { return m > 0 }

// IsNegative reports whether the amount is less than zero.
func (m Money) IsNegative() bool { return m < 0 }

// Format renders the amount as a fixed two-decimal string (1234 -> "12.34").
func (m Money) Format() string {
	v := int64(m)
	neg := v < 0
	if neg {
		v = -v
	}
	s := strconv.FormatInt(v/100, 10) + "." + fmt.Sprintf("%02d", v%100)
	if neg {
		return "-" + s
	}
	return s
}

// String implements fmt.Stringer (two-decimal form).
func (m Money) String() string { return m.Format() }

// Value implements driver.Valuer: persisted as an integer.
func (m Money) Value() (driver.Value, error) { return int64(m), nil }

// Scan implements sql.Scanner: reads an integer column.
func (m *Money) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*m = 0
	case int64:
		*m = Money(v)
	case int:
		*m = Money(v)
	case []byte:
		n, err := strconv.ParseInt(string(v), 10, 64)
		if err != nil {
			return fmt.Errorf("money: scan %q: %w", v, err)
		}
		*m = Money(n)
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("money: scan %q: %w", v, err)
		}
		*m = Money(n)
	default:
		return fmt.Errorf("money: cannot scan %T into Money", src)
	}
	return nil
}
