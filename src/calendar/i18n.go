package calendar

import (
	"encoding/json"
	"fmt"

	"github.com/universaltill/ut-plugin-page-appointments/locales"
)

// bundles maps a locale to its keys. Only English ships in code PRs; other
// locales arrive from Weblate (ADR-0160) and fall back to English until then.
var bundles = map[string]map[string]string{}

func init() {
	m := map[string]string{}
	if err := json.Unmarshal(locales.EN, &m); err != nil {
		panic("calendar: locales/en.json: " + err.Error())
	}
	bundles["en"] = m
}

// tr resolves a key for table cells and composed literals, where the view
// format carries plain text, not keys.
type tr struct{ locale string }

func (t tr) s(key string, args ...any) string {
	v, ok := bundles[t.locale][key]
	if !ok {
		v, ok = bundles["en"][key]
	}
	if !ok {
		return key
	}
	if len(args) > 0 {
		return fmt.Sprintf(v, args...)
	}
	return v
}

// lit is a composed literal in the operator's language.
func (t tr) lit(key string, args ...any) Component { return Lit(t.s(key, args...)) }
