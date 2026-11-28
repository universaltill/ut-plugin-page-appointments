// Package memdb is a statement-level, SQLite-free test double of store.DB. It
// understands exactly the statements in store/sql.go (matched by their exact
// text) and keeps the tables in memory. It enforces primary keys and the
// "append-only / booking data immutable" rules the real migrations enforce
// with triggers only as far as the statements allow (there is no statement
// that edits booking data). store's contract tests run the same suite against
// this double and against real SQLite when the sqlite3 command is installed,
// so the two cannot drift silently.
package memdb

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/universaltill/ut-plugin-page-appointments/src/store"
)

type rec = map[string]any

// DB is the in-memory database. The zero value is not usable; use New.
type DB struct {
	mu     sync.Mutex
	tables map[string][]rec
	// FailExec, when set, is called before every Exec; a non-nil error is
	// returned instead of running the statement (crash injection in tests).
	FailExec func(q string, args []any) error
}

// New returns an empty database.
func New() *DB { return &DB{tables: map[string][]rec{}} }

func asInt(v any) int64 {
	switch x := v.(type) {
	case int:
		return int64(x)
	case int64:
		return x
	case bool:
		if x {
			return 1
		}
		return 0
	}
	panic(fmt.Sprintf("memdb: not an integer: %T", v))
}

func asStr(v any) string {
	s, ok := v.(string)
	if !ok {
		panic(fmt.Sprintf("memdb: not a string: %T", v))
	}
	return s
}

func (d *DB) find(table string, match func(rec) bool) int {
	for i, r := range d.tables[table] {
		if match(r) {
			return i
		}
	}
	return -1
}

func (d *DB) del(table string, match func(rec) bool) int64 {
	var keep []rec
	var n int64
	for _, r := range d.tables[table] {
		if match(r) {
			n++
		} else {
			keep = append(keep, r)
		}
	}
	d.tables[table] = keep
	return n
}

func (d *DB) insert(table string, r rec, key ...string) error {
	if len(key) > 0 {
		if d.find(table, func(o rec) bool {
			for _, k := range key {
				if o[k] != r[k] {
					return false
				}
			}
			return true
		}) >= 0 {
			return fmt.Errorf("memdb: UNIQUE constraint failed: %s", table)
		}
	}
	d.tables[table] = append(d.tables[table], r)
	return nil
}

