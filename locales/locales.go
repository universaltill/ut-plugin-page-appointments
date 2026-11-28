// Package locales embeds the plugin's own locale bundles. The till reads
// locales/<locale>.json from the plugin bundle for view-document keys; the
// plugin also needs them in-process for table cells, which carry plain text,
// not keys.
package locales

import _ "embed"

// EN is locales/en.json, the English source. Other locales come from Weblate
// (ADR-0160); code PRs add English only.
//
//go:embed en.json
var EN []byte
