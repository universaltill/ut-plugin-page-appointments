package calendar

import (
	"fmt"

	"github.com/universaltill/ut-plugin-page-appointments/locales"
)

// tr resolves a key for table cells and composed literals, where the view
// format carries plain text, not keys. It goes through locales.Lookup, so the
// bundles embedded from Weblate (ADR-0160) apply as soon as they exist and
// everything else falls back to English.
type tr struct{ locale string }

func (t tr) s(key string, args ...any) string {
	v := locales.Lookup(t.locale, key)
	if len(args) == 0 {
		return v
	}
	// A translator's bad verb must not print %!s(MISSING): use the English
	// text when the verbs differ from English.
	if en := locales.Lookup("en", key); !locales.SameVerbs(en, v) {
		v = en
	}
	return fmt.Sprintf(v, args...)
}

// lit is a composed literal in the operator's language.
func (t tr) lit(key string, args ...any) Component { return Lit(t.s(key, args...)) }