// Exec implements store.DB.
func (d *DB) Exec(_ context.Context, q string, a ...any) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.FailExec != nil {
		if err := d.FailExec(q, a); err != nil {
			return 0, err
		}
	}
	switch q {
	case store.SQLUpsertResource:
		r := rec{"id": a[0], "kind": a[1], "display_name": a[2], "colour": a[3], "status": a[4], "logbook_staff_ref": a[5]}
		if i := d.find("resources", func(o rec) bool { return o["id"] == a[0] }); i >= 0 {
			d.tables["resources"][i] = r
		} else {
			d.tables["resources"] = append(d.tables["resources"], r)
		}
		return 1, nil
	case store.SQLDeleteWorkingHours:
		return d.del("working_hours", func(o rec) bool { return o["resource_id"] == a[0] }), nil
	case store.SQLInsertWorkingHour:
		return 1, d.insert("working_hours", rec{"resource_id": a[0], "weekday": asInt(a[1]), "start_min": asInt(a[2]), "end_min": asInt(a[3])}, "resource_id", "weekday", "start_min")
	case store.SQLInsertTimeOff:
		return 1, d.insert("time_off", rec{"id": a[0], "resource_id": a[1], "from_utc": asInt(a[2]), "to_utc": asInt(a[3]), "reason": a[4]}, "id")
	case store.SQLDeleteTimeOff:
		return d.del("time_off", func(o rec) bool { return o["id"] == a[0] }), nil
	case store.SQLUpsertService:
		r := rec{"sku": a[0], "duration_min": asInt(a[1]), "buffer_min": asInt(a[2]), "all_resources": asInt(a[3]), "active": asInt(a[4])}
		if i := d.find("services", func(o rec) bool { return o["sku"] == a[0] }); i >= 0 {
			d.tables["services"][i] = r
		} else {
			d.tables["services"] = append(d.tables["services"], r)
		}
		return 1, nil
	case store.SQLDeleteServiceResource:
		return d.del("service_resources", func(o rec) bool { return o["sku"] == a[0] }), nil
	case store.SQLInsertServiceResource:
		return 1, d.insert("service_resources", rec{"sku": a[0], "resource_id": a[1]}, "sku", "resource_id")
	case store.SQLUpsertSetting:
		r := rec{"key": a[0], "value": a[1]}
		if i := d.find("settings", func(o rec) bool { return o["key"] == a[0] }); i >= 0 {
			d.tables["settings"][i] = r
		} else {
			d.tables["settings"] = append(d.tables["settings"], r)
		}
		return 1, nil
	case store.SQLInsertLine: // INSERT OR IGNORE
		r := rec{"appointment_id": a[0], "line_no": asInt(a[1]), "sku": a[2], "name_snapshot": a[3], "qty": asInt(a[4]),
			"amount_minor": asInt(a[5]), "duration_min": asInt(a[6]), "buffer_min": asInt(a[7])}
		if d.find("appointment_lines", func(o rec) bool { return o["appointment_id"] == a[0] && o["line_no"] == r["line_no"] }) >= 0 {
			return 0, nil
		}
		d.tables["appointment_lines"] = append(d.tables["appointment_lines"], r)
		return 1, nil
	case store.SQLInsertAppointment:
		if a[13] != "" && d.find("appointments", func(o rec) bool { return o["supersedes_id"] == a[13] }) >= 0 {
			return 0, fmt.Errorf("memdb: UNIQUE constraint failed: appointments.supersedes_id")
		}
		return 1, d.insert("appointments", rec{"id": a[0], "origin_till_id": a[1], "source": a[2], "resource_id": a[3],
			"start_utc": asInt(a[4]), "end_utc": asInt(a[5]), "buffer_min": asInt(a[6]), "walk_in": asInt(a[7]), "state": a[8],
			"client_first_name": a[9], "currency": a[10], "sale_ref": a[11], "late_cancel": asInt(a[12]),
			"supersedes_id": a[13], "created_at": asInt(a[14])}, "id")
	case store.SQLSetProjection:
		i := d.find("appointments", func(o rec) bool { return o["id"] == a[0] && o["state"] == a[4] })
		if i < 0 {
			return 0, nil
		}
		d.tables["appointments"][i]["state"] = a[1]
		d.tables["appointments"][i]["late_cancel"] = asInt(a[2])
		d.tables["appointments"][i]["sale_ref"] = a[3]
		return 1, nil
	case store.SQLInsertEvent:
		return 1, d.insert("appointment_events", rec{"id": a[0], "appointment_id": a[1], "at": asInt(a[2]), "actor_user_id": a[3],
			"kind": a[4], "from_state": a[5], "to_state": a[6], "reason": a[7], "conflicting_ids": a[8]}, "id")
	case store.SQLInsertQueue:
		return 1, d.insert("walkin_queue", rec{"id": a[0], "seq": asInt(a[1]), "arrived_utc": asInt(a[2]), "first_name": a[3],
			"service_sku": a[4], "duration_min": asInt(a[5]), "buffer_min": asInt(a[6]), "resource_ids": a[7]}, "id")
	case store.SQLDeleteQueue:
		return d.del("walkin_queue", func(o rec) bool { return o["id"] == a[0] }), nil
	case store.SQLPurgeQueue:
		return d.del("walkin_queue", func(o rec) bool {
			return o["arrived_utc"].(int64) < asInt(a[0]) || d.find("appointments", func(x rec) bool { return x["id"] == o["id"] }) >= 0
		}), nil
	case store.SQLInsertOutbox:
		if d.find("completion_outbox", func(o rec) bool { return o["appointment_id"] == a[0] }) >= 0 {
			return 0, nil
		}
		return 1, d.insert("completion_outbox", rec{"appointment_id": a[0], "sale_ref": a[1], "payload": a[2], "published": int64(0)})
	case store.SQLMarkPublished:
		var n int64
		for _, o := range d.tables["completion_outbox"] {
			if o["appointment_id"] == a[0] && o["published"] != int64(1) {
				o["published"] = int64(1)
				n++
			}
		}
		return n, nil
	case store.SQLBlankName:
		i := d.find("appointments", func(o rec) bool { return o["id"] == a[0] && o["client_first_name"] != "" })
		if i < 0 {
			return 0, nil
		}
		d.tables["appointments"][i]["client_first_name"] = ""
		return 1, nil
	}
	return 0, fmt.Errorf("memdb: unknown statement %q", q)
}

