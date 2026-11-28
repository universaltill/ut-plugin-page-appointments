package locales

import (
	"sort"
	"strings"
	"testing"
)

func TestLookup_FallsBackToEnglish(t *testing.T) {
	const k = "appointments.admin.saved"
	if Lookup("en", k) == "" || Lookup("en", k) == k {
		t.Fatal("en key missing")
	}
	for _, loc := range []string{"", "xx", "de-DE", "en_GB"} {
		if Lookup(loc, k) != Lookup("en", k) {
			t.Errorf("locale %q did not fall back to English", loc)
		}
	}
	if Lookup("en", "no.such.key") != "no.such.key" {
		t.Error("an unknown key must come back as itself")
	}
}

func TestVerbs(t *testing.T) {
	if !SameVerbs("Hello %s, %d left", "Hallo %s, noch %d") {
		t.Error("same verbs refused")
	}
	if !SameVerbs("100%% of %s", "100%% von %s") {
		t.Error("escaped percent mishandled")
	}
	for _, bad := range []string{"Hallo %s", "Hallo %s %s %d", "Hallo %d, %s", "%!s(MISSING)", "Hallo %"} {
		if SameVerbs("Hello %s, %d left", bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

// Every key of every other bundle keeps the verbs of the English text, in
// order, so a translator cannot make Sprintf print %!s(MISSING).
func TestEveryBundleKeepsEnglishVerbs(t *testing.T) {
	en := Bundles()["en"]
	if len(en) == 0 {
		t.Fatal("no English bundle")
	}
	var bad []string
	for loc, b := range Bundles() {
		if loc == "en" {
			continue
		}
		for k, v := range b {
			if e, ok := en[k]; ok && !SameVerbs(e, v) {
				bad = append(bad, loc+":"+k)
			}
		}
	}
	sort.Strings(bad)
	if len(bad) > 0 {
		t.Errorf("verbs differ from English: %s", strings.Join(bad, ", "))
	}
}
