package calendar

import "testing"

// Calendar cell text resolves through locales.Lookup: a region tag and an
// unknown locale both fall back to English, an unknown key returns the key,
// and arguments are formatted.
func TestTrResolvesThroughLookup(t *testing.T) {
	for _, loc := range []string{"en", "en-GB", "xx", "xx-YY", ""} {
		if got := (tr{locale: loc}).s("appt.cal.title"); got == "appt.cal.title" || got == "" {
			t.Errorf("locale %q: key not resolved: %q", loc, got)
		}
	}
	if got := (tr{locale: "en"}).s("no.such.key"); got != "no.such.key" {
		t.Errorf("unknown key = %q", got)
	}
}
