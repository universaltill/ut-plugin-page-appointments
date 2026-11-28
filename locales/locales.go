// Package locales embeds the plugin's own locale bundles. The till reads
// locales/<locale>.json from the plugin bundle for view-document keys; the
// plugin also needs them in-process for table cells, which carry plain text,
// not keys.
package locales

import (
	"embed"
	"encoding/json"
	"regexp"
	"strings"
)

// EN is locales/en.json, the English source. Other locales come from Weblate
// (ADR-0160); code PRs add English only.
//
//go:embed en.json
var EN []byte

// files holds every locales/*.json, so Weblate bundles dropped into this
// directory are shipped in the module without a code change.
//
//go:embed *.json
var files embed.FS

var bundles = load()

func load() map[string]map[string]string {
	out := map[string]map[string]string{}
	es, err := files.ReadDir(".")
	if err != nil {
		panic("locales: " + err.Error())
	}
	for _, e := range es {
		b, err := files.ReadFile(e.Name())
		if err != nil {
			panic("locales: " + err.Error())
		}
		m := map[string]string{}
		if err := json.Unmarshal(b, &m); err != nil {
			panic("locales: " + e.Name() + ": " + err.Error())
		}
		out[strings.TrimSuffix(e.Name(), ".json")] = m
	}
	return out
}

// Bundles returns every embedded bundle by locale code (a copy of the map
// header only; do not modify the inner maps).
func Bundles() map[string]map[string]string {
	out := make(map[string]map[string]string, len(bundles))
	for k, v := range bundles {
		out[k] = v
	}
	return out
}

// Lookup resolves key in the locale ("de", "de-DE" and "de_DE" all mean de),
// falls back to English, and returns the key itself when neither has it.
func Lookup(locale, key string) string {
	if i := strings.IndexAny(locale, "-_"); i > 0 {
		locale = locale[:i]
	}
	if v, ok := bundles[strings.ToLower(locale)][key]; ok && v != "" {
		return v
	}
	if v, ok := bundles["en"][key]; ok {
		return v
	}
	return key
}

var verbRE = regexp.MustCompile(`%[-+# 0-9.]*[a-zA-Z%]`)

// SameVerbs reports whether two format strings have the same % verbs in the
// same order (an escaped %% is ignored). A lone % or a %!x( marker is not a
// valid verb sequence.
func SameVerbs(english, translated string) bool {
	a, b := verbs(english), verbs(translated)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return !strings.Contains(translated, "%!") && !strings.HasSuffix(strings.ReplaceAll(translated, "%%", ""), "%")
}

func verbs(s string) []string {
	var out []string
	for _, v := range verbRE.FindAllString(s, -1) {
		if v != "%%" {
			out = append(out, v[len(v)-1:])
		}
	}
	return out
}