func sorted(rows []rec, less func(a, b rec) bool) []rec {
	out := append([]rec(nil), rows...)
	sort.SliceStable(out, func(i, j int) bool { return less(out[i], out[j]) })
	return out
}

func sortKeys(keys ...string) func(a, b rec) bool {
	return func(a, b rec) bool {
		for _, k := range keys {
			switch x := a[k].(type) {
			case string:
				if y := b[k].(string); x != y {
					return x < y
				}
			case int64:
				if y := b[k].(int64); x != y {
					return x < y
				}
			}
		}
		return false
	}
}

func project(rows []rec, cols ...string) []rec {
	out := make([]rec, len(rows))
	for i, r := range rows {
		o := rec{}
		for _, c := range cols {
			o[c] = r[c]
		}
		out[i] = o
	}
	return out
}

var apptCols = []string{"id", "origin_till_id", "source", "resource_id", "start_utc", "end_utc", "buffer_min", "walk_in", "state",
	"client_first_name", "currency", "sale_ref", "late_cancel", "supersedes_id", "created_at"}

func (d *DB) superseded(id string) bool {
	return d.find("appointments", func(o rec) bool { return o["supersedes_id"] == id }) >= 0
}

// Query implements store.DB.
func (d *DB) Query(_ context.Context, q string, a ...any) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var rows []rec
	switch q {
	case store.SQLListResources:
		rows = project(sorted(d.tables["resources"], sortKeys("id")), "id", "kind", "display_name", "colour", "status", "logbook_staff_ref")
	case store.SQLListWorkingHours:
		rows = project(sorted(d.tables["working_hours"], sortKeys("resource_id", "weekday", "start_min")), "resource_id", "weekday", "start_min", "end_min")
	case store.SQLListTimeOff:
		rows = project(sorted(d.tables["time_off"], sortKeys("resource_id", "from_utc", "id")), "id", "resource_id", "from_utc", "to_utc", "reason")
	case store.SQLListServices:
		rows = project(sorted(d.tables["services"], sortKeys("sku")), "sku", "duration_min", "buffer_min", "all_resources", "active")
	case store.SQLListServiceResources:
		rows = project(sorted(d.tables["service_resources"], sortKeys("sku", "resource_id")), "sku", "resource_id")
	case store.SQLListSettings:
		rows = project(sorted(d.tables["settings"], sortKeys("key")), "key", "value")
	case store.SQLGetAppointment:
		for _, r := range d.tables["appointments"] {
			if r["id"] == a[0] {
				rows = project([]rec{r}, apptCols...)
			}
		}
	case store.SQLListCurrent:
		from, to, res := asInt(a[0]), asInt(a[1]), asStr(a[2])
		var m []rec
		for _, r := range d.tables["appointments"] {
			if r["end_utc"].(int64)+r["buffer_min"].(int64)*60 > from && r["start_utc"].(int64) < to &&
				(res == "" || r["resource_id"] == res) && !d.superseded(r["id"].(string)) {
				m = append(m, r)
			}
		}
		rows = project(sorted(m, sortKeys("start_utc", "id")), apptCols...)
	case store.SQLHasSuperseder:
		for _, r := range d.tables["appointments"] {
			if r["supersedes_id"] == a[0] {
				rows = []rec{{"id": r["id"]}}
				break
			}
		}
	case store.SQLListLines:
		var m []rec
		for _, r := range d.tables["appointment_lines"] {
			if r["appointment_id"] == a[0] {
				m = append(m, r)
			}
		}
		rows = project(sorted(m, sortKeys("line_no")), "appointment_id", "line_no", "sku", "name_snapshot", "qty", "amount_minor", "duration_min", "buffer_min")
	case store.SQLPendingOutbox:
		var m []rec
		for _, o := range d.tables["completion_outbox"] {
			if o["published"] == int64(0) {
				m = append(m, o)
			}
		}
		rows = project(sorted(m, sortKeys("appointment_id")), "appointment_id", "sale_ref", "payload")
	case store.SQLUnoutboxed:
		var m []rec
		for _, r := range d.tables["appointments"] {
			if r["state"] == "completed" && r["sale_ref"] != "" && !d.superseded(r["id"].(string)) &&
				d.find("completion_outbox", func(o rec) bool { return o["appointment_id"] == r["id"] }) < 0 {
				m = append(m, r)
			}
		}
		rows = project(sorted(m, sortKeys("id")), "id")
	case store.SQLListQueue:
		since := asInt(a[0])
		var m []rec
		for _, r := range d.tables["walkin_queue"] {
			if r["arrived_utc"].(int64) >= since && d.find("appointments", func(x rec) bool { return x["id"] == r["id"] }) < 0 {
				m = append(m, r)
			}
		}
		rows = project(sorted(m, sortKeys("seq", "id")), "id", "seq", "arrived_utc", "first_name", "service_sku", "duration_min", "buffer_min", "resource_ids")
	case store.SQLListEvents:
		var m []rec
		for _, r := range d.tables["appointment_events"] {
			if r["appointment_id"] == a[0] {
				m = append(m, r)
			}
		}
		rows = project(m, "id", "appointment_id", "at", "actor_user_id", "kind", "from_state", "to_state", "reason", "conflicting_ids")
	case store.SQLNamedPage:
		after, limit := asStr(a[0]), int(asInt(a[1]))
		for _, r := range sorted(d.tables["appointments"], sortKeys("id")) {
			if len(rows) >= limit {
				break
			}
			if r["client_first_name"] != "" && r["id"].(string) > after {
				rows = append(rows, rec{"id": r["id"]})
			}
		}
	case store.SQLChainRow:
		for _, r := range d.tables["appointments"] {
			if r["id"] == a[0] {
				rows = []rec{{"id": r["id"], "supersedes_id": r["supersedes_id"], "state": r["state"], "end_utc": r["end_utc"],
					"named": asInt(r["client_first_name"] != "")}}
			}
		}
	case store.SQLStaleProjection:
		after, limit := asStr(a[0]), int(asInt(a[1]))
		for _, r := range sorted(d.tables["appointments"], sortKeys("id")) {
			if len(rows) >= limit {
				break
			}
			if r["id"].(string) <= after {
				continue
			}
			last := ""
			for _, e := range d.tables["appointment_events"] { // insertion order = rowid
				if e["appointment_id"] == r["id"] {
					last = e["to_state"].(string)
				}
			}
			if r["state"] != last {
				rows = append(rows, rec{"id": r["id"], "state": r["state"], "last_state": last})
			}
		}
	default:
		return nil, fmt.Errorf("memdb: unknown statement %q", q)
	}
	if rows == nil {
		rows = []rec{}
	}
	return json.Marshal(rows)
}

// Dump returns every table as one JSON document (tables and keys sorted), for
// the PII test: it contains every stored byte of application data.
func (d *DB) Dump() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	b, _ := json.Marshal(d.tables)
	return string(b)
}
