package admin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/universaltill/ut-plugin-page-appointments/src/admin"
	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
	"github.com/universaltill/ut-plugin-page-appointments/src/money"
	"github.com/universaltill/ut-plugin-page-appointments/src/store"
	"github.com/universaltill/ut-plugin-page-appointments/src/store/memdb"
)

var ctx = context.Background()

type clk struct{}

func (clk) Now() time.Time { return time.Date(2026, 1, 14, 8, 0, 0, 0, time.UTC) }

func counter() engine.IDGen {
	n := 0
	return func() string { n++; return fmt.Sprintf("id-%d", n) }
}

func newHandler(t *testing.T) (*admin.Handler, *store.Repository) {
	t.Helper()
	ids := counter()
	repo := store.New(memdb.New(), clk{}, ids, "till1")
	h := &admin.Handler{Repo: repo, NewID: ids, Prices: func(sku string) (money.Money, bool) {
		if sku == "cut" {
			return money.FromMinor(2500), true
		}
		return 0, false
	}}
	return h, repo
}

func view(t *testing.T, h *admin.Handler, v, resource string) admin.Document {
	t.Helper()
	r, err := h.HandleView(ctx, admin.ViewRequest{View: v, Params: map[string]string{"resource": resource}})
	if err != nil || r.Document == nil || r.Redirect != "" {
		t.Fatalf("%v %+v", err, r)
	}
	checkDocument(t, *r.Document)
	return *r.Document
}

func act(t *testing.T, h *admin.Handler, v, a string, form map[string]any) admin.Document {
	t.Helper()
	r, err := h.HandleAction(ctx, admin.ActionRequest{View: v, Action: a, Form: form})
	if err != nil || r.Document == nil {
		t.Fatalf("%v %+v", err, r)
	}
	checkDocument(t, *r.Document)
	return *r.Document
}

func firstNotice(d admin.Document) (admin.Notice, bool) {
	for _, c := range d.Components {
		if n, ok := c.(admin.Notice); ok {
			return n, true
		}
	}
	return admin.Notice{}, false
}

func wantNotice(t *testing.T, d admin.Document, level, key string) {
	t.Helper()
	n, ok := firstNotice(d)
	if !ok || n.Level != level || n.Text.Key != key {
		t.Fatalf("notice %+v ok=%v, want %s %s", n, ok, level, key)
	}
}

