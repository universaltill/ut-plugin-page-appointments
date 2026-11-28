package calendar

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
	"github.com/universaltill/ut-plugin-page-appointments/src/snapshot"
)

// Replica snapshot freshness (design 7207 section 5).
const (
	ReplicaStaleAfter   = 2 * time.Hour
	ReplicaExpiredAfter = 12 * time.Hour
)

// ReplicaAsk is a view or action on a replica till.
type ReplicaAsk struct {
	Action string // "" for a view; "day_YYYYMMDD" and "today" navigate, anything else is ignored
	Params map[string]string
	Locale string
	Now    time.Time
}

// ReplicaDocument draws the read-only day calendar from the main till's
// snapshot. It writes nothing and offers no booking action: the only buttons
// move between days of the held window. found is false when the till has no
// snapshot (never paired, or the pull has not run), which shows the empty
// state. A snapshot older than ReplicaExpiredAfter lists no appointments (a
// day-old list reads as today's), one older than ReplicaStaleAfter says so.
func ReplicaDocument(p snapshot.Payload, found bool, a ReplicaAsk) *Document {
	loc := a.Locale
	if loc == "" {
		loc = "en"
	}
	t := tr{locale: loc}
	doc := &Document{Version: 1, Title: Key("appt.cal.title")}
	if !found {
		doc.Components = append(doc.Components, emptyState(Key("appt.replica.none"), Key("appt.replica.none_body")))
		return doc
	}
	asOf, from, to, err := p.Times()
	zone, zerr := time.LoadLocation(p.Zone)
	if err != nil || zerr != nil {
		doc.Components = append(doc.Components, emptyState(Key("appt.replica.none"), Key("appt.replica.none_body")))
		return doc
	}
	age := a.Now.Sub(asOf)
	stamp := asOf.In(zone).Format("15:04")
	if age > ReplicaExpiredAfter {
		stamp = asOf.In(zone).Format("2006-01-02 15:04")
		doc.Components = append(doc.Components, notice("warn", t.lit("appt.replica.expired", stamp)))
		return doc
	}
	doc.Components = append(doc.Components, para(t.lit("appt.replica.synced", stamp)))
	if age > ReplicaStaleAfter || age < -ReplicaStaleAfter {
		doc.Components = append(doc.Components, notice("warn", Key("appt.replica.stale")))
	}
	doc.Components = append(doc.Components, notice("info", Key("appt.replica.readonly")))

	// The day shown: inside the held window only.
	first := civil(from, zone)
	last := civil(to.Add(-time.Second), zone)
	day := civil(a.Now, zone)
	if d, ok := parseISODate(a.Params["date"]); ok {
		day = d
	}
	if strings.HasPrefix(a.Action, "day_") {
		if d, ok := parseCompactDate(strings.TrimPrefix(a.Action, "day_")); ok {
			day = d
		}
	}
	if before(day, first) {
		day = first
	}
	if before(last, day) {
		day = last
	}
	nav := []Component{heading(Lit(weekdayOf(day, t) + " " + isoDate(day)))}
	if before(first, day) {
		nav = append(nav, button("day_"+compactDate(addDays(day, -1)), Key("appt.cal.prev_day"), "secondary"))
	}
	if before(day, last) {
		nav = append(nav, button("day_"+compactDate(addDays(day, 1)), Key("appt.cal.next_day"), "secondary"))
	}
	doc.Components = append(doc.Components, nav...)

	res := append([]snapshot.Resource(nil), p.Resources...)
	sort.SliceStable(res, func(i, j int) bool {
		if res[i].Name != res[j].Name {
			return res[i].Name < res[j].Name
		}
		return res[i].ID < res[j].ID
	})
	for _, r := range res {
		var rows [][]any
		for _, ap := range p.Appointments {
			if ap.R != r.ID {
				continue
			}
			s, e1 := time.Parse(time.RFC3339, ap.S)
			e, e2 := time.Parse(time.RFC3339, ap.E)
			if e1 != nil || e2 != nil || civil(s, zone) != day { // rows outside the day (or window) are ignored
				continue
			}
			state := t.s("appt.cal.state." + ap.St)
			if ap.W {
				state += " (" + t.s("appt.cal.tag.walkin") + ")"
			}
			name := ap.N
			if name == "" {
				name = t.s("appt.cal.walkin_client")
			}
			rows = append(rows, []any{s.In(zone).Format("15:04") + "-" + e.In(zone).Format("15:04"), name, strings.Join(ap.Svc, ", "), state})
		}
		doc.Components = append(doc.Components, heading(Lit(r.Name)))
		if len(rows) == 0 {
			doc.Components = append(doc.Components, emptyState(Key("appt.cal.day_empty"), Key("appt.replica.day_empty_body")))
			continue
		}
		doc.Components = append(doc.Components, table(
			[]Component{Key("appt.cal.col_time"), Key("appt.cal.col_client"), Key("appt.cal.col_service"), Key("appt.cal.col_status")},
			[]string{"text", "text", "text", "text"}, rows))
	}
	if p.Truncated {
		doc.Components = append(doc.Components, notice("warn", Key("appt.replica.truncated")))
	}
	return doc
}

func civil(t time.Time, zone *time.Location) engine.Date {
	y, m, d := t.In(zone).Date()
	return engine.Date{Y: y, M: m, D: d}
}

func weekdayOf(d engine.Date, t tr) string {
	return t.s(fmt.Sprintf("appt.cal.wd.%d", int(time.Date(d.Y, d.M, d.D, 12, 0, 0, 0, time.UTC).Weekday())))
}
