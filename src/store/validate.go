package store

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Binding build rules of appointment-v1 (sections 1 and 7).
const (
	MaxFirstNameRunes = 40

	MinRetentionDays     = 1
	DefaultRetentionDays = 90
	MaxRetentionDays     = 730

	// OverrideReasonHint is the English hint shown next to the conflict
	// override reason; the locale key is in locales/en.json.
	OverrideReasonHint = "Do not write client or health details"
)

// TimeOffReasons is the fixed list; a free-text reason about a staff member
// would be health data.
var TimeOffReasons = []string{"holiday", "away", "other"}

// Validation and repository errors. The strings are stable codes.
var (
	ErrNameTooLong     = errors.New("name_too_long")
	ErrNameChars       = errors.New("name_chars")
	ErrTimeOffReason   = errors.New("time_off_reason")
	ErrRetentionRange  = errors.New("retention_range")
	ErrNotFound        = errors.New("not_found")
	ErrNotCurrent      = errors.New("not_current")
	ErrStale           = errors.New("stale")
	ErrDepositReserved = errors.New("deposit_reserved")
	ErrInvalidInput    = errors.New("invalid_input")
)

// ValidateFirstName trims the name and checks it: at most 40 characters, no
// digit and no '@' (so a phone number or an address cannot be typed in), no
// control character. The empty name is allowed (an anonymous walk-in).
func ValidateFirstName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > MaxFirstNameRunes {
		return "", ErrNameTooLong
	}
	for _, r := range s {
		if r == '@' || unicode.IsDigit(r) || unicode.IsNumber(r) || unicode.IsControl(r) {
			return "", ErrNameChars
		}
	}
	return s, nil
}

// NormaliseTimeOffReason maps "" to "other" and refuses anything off the list.
func NormaliseTimeOffReason(s string) (string, error) {
	if s == "" {
		return "other", nil
	}
	for _, r := range TimeOffReasons {
		if s == r {
			return s, nil
		}
	}
	return "", ErrTimeOffReason
}

// ValidateRetentionDays checks the shop setting (1..730).
func ValidateRetentionDays(n int) error {
	if n < MinRetentionDays || n > MaxRetentionDays {
		return ErrRetentionRange
	}
	return nil
}
