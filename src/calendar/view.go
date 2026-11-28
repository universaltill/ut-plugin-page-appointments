package calendar

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
)

// View names the plugin registers (manifest entries, wired by the host slice).
const (
	ViewDay   = "appointments.day"
	ViewToday = "appointments.today"
)

// Component is one view-document component. The view format refuses unknown
// fields, so builders below are the only place that spells them.
type Component map[string]any

// Document is a v1 view document.
type Document struct {
	Version    int         `json:"version"`
	Title      Component   `json:"title,omitempty"`
	Components []Component `json:"components"`
}

// Answer is the plugin's reply to a view or action ask: a document or a
// redirect to one of the plugin's own routes, never both.
type Answer struct {
	Document *Document `json:"document,omitempty"`
	Redirect string    `json:"redirect,omitempty"`
}

// MarshalJSON keeps the wire shape {"document":…} / {"redirect":…}.
func (a Answer) MarshalJSON() ([]byte, error) {
	if (a.Document == nil) == (a.Redirect == "") {
		return nil, fmt.Errorf("answer needs exactly one of document or redirect")
	}
	type plain Answer
	return json.Marshal(plain(a))
}

// Key is a text from the plugin's own locale bundle.
func Key(k string) Component { return Component{"key": k} }

// Lit is a text shown as typed (the till escapes it).
func Lit(s string) Component { return Component{"literal": s} }

func heading(t Component) Component { return Component{"type": "heading", "text": t} }
func para(t Component) Component    { return Component{"type": "text", "text": t} }
func notice(level string, t Component) Component {
	return Component{"type": "notice", "level": level, "text": t}
}
func button(action string, label Component, style string) Component {
	return Component{"type": "button", "action": action, "label": label, "style": style}
}
func list(items ...Component) Component { return Component{"type": "list", "items": items} }
func emptyState(title, body Component) Component {
	return Component{"type": "empty_state", "title": title, "body": body}
}

func table(cols []Component, kinds []string, rows [][]any) Component {
	cs := make([]Component, len(cols))
	for i := range cols {
		cs[i] = Component{"label": cols[i], "kind": kinds[i]}
	}
	if rows == nil {
		rows = [][]any{}
	}
	return Component{"type": "table", "columns": cs, "rows": rows}
}

func form(action string, submit Component, fields ...Component) Component {
	return Component{"type": "form", "action": action, "submit": submit, "fields": fields}
}

func field(name string, label Component, kind string, required bool, value any, options []Component) Component {
	f := Component{"name": name, "label": label, "kind": kind}
	if required {
		f["required"] = true
	}
	if value != nil {
		f["value"] = value
	}
	if options != nil {
		f["options"] = options
	}
	return f
}

func option(value string, label Component) Component {
	return Component{"value": value, "label": label}
}

// Limits of the view format (plugin-views.md "Limits").
const (
	maxComponents = 200
	maxString     = 4096
	maxAnswer     = 256 << 10
)

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_]{0,63}$`)

// CheckDocument validates a document against the parts of the view format
// this package uses, so a bad document fails a test instead of the till
// refusing it. It returns every problem found.
func CheckDocument(d Document) []string {
	var errs []string
	add := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }
	if d.Version != 1 {
		add("version %d", d.Version)
	}
	if len(d.Components) > maxComponents {
		add("%d components (max %d)", len(d.Components), maxComponents)
	}
	if b, err := json.Marshal(d); err != nil || len(b) > maxAnswer {
		add("answer size %d / %v", len(b), err)
	}
	checkText := func(where string, v any) {
		c, ok := v.(Component)
		if !ok {
			add("%s: not a text", where)
			return
		}
		k, lit := c["key"], c["literal"]
		if (k == nil) == (lit == nil) {
			add("%s: need exactly one of key/literal", where)
		}
		if s, _ := lit.(string); lit != nil && (s == "" || len(s) > maxString) {
			add("%s: literal empty or too long", where)
		}
	}
	if d.Title != nil {
		checkText("title", d.Title)
	}
	for i, c := range d.Components {
		w := fmt.Sprintf("component %d (%v)", i, c["type"])
		switch c["type"] {
		case "heading", "text":
			checkText(w, c["text"])
		case "notice":
			checkText(w, c["text"])
			if l := c["level"]; l != "info" && l != "warn" && l != "error" {
				add("%s: level %v", w, l)
			}
		case "list":
			items, _ := c["items"].([]Component)
			if len(items) == 0 || len(items) > 200 {
				add("%s: %d items", w, len(items))
			}
			for _, it := range items {
				checkText(w, it)
			}
		case "empty_state":
			checkText(w, c["title"])
		case "button":
			checkAction(add, w, c["action"])
			checkText(w, c["label"])
			if s := c["style"]; s != "primary" && s != "secondary" && s != "danger" {
				add("%s: style %v", w, s)
			}
		case "table":
			cols, _ := c["columns"].([]Component)
			rows, _ := c["rows"].([][]any)
			if len(cols) == 0 || len(cols) > 20 || rows == nil || len(rows) > 500 {
				add("%s: %d columns, rows %v", w, len(cols), rows == nil)
			}
			for ri, r := range rows {
				if len(r) != len(cols) {
					add("%s: row %d has %d cells", w, ri, len(r))
				}
			}
		case "form":
			checkAction(add, w, c["action"])
			checkText(w, c["submit"])
			fs, _ := c["fields"].([]Component)
			if len(fs) == 0 || len(fs) > 50 {
				add("%s: %d fields", w, len(fs))
			}
			seen := map[string]bool{}
			for _, f := range fs {
				n, _ := f["name"].(string)
				if !nameRE.MatchString(n) || seen[n] {
					add("%s: field name %q", w, n)
				}
				seen[n] = true
				checkText(w+" field "+n, f["label"])
				switch f["kind"] {
				case "text", "number", "money", "select", "toggle":
				default:
					add("%s: field kind %v", w, f["kind"])
				}
				if f["kind"] == "select" {
					if os, _ := f["options"].([]Component); len(os) == 0 || len(os) > 100 {
						add("%s: select %s has %d options", w, n, len(os))
					}
				}
			}
		default:
			add("%s: unknown component type", w)
		}
	}
	sort.Strings(errs)
	return errs
}

func checkAction(add func(string, ...any), w string, v any) {
	if s, _ := v.(string); !nameRE.MatchString(s) {
		add("%s: action name %v", w, v)
	}
}
