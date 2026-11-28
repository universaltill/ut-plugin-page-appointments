package calendar

import (
	"errors"
	"strings"

	"github.com/universaltill/ut-plugin-page-appointments/src/store"
)

// MaxNameRunes bounds a client first name (appointment-v1 section 1, legal 3).
const MaxNameRunes = store.MaxFirstNameRunes

// Name errors. The strings are wire codes mapped to locale keys.
var (
	ErrNameRequired = errors.New("name_required")
	ErrNameTooLong  = errors.New("name_too_long")
	ErrNameInvalid  = errors.New("name_invalid")
)

// CleanName trims a first name and applies the binding name rules through the
// store's one validator (store.ValidateFirstName): at most 40 characters, no
// digit or other number, no "@", no control character or invalid byte. It
// never returns a partially cleaned value on error. An empty name is allowed
// only when allowEmpty (a walk-in is not asked for one).
func CleanName(s string, allowEmpty bool) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		if allowEmpty {
			return "", nil
		}
		return "", ErrNameRequired
	}
	out, err := store.ValidateFirstName(s)
	switch {
	case err == nil:
		return out, nil
	case errors.Is(err, store.ErrNameTooLong):
		return "", ErrNameTooLong
	default:
		return "", ErrNameInvalid
	}
}
