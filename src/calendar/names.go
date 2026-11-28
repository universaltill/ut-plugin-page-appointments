package calendar

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxNameRunes bounds a client first name (appointment-v1 section 1, legal 3).
const MaxNameRunes = 40

// Name errors. The strings are wire codes mapped to locale keys.
var (
	ErrNameRequired = errors.New("name_required")
	ErrNameTooLong  = errors.New("name_too_long")
	ErrNameInvalid  = errors.New("name_invalid")
)

// CleanName trims a first name and applies the binding name rules: at most 40
// characters, no digit, no "@", no control character. It never returns a
// partially cleaned value on error. An empty name is allowed only when
// allowEmpty (a walk-in is not asked for one).
func CleanName(s string, allowEmpty bool) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		if allowEmpty {
			return "", nil
		}
		return "", ErrNameRequired
	}
	if utf8.RuneCountInString(s) > MaxNameRunes {
		return "", ErrNameTooLong
	}
	for _, r := range s {
		if r == '@' || unicode.IsDigit(r) || unicode.IsControl(r) || r == utf8.RuneError {
			return "", ErrNameInvalid
		}
	}
	return s, nil
}