// ---- v1 document rules (plugin-views.md) ----------------------------------------

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_]{0,63}$`)

func checkText(t *testing.T, v any, where string) {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Errorf("%s: text is not an object: %v", where, v)
		return
	}
	_, k := m["key"]
	_, l := m["literal"]
	if k == l || len(m) != 1 {
		t.Errorf("%s: text needs exactly one of key or literal: %v", where, m)
	}
}

// checkDocument validates a document against the v1 rules that matter here,
// and that it contains only known fields.
func checkDocument(t *testing.T, d admin.Document) {
	t.Helper()
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	_ = json.Unmarshal(b, &doc)
	if doc["version"] != float64(1) {
		t.Fatal("version")
	}
	for k := range doc {
		if k != "version" && k != "title" && k != "components" {
			t.Errorf("unknown document field %s", k)
		}
	}
	if ti, ok := doc["title"]; ok {
		checkText(t, ti, "title")
	}
	cs, _ := doc["components"].([]any)
	if cs == nil {
		t.Fatal("components must be a list")
	}
	allowed := map[string][]string{
		"heading": {"type", "text"}, "text": {"type", "text"}, "notice": {"type", "level", "text"},
		"table": {"type", "columns", "rows"}, "empty_state": {"type", "title", "body"},
		"form": {"type", "action", "submit", "fields"},
	}
	for i, c := range cs {
		m := c.(map[string]any)
		typ, _ := m["type"].(string)
		fields, ok := allowed[typ]
		if !ok {
			t.Errorf("component %d: type %q is not used by this plugin", i, typ)
			continue
		}
		for k := range m {
			if !contains(fields, k) {
				t.Errorf("component %d (%s): unknown field %s", i, typ, k)
			}
		}
		switch typ {
		case "heading", "text":
			checkText(t, m["text"], typ)
		case "notice":
			checkText(t, m["text"], typ)
			if l := m["level"]; l != "info" && l != "warn" && l != "error" {
				t.Errorf("level %v", l)
			}
		case "empty_state":
			checkText(t, m["title"], typ)
			if b, ok := m["body"]; ok {
				checkText(t, b, typ)
			}
		case "table":
			checkTable(t, m)
		case "form":
			checkForm(t, m)
		}
	}
}

func checkTable(t *testing.T, m map[string]any) {
	cols := m["columns"].([]any)
	if len(cols) < 1 || len(cols) > 20 {
		t.Errorf("columns %d", len(cols))
	}
	rows := m["rows"].([]any)
	if len(rows) > 500 {
		t.Errorf("rows %d", len(rows))
	}
	for _, c := range cols {
		cm := c.(map[string]any)
		checkText(t, cm["label"], "column")
	}
	for ri, r := range rows {
		cells := r.([]any)
		if len(cells) != len(cols) {
			t.Fatalf("row %d has %d cells for %d columns", ri, len(cells), len(cols))
		}
		for ci, cell := range cells {
			kind := cols[ci].(map[string]any)["kind"]
			switch kind {
			case "text":
				if _, ok := cell.(string); !ok && cell != nil {
					t.Errorf("row %d col %d: text cell %v", ri, ci, cell)
				}
			case "number":
				if _, ok := cell.(float64); !ok && cell != nil {
					t.Errorf("row %d col %d: number cell %v", ri, ci, cell)
				}
			case "money":
				if cell == nil {
					continue
				}
				mm, ok := cell.(map[string]any)
				cur, _ := mm["currency"].(string)
				if _, isNum := mm["minor"].(float64); !ok || !isNum || len(cur) != 3 || cur != strings.ToUpper(cur) || len(mm) != 2 {
					t.Errorf("row %d col %d: money cell %v", ri, ci, cell)
				}
			default:
				t.Errorf("column kind %v", kind)
			}
		}
	}
}

func checkForm(t *testing.T, m map[string]any) {
	if !nameRe.MatchString(m["action"].(string)) {
		t.Errorf("action %v", m["action"])
	}
	checkText(t, m["submit"], "submit")
	fields := m["fields"].([]any)
	if len(fields) > 50 {
		t.Errorf("fields %d", len(fields))
	}
	seen := map[string]bool{}
	for _, f := range fields {
		fm := f.(map[string]any)
		name := fm["name"].(string)
		if !nameRe.MatchString(name) || seen[name] {
			t.Errorf("field name %q", name)
		}
		seen[name] = true
		checkText(t, fm["label"], name)
		kind := fm["kind"]
		if !contains([]string{"text", "number", "money", "select", "toggle"}, kind.(string)) {
			t.Errorf("kind %v", kind)
		}
		if kind == "select" {
			opts := fm["options"].([]any)
			if len(opts) < 1 || len(opts) > 100 {
				t.Errorf("%s options %d", name, len(opts))
			}
			vals := []string{}
			for _, o := range opts {
				om := o.(map[string]any)
				checkText(t, om["label"], name)
				vals = append(vals, om["value"].(string))
			}
			if v, ok := fm["value"]; ok && !contains(vals, v.(string)) {
				t.Errorf("%s: value %v not in options %v", name, v, vals)
			}
		} else if _, has := fm["options"]; has {
			t.Errorf("%s: options on a %v field", name, kind)
		}
		if kind == "number" {
			if v, ok := fm["value"]; ok {
				if _, isStr := v.(string); !isStr {
					t.Errorf("%s: number value must be a decimal string", name)
				}
			}
		}
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// ---- locale ------------------------------------------------------------------------

func collectKeys(v any, out map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		for k, y := range x {
			if k == "key" {
				if s, ok := y.(string); ok {
					out[s] = true
				}
			}
			collectKeys(y, out)
		}
	case []any:
		for _, y := range x {
			collectKeys(y, out)
		}
	}
}

func TestLocale_EveryKeyDefinedAndUsed(t *testing.T) {
	raw, err := os.ReadFile("../../locales/en.json")
	if err != nil {
		t.Fatal(err)
	}
	var en map[string]string
	if err := json.Unmarshal(raw, &en); err != nil {
		t.Fatal(err)
	}
	for k, v := range en {
		if !strings.HasPrefix(k, "appointments.") {
			continue // another package's namespace (calendar: appt.cal.)
		}
		if strings.TrimSpace(v) == "" {
			t.Errorf("bad entry %q", k)
		}
	}
	h, repo := newHandler(t)
	// populate so every conditional branch of every view is drawn
	_ = repo.SaveResource(ctx, store.ResourceRecord{Resource: engine.Resource{ID: "r1", Kind: "staff", Active: true}, DisplayName: "Sam"})
	_ = repo.SetWorkingHours(ctx, "r1", map[time.Weekday][]engine.Window{time.Monday: {{StartMin: 540, EndMin: 600}}})
	_, _ = repo.AddTimeOff(ctx, "r1", time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC), "away")
	_ = repo.SaveService(ctx, engine.Service{SKU: "cut", DurationMin: 30, All: true, Active: true})
	used := map[string]bool{}
	filled := h
	for _, pass := range []string{"filled", "empty"} {
		if pass == "empty" {
			h, _ = newHandler(t)
		}
		for _, v := range []string{admin.ViewResources, admin.ViewServices, admin.ViewHours, admin.ViewTimeOff, admin.ViewSettings} {
			d := view(t, h, v, "")
			b, _ := json.Marshal(d)
			var g any
			_ = json.Unmarshal(b, &g)
			collectKeys(g, used)
		}
	}
	// hours with no windows
	_ = repo.SaveResource(ctx, store.ResourceRecord{Resource: engine.Resource{ID: "r2", Kind: "chair", Active: true}, DisplayName: "Chair"})
	b, _ := json.Marshal(view(t, filled, admin.ViewHours, "r2"))
	var g any
	_ = json.Unmarshal(b, &g)
	collectKeys(g, used)
	for _, k := range admin.ErrorKeys() {
		used[k] = true
	}
	used[admin.KeyOverrideReasonHint] = true
	used["appointments.admin.saved"] = true
	// keys the manifest names (entry label, retention reason); scripts/validate.sh checks they exist
	used["appointments.nav.today"] = true
	used["appointments.retention.reason"] = true
	var missing, unused []string
	for k := range used {
		if _, ok := en[k]; !ok {
			missing = append(missing, k)
		}
	}
	for k := range en {
		if strings.HasPrefix(k, "appt.") {
			continue
		}
		if !used[k] {
			unused = append(unused, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(unused)
	if len(missing) > 0 {
		t.Errorf("keys used but missing from locales/en.json: %v", missing)
	}
	if len(unused) > 0 {
		t.Errorf("keys in locales/en.json never used: %v", unused)
	}
	if en[admin.KeyOverrideReasonHint] != store.OverrideReasonHint {
		t.Errorf("override hint text %q", en[admin.KeyOverrideReasonHint])
	}
}

// Only en.json exists: translations come from Weblate, not from this PR.
func TestLocale_EnglishOnly(t *testing.T) {
	es, err := os.ReadDir("../../locales")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		if n := e.Name(); n != "en.json" && n != "locales.go" && n != "locales_test.go" {
			t.Fatalf("unexpected locale file %q: only en.json is allowed", n)
		}
	}
}

// ---- views --------------------------------------------------------------------------

func TestViews_EmptyShop(t *testing.T) {
	h, _ := newHandler(t)
	for _, v := range []string{admin.ViewResources, admin.ViewServices, admin.ViewHours, admin.ViewTimeOff, admin.ViewSettings} {
		d := view(t, h, v, "")
		if d.Title == nil || d.Title.Key == "" {
			t.Errorf("%s: no title", v)
		}
	}
	r, _ := h.HandleView(ctx, admin.ViewRequest{View: "nope"})
	wantNotice(t, *r.Document, "error", "appointments.admin.error.unknown")
}

func TestResources_SaveAndList(t *testing.T) {
	h, repo := newHandler(t)
	d := act(t, h, admin.ViewResources, admin.ActSaveResource, map[string]any{"resource": "_new", "name": "Sam", "kind": "staff", "status": "active", "colour": "#abc", "logbook_staff_ref": "lb-7"})
	wantNotice(t, d, "info", "appointments.admin.saved")
	rs, _ := repo.Resources(ctx)
	if len(rs) != 1 || rs[0].DisplayName != "Sam" || rs[0].LogbookStaffRef != "lb-7" || !rs[0].Active {
		t.Fatalf("%+v", rs)
	}
	// edit the same one, make it inactive
	act(t, h, admin.ViewResources, admin.ActSaveResource, map[string]any{"resource": rs[0].ID, "name": "Samira", "kind": "staff", "status": "inactive"})
	rs, _ = repo.Resources(ctx)
	if len(rs) != 1 || rs[0].DisplayName != "Samira" || rs[0].Active {
		t.Fatalf("%+v", rs)
	}
	b, _ := json.Marshal(view(t, h, admin.ViewResources, ""))
	if !strings.Contains(string(b), `"Samira"`) || !strings.Contains(string(b), `"Inactive"`) {
		t.Fatalf("%s", b)
	}
	// errors become notices, nothing is half written
	wantNotice(t, act(t, h, admin.ViewResources, admin.ActSaveResource, map[string]any{"resource": "_new", "name": "", "kind": "staff"}), "error", "appointments.admin.error.invalid_input")
	wantNotice(t, act(t, h, admin.ViewResources, admin.ActSaveResource, map[string]any{"resource": "ghost", "name": "X", "kind": "staff"}), "error", "appointments.admin.error.not_found")
	if rs, _ = repo.Resources(ctx); len(rs) != 1 {
		t.Fatal("a refused save wrote a resource")
	}
}

func TestServices_SaveListPriceFromCatalogue(t *testing.T) {
	h, repo := newHandler(t)
	_ = repo.SaveResource(ctx, store.ResourceRecord{Resource: engine.Resource{ID: "id-r.1", Kind: "staff", Active: true}, DisplayName: "Sam"})
	_ = repo.SaveResource(ctx, store.ResourceRecord{Resource: engine.Resource{ID: "id-r.2", Kind: "staff", Active: true}, DisplayName: "Kim"})
	d := view(t, h, admin.ViewServices, "")
	// one toggle per resource, names valid
	var names []string
	for _, c := range d.Components {
		if f, ok := c.(admin.Form); ok {
			for _, fl := range f.Fields {
				if strings.HasPrefix(fl.Name, "res_") {
					names = append(names, fl.Name)
				}
			}
		}
	}
	if len(names) != 2 {
		t.Fatalf("%v", names)
	}
	wantNotice(t, act(t, h, admin.ViewServices, admin.ActSaveService, map[string]any{"sku": "cut", "duration_min": "45", "buffer_min": "5", "active": true, names[1]: true}), "info", "appointments.admin.saved")
	svcs, _ := repo.Services(ctx)
	if len(svcs) != 1 || svcs[0].DurationMin != 45 || svcs[0].BufferMin != 5 || svcs[0].All || len(svcs[0].ResourceIDs) != 1 || svcs[0].ResourceIDs[0] != "id-r.2" {
		t.Fatalf("%+v", svcs)
	}
	b, _ := json.Marshal(view(t, h, admin.ViewServices, ""))
	if !strings.Contains(string(b), `{"minor":2500,"currency":"GBP"}`) || !strings.Contains(string(b), "Kim") {
		t.Fatalf("%s", b)
	}
	// a service with no catalogue price shows an empty cell, not a made-up one
	act(t, h, admin.ViewServices, admin.ActSaveService, map[string]any{"sku": "dye", "duration_min": "90", "buffer_min": "0", "all_resources": true})
	b, _ = json.Marshal(view(t, h, admin.ViewServices, ""))
	if !strings.Contains(string(b), `"dye",90,0,"*","Inactive",null`) {
		t.Fatalf("%s", b)
	}
	for name, form := range map[string]map[string]any{
		"bad sku":   {"sku": "a b", "duration_min": "30", "buffer_min": "0"},
		"fraction":  {"sku": "x", "duration_min": "30.5", "buffer_min": "0"},
		"no number": {"sku": "x", "duration_min": "", "buffer_min": "0"},
		"zero":      {"sku": "x", "duration_min": "0", "buffer_min": "0"},
		"negative":  {"sku": "x", "duration_min": "30", "buffer_min": "-5"},
	} {
		wantNotice(t, act(t, h, admin.ViewServices, admin.ActSaveService, form), "error", "appointments.admin.error.invalid_input")
		_ = name
	}
}

func TestHours_AddShowClear(t *testing.T) {
	h, repo := newHandler(t)
	_ = repo.SaveResource(ctx, store.ResourceRecord{Resource: engine.Resource{ID: "r1", Kind: "staff", Active: true}, DisplayName: "Sam"})
	_ = repo.SaveResource(ctx, store.ResourceRecord{Resource: engine.Resource{ID: "r2", Kind: "chair", Active: true}, DisplayName: "Chair"})
	wantNotice(t, act(t, h, admin.ViewHours, admin.ActAddHours, map[string]any{"resource": "r1", "weekday": "1", "start": "09:00", "end": "12:30"}), "info", "appointments.admin.saved")
	act(t, h, admin.ViewHours, admin.ActAddHours, map[string]any{"resource": "r1", "weekday": "1", "start": "13:30", "end": "17:00"})
	rec, _ := repo.Resource(ctx, "r1")
	if got := rec.WorkingHours[time.Monday]; len(got) != 2 || got[0] != (engine.Window{StartMin: 540, EndMin: 750}) || got[1] != (engine.Window{StartMin: 810, EndMin: 1020}) {
		t.Fatalf("%+v", got)
	}
	// 24:00 closes the day
	act(t, h, admin.ViewHours, admin.ActAddHours, map[string]any{"resource": "r1", "weekday": "0", "start": "10:00", "end": "24:00"})
	// show other resource
	d := act(t, h, admin.ViewHours, admin.ActShowHours, map[string]any{"resource": "r2"})
	b, _ := json.Marshal(d)
	if strings.Contains(string(b), `["09:00"`) || !strings.Contains(string(b), `"literal":"Chair"`) {
		t.Fatalf("%s", b)
	}
	b, _ = json.Marshal(view(t, h, admin.ViewHours, "r1"))
	if !strings.Contains(string(b), `["09:00","12:30"]`) || !strings.Contains(string(b), `["10:00","24:00"]`) {
		t.Fatalf("%s", b)
	}
	// Monday is listed before Sunday
	if strings.Index(string(b), "weekday.1") > strings.Index(string(b), "weekday.0") {
		t.Fatal("Monday first")
	}
	d = act(t, h, admin.ViewHours, admin.ActClearHours, map[string]any{"resource": "r1", "weekday": "1"})
	rec, _ = repo.Resource(ctx, "r1")
	if len(rec.WorkingHours[time.Monday]) != 0 || len(rec.WorkingHours[time.Sunday]) != 1 {
		t.Fatalf("%+v", rec.WorkingHours)
	}
	for name, f := range map[string]map[string]any{
		"overlap":    {"resource": "r1", "weekday": "0", "start": "11:00", "end": "12:00"},
		"reverse":    {"resource": "r1", "weekday": "2", "start": "12:00", "end": "11:00"},
		"format":     {"resource": "r1", "weekday": "2", "start": "9am", "end": "11:00"},
		"minutes":    {"resource": "r1", "weekday": "2", "start": "09:60", "end": "11:00"},
		"past 24:00": {"resource": "r1", "weekday": "2", "start": "09:00", "end": "24:30"},
		"weekday":    {"resource": "r1", "weekday": "7", "start": "09:00", "end": "10:00"},
	} {
		wantNotice(t, act(t, h, admin.ViewHours, admin.ActAddHours, f), "error", "appointments.admin.error.invalid_input")
		_ = name
	}
	wantNotice(t, act(t, h, admin.ViewHours, admin.ActAddHours, map[string]any{"resource": "ghost", "weekday": "1", "start": "09:00", "end": "10:00"}), "error", "appointments.admin.error.not_found")
}

func TestTimeOff_FixedReasonsOnly(t *testing.T) {
	h, repo := newHandler(t)
	_ = repo.SaveResource(ctx, store.ResourceRecord{Resource: engine.Resource{ID: "r1", Kind: "staff", Active: true}, DisplayName: "Sam"})
	d := view(t, h, admin.ViewTimeOff, "")
	for _, c := range d.Components {
		if f, ok := c.(admin.Form); ok && f.Action == admin.ActAddTimeOff {
			for _, fl := range f.Fields {
				if fl.Name == "reason" {
					var vals []string
					for _, o := range fl.Options {
						vals = append(vals, o.Value)
						if o.Label.Literal != "" {
							t.Errorf("reason label must be a key, not free text")
						}
					}
					if fmt.Sprint(vals) != "[holiday away other]" {
						t.Fatalf("%v", vals)
					}
				}
				if fl.Kind == "text" && fl.Name == "reason" {
					t.Fatal("reason is not free text")
				}
			}
		}
	}
	wantNotice(t, act(t, h, admin.ViewTimeOff, admin.ActAddTimeOff, map[string]any{"resource": "r1", "from": "2026-03-01 00:00", "to": "2026-03-03 00:00", "reason": "holiday"}), "info", "appointments.admin.saved")
	tos, _ := repo.TimeOff(ctx, "r1")
	if len(tos) != 1 || tos[0].Reason != "holiday" || !tos[0].From.Equal(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("%+v", tos)
	}
	// BST: 2026-07-01 09:00 shop time is 08:00 UTC
	act(t, h, admin.ViewTimeOff, admin.ActAddTimeOff, map[string]any{"resource": "r1", "from": "2026-07-01 09:00", "to": "2026-07-01 17:00", "reason": "away"})
	tos, _ = repo.TimeOff(ctx, "r1")
	if !tos[1].From.Equal(time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("zone: %v", tos[1].From)
	}
	b, _ := json.Marshal(view(t, h, admin.ViewTimeOff, ""))
	if !strings.Contains(string(b), "2026-07-01 09:00") {
		t.Fatalf("shown in the shop zone: %s", b)
	}
	for _, reason := range []string{"sick", "chemotherapy", "maternity", "Holiday"} {
		wantNotice(t, act(t, h, admin.ViewTimeOff, admin.ActAddTimeOff, map[string]any{"resource": "r1", "from": "2026-03-01 00:00", "to": "2026-03-03 00:00", "reason": reason}), "error", "appointments.admin.error.time_off_reason")
	}
	for _, f := range []map[string]any{
		{"resource": "r1", "from": "01/03/2026", "to": "2026-03-03 00:00"},
		{"resource": "r1", "from": "2026-03-03 00:00", "to": "2026-03-01 00:00"},
	} {
		wantNotice(t, act(t, h, admin.ViewTimeOff, admin.ActAddTimeOff, f), "error", "appointments.admin.error.invalid_input")
	}
	if tos, _ = repo.TimeOff(ctx, ""); len(tos) != 2 {
		t.Fatalf("refused entries were stored: %+v", tos)
	}
	wantNotice(t, act(t, h, admin.ViewTimeOff, admin.ActRemoveTimeOff, map[string]any{"entry": tos[0].ID}), "info", "appointments.admin.saved")
	if tos, _ = repo.TimeOff(ctx, ""); len(tos) != 1 {
		t.Fatal(tos)
	}
}

func TestSettings_SaveAndRanges(t *testing.T) {
	h, repo := newHandler(t)
	form := map[string]any{"slot_step_min": "30", "no_show_grace_min": "10", "late_cancel_hours": "0", "overrun_min": "5",
		"allow_double_booking": true, "retention_days": "180", "shop_zone": "Europe/Dublin", "currency": "eur"}
	wantNotice(t, act(t, h, admin.ViewSettings, admin.ActSaveSettings, form), "info", "appointments.admin.saved")
	s, _ := repo.Settings(ctx)
	if s.SlotStepMin != 30 || s.NoShowGraceMin != 10 || s.LateCancelHours != 0 || !s.AllowDoubleBooking || s.RetentionDays != 180 || s.ShopZone != "Europe/Dublin" || s.Currency != "EUR" {
		t.Fatalf("%+v", s)
	}
	b, _ := json.Marshal(view(t, h, admin.ViewSettings, ""))
	if !strings.Contains(string(b), `"value":"180"`) || !strings.Contains(string(b), `"value":"30"`) {
		t.Fatalf("form prefilled from stored settings: %s", b)
	}
	for _, c := range []struct {
		field, val, key string
	}{
		{"retention_days", "731", "retention_range"}, {"retention_days", "0", "retention_range"}, {"slot_step_min", "7", "invalid_policy"},
		{"no_show_grace_min", "-1", "invalid_policy"}, {"shop_zone", "Mars/Base", "invalid_input"}, {"currency", "POUND", "invalid_input"},
		{"retention_days", "abc", "invalid_input"},
	} {
		f := map[string]any{}
		for k, v := range form {
			f[k] = v
		}
		f[c.field] = c.val
		wantNotice(t, act(t, h, admin.ViewSettings, admin.ActSaveSettings, f), "error", "appointments.admin.error."+c.key)
	}
	if s2, _ := repo.Settings(ctx); s2 != s {
		t.Fatal("a refused save changed the settings")
	}
}

func TestAction_InvalidUnknownAndForbidden(t *testing.T) {
	h, repo := newHandler(t)
	r, _ := h.HandleAction(ctx, admin.ActionRequest{View: admin.ViewSettings, Action: admin.ActSaveSettings, Invalid: []string{"retention_days"}})
	wantNotice(t, *r.Document, "error", "appointments.admin.error.form")
	r, _ = h.HandleAction(ctx, admin.ActionRequest{View: admin.ViewSettings, Action: "drop_everything"})
	wantNotice(t, *r.Document, "error", "appointments.admin.error.unknown")
	r, _ = h.HandleAction(ctx, admin.ActionRequest{View: "nope", Action: admin.ActSaveSettings})
	wantNotice(t, *r.Document, "error", "appointments.admin.error.unknown")
	h.CanAdmin = func(op *admin.Operator) bool { return op != nil && op.Role == "manager" }
	r, _ = h.HandleAction(ctx, admin.ActionRequest{View: admin.ViewResources, Action: admin.ActSaveResource, Operator: &admin.Operator{Role: "cashier"},
		Form: map[string]any{"name": "X", "kind": "staff"}})
	wantNotice(t, *r.Document, "error", "appointments.admin.error.forbidden")
	if rs, _ := repo.Resources(ctx); len(rs) != 0 {
		t.Fatal("a forbidden action wrote data")
	}
	rv, _ := h.HandleView(ctx, admin.ViewRequest{View: admin.ViewResources, Operator: &admin.Operator{Role: "cashier"}})
	wantNotice(t, *rv.Document, "error", "appointments.admin.error.forbidden")
	rv, _ = h.HandleView(ctx, admin.ViewRequest{View: admin.ViewResources, Operator: &admin.Operator{Role: "manager"}})
	if _, bad := firstNotice(*rv.Document); bad {
		t.Fatal("manager refused")
	}
}

func TestResponseJSONShape(t *testing.T) {
	h, _ := newHandler(t)
	r, _ := h.HandleView(ctx, admin.ViewRequest{View: admin.ViewSettings})
	b, err := r.Document.JSON()
	if err != nil || !strings.HasPrefix(string(b), `{"document":{"version":1,"title":{"key":"appointments.admin.settings.title"}`) {
		t.Fatalf("%v %s", err, b)
	}
	// round trip through strict decoding of the request payloads
	var req admin.ActionRequest
	dec := json.NewDecoder(strings.NewReader(`{"view":"appointments.admin.settings","action":"save_settings","form":{"retention_days":"90","allow_double_booking":false},"invalid":[],"upload_handles":[],"locale":"en"}`))
	if err := dec.Decode(&req); err != nil || req.Form["retention_days"] != "90" {
		t.Fatalf("%v %+v", err, req)
	}
}

func TestErrorKey(t *testing.T) {
	if admin.ErrorKey(fmt.Errorf("wrapped: %w", store.ErrNameChars)) != "appointments.admin.error.name_chars" {
		t.Fatal("wrapped")
	}
	if admin.ErrorKey(fmt.Errorf("disk")) != "appointments.admin.error.generic" {
		t.Fatal("generic")
	}
}

// N2: with CanAdmin unset the handler fails closed for a non-manager role.
func TestAdmin_DefaultCheckFailsClosed(t *testing.T) {
	h, repo := newHandler(t) // CanAdmin is nil
	for _, role := range []string{"cashier", "", "viewer"} {
		r, _ := h.HandleAction(ctx, admin.ActionRequest{View: admin.ViewResources, Action: admin.ActSaveResource, Operator: &admin.Operator{Role: role},
			Form: map[string]any{"name": "X", "kind": "staff"}})
		wantNotice(t, *r.Document, "error", "appointments.admin.error.forbidden")
		rv, _ := h.HandleView(ctx, admin.ViewRequest{View: admin.ViewSettings, Operator: &admin.Operator{Role: role}})
		wantNotice(t, *rv.Document, "error", "appointments.admin.error.forbidden")
	}
	if rs, _ := repo.Resources(ctx); len(rs) != 0 {
		t.Fatal("a forbidden action wrote data")
	}
	for _, op := range []*admin.Operator{nil, {Role: "manager"}, {Role: "admin"}, {Role: "owner"}} {
		rv, _ := h.HandleView(ctx, admin.ViewRequest{View: admin.ViewSettings, Operator: op})
		if _, bad := firstNotice(*rv.Document); bad {
			t.Fatalf("refused %+v", op)
		}
	}
}

// N10 / N12: the texts claim only what is true until a purge trigger and a
// completion path exist.
func TestLocale_NoClaimsWithoutAPath(t *testing.T) {
	raw, err := os.ReadFile("../../locales/en.json")
	if err != nil {
		t.Fatal(err)
	}
	var en map[string]string
	if err := json.Unmarshal(raw, &en); err != nil {
		t.Fatal(err)
	}
	if v := en["appointments.admin.settings.privacy"]; strings.Contains(v, "automatically") {
		t.Fatalf("privacy text claims automatic blanking: %q", v)
	}
	if v := en["appt.cal.complete_at_till"]; strings.Contains(v, "at the till") {
		t.Fatalf("completion text promises a path that does not exist: %q", v)
	}
}

// ---- review N3 / N14 --------------------------------------------------------------

func fieldValue(t *testing.T, d admin.Document, formAction, field string) any {
	t.Helper()
	b, _ := json.Marshal(d)
	var g struct {
		Components []struct {
			Type   string `json:"type"`
			Action string `json:"action"`
			Fields []struct {
				Name  string `json:"name"`
				Value any    `json:"value"`
			} `json:"fields"`
		} `json:"components"`
	}
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	for _, c := range g.Components {
		if c.Type == "form" && c.Action == formAction {
			for _, f := range c.Fields {
				if f.Name == field {
					return f.Value
				}
			}
		}
	}
	t.Fatalf("no field %s in form %s", field, formAction)
	return nil
}

func TestResources_EditFormIsPrefilledAndKeepsLogbookLink_N3(t *testing.T) {
	h, repo := newHandler(t)
	_ = repo.SaveResource(ctx, store.ResourceRecord{Resource: engine.Resource{ID: "r1", Kind: "chair", Active: false},
		DisplayName: "Sam", Colour: "#abc", LogbookStaffRef: "lb-7"})
	d := view(t, h, admin.ViewResources, "r1")
	for f, want := range map[string]any{"resource": "r1", "name": "Sam", "kind": "chair", "colour": "#abc", "status": "inactive", "logbook_staff_ref": "lb-7"} {
		if got := fieldValue(t, d, admin.ActSaveResource, f); got != want {
			t.Errorf("field %s = %v, want %v", f, got, want)
		}
	}
	// the "show" action reloads the form for the chosen resource
	d = act(t, h, admin.ViewResources, admin.ActShowResource, map[string]any{"resource": "r1"})
	if fieldValue(t, d, admin.ActSaveResource, "logbook_staff_ref") != "lb-7" {
		t.Error("show_resource did not pre-fill")
	}
	// a save that leaves the optional fields out keeps the stored values
	act(t, h, admin.ViewResources, admin.ActSaveResource, map[string]any{"resource": "r1", "name": "Samira"})
	rs, _ := repo.Resources(ctx)
	if rs[0].DisplayName != "Samira" || rs[0].LogbookStaffRef != "lb-7" || rs[0].Colour != "#abc" || rs[0].Kind != "chair" || rs[0].Active {
		t.Errorf("a save wiped stored fields: %+v", rs[0])
	}
}

func TestTableCellsComeFromTheBundle_N14(t *testing.T) {
	h, repo := newHandler(t)
	_ = repo.SaveResource(ctx, store.ResourceRecord{Resource: engine.Resource{ID: "r1", Kind: "staff", Active: true}, DisplayName: "Sam"})
	_, _ = repo.AddTimeOff(ctx, "r1", time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC), "holiday")
	_ = repo.SaveService(ctx, engine.Service{SKU: "cut", DurationMin: 30, All: true, Active: false})
	for _, v := range []string{admin.ViewResources, admin.ViewServices, admin.ViewTimeOff} {
		b, _ := json.Marshal(view(t, h, v, ""))
		var g struct {
			Components []struct {
				Type string  `json:"type"`
				Rows [][]any `json:"rows"`
			} `json:"components"`
		}
		_ = json.Unmarshal(b, &g)
		rows := 0
		for _, c := range g.Components {
			for _, r := range c.Rows {
				rows++
				for _, cell := range r {
					switch cell {
					case "active", "inactive", "staff", "chair", "holiday", "away", "other":
						t.Errorf("%s: raw English literal %q in a table cell", v, cell)
					}
				}
			}
		}
		if rows == 0 {
			t.Errorf("%s: no table rows drawn", v)
		}
	}
}
