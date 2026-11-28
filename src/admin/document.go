// Package admin builds the plugin's admin pages as plugin-view JSON documents
// (ut-docs reference/plugin-views.md, v1): resources, services, hours, time
// off and settings. It never writes HTML. Every text is a key from the
// plugin's own locales/en.json (code PRs carry English only; Weblate adds the
// other languages) or an escaped literal for shop data such as a resource
// name. Amounts are money.Money, sent to the till as integer minor units plus
// an ISO currency.
//
// The package is pure with respect to the host: it talks to the store.Repository
// only, so it runs offline and is tested with fixtures.
package admin

import (
	"encoding/json"

	"github.com/universaltill/ut-plugin-page-appointments/src/money"
)

// Text is a locale key or a literal (exactly one).
type Text struct {
	Key     string `json:"key,omitempty"`
	Literal string `json:"literal,omitempty"`
}

// K is a locale key text; L is a literal.
func K(key string) Text     { return Text{Key: key} }
func L(literal string) Text { return Text{Literal: literal} }

// Document is a view document, version 1.
type Document struct {
	Version    int         `json:"version"`
	Title      *Text       `json:"title,omitempty"`
	Components []Component `json:"components"`
}

// Component is one of the structs below; each marshals its own "type".
type Component interface{ component() }

// Heading is a section heading.
type Heading struct {
	Type string `json:"type"`
	Text Text   `json:"text"`
}

// Paragraph is a paragraph of text.
type Paragraph struct {
	Type string `json:"type"`
	Text Text   `json:"text"`
}

// Notice is an inline message; Level is info, warn or error.
type Notice struct {
	Type  string `json:"type"`
	Level string `json:"level"`
	Text  Text   `json:"text"`
}

// Column is a table column; Kind is text, number or money.
type Column struct {
	Label Text   `json:"label"`
	Kind  string `json:"kind"`
}

// Table shows rows; every cell matches its column kind (string, number,
// MoneyValue or nil).
type Table struct {
	Type    string   `json:"type"`
	Columns []Column `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

// EmptyState is shown when there is nothing to list.
type EmptyState struct {
	Type  string `json:"type"`
	Title Text   `json:"title"`
	Body  *Text  `json:"body,omitempty"`
}

// Option is a select option.
type Option struct {
	Value string `json:"value"`
	Label Text   `json:"label"`
}

// Field is a form field; Kind is text, number, money, select or toggle.
type Field struct {
	Name     string   `json:"name"`
	Label    Text     `json:"label"`
	Kind     string   `json:"kind"`
	Required bool     `json:"required,omitempty"`
	Options  []Option `json:"options,omitempty"`
	Value    any      `json:"value,omitempty"`
}

// Form posts _action=Action with its fields.
type Form struct {
	Type   string  `json:"type"`
	Action string  `json:"action"`
	Submit Text    `json:"submit"`
	Fields []Field `json:"fields"`
}

func (Heading) component()    {}
func (Paragraph) component()  {}
func (Notice) component()     {}
func (Table) component()      {}
func (EmptyState) component() {}
func (Form) component()       {}

// MoneyValue is the till's money cell: an integer in minor units.
type MoneyValue struct {
	Minor    int64  `json:"minor"`
	Currency string `json:"currency"`
}

// Money converts an amount.
func Money(m money.Money, currency string) MoneyValue {
	return MoneyValue{Minor: m.Minor(), Currency: currency}
}

func heading(key string) Heading     { return Heading{Type: "heading", Text: K(key)} }
func paragraph(key string) Paragraph { return Paragraph{Type: "text", Text: K(key)} }
func notice(level, key string) Notice {
	return Notice{Type: "notice", Level: level, Text: K(key)}
}
func table(cols []Column, rows [][]any) Table {
	if rows == nil {
		rows = [][]any{}
	}
	return Table{Type: "table", Columns: cols, Rows: rows}
}
func form(action, submitKey string, fields ...Field) Form {
	return Form{Type: "form", Action: action, Submit: K(submitKey), Fields: fields}
}

// MarshalJSON of Document is the default; JSON returns the answer body
// {"document": ...} the till expects.
func (d Document) JSON() ([]byte, error) { return json.Marshal(Response{Document: &d}) }
