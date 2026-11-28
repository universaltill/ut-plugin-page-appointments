package calendar

import (
	"errors"
	"strings"
	"testing"
)

func TestCleanName(t *testing.T) {
	rows := []struct {
		in    string
		empty bool
		want  string
		err   error
	}{
		{"  Anna ", false, "Anna", nil},
		{"Zoë-Marie", false, "Zoë-Marie", nil},
		{strings.Repeat("é", 40), false, strings.Repeat("é", 40), nil},
		{strings.Repeat("é", 41), false, "", ErrNameTooLong},
		{"", false, "", ErrNameRequired},
		{"   ", false, "", ErrNameRequired},
		{"", true, "", nil},
		{"Anna 2", false, "", ErrNameInvalid},
		{"Anna 07700 900123", false, "", ErrNameInvalid},
		{"٣ Anna", false, "", ErrNameInvalid}, // Arabic-indic digit
		{"anna@x.com", false, "", ErrNameInvalid},
		{"Anna\nSmith", false, "", ErrNameInvalid},
		{"bad\xffbyte", false, "", ErrNameInvalid},
	}
	for _, r := range rows {
		got, err := CleanName(r.in, r.empty)
		if got != r.want || !errors.Is(err, r.err) {
			t.Errorf("%q: got %q %v want %q %v", r.in, got, err, r.want, r.err)
		}
	}
}
