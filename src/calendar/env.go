package calendar

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
)

// env is everything one ask needs, loaded once.
type env struct {
	deps     Deps
	shop     Shop
	loc      *time.Location
	staff    []Staff
	services []ServiceInfo
	policy   engine.Policy
	t        tr
	now      time.Time
	op       *Operator
}

func (d Deps) load(a Ask) (*env, error) {
	shop, err := d.Store.Shop()
	if err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(shop.TimeZone)
	if err != nil {
		return nil, fmt.Errorf("calendar: shop time zone %q: %w", shop.TimeZone, err)
	}
	staff, err := d.Store.Staff()
	if err != nil {
		return nil, err
	}
	svcs, err := d.Store.Services()
	if err != nil {
		return nil, err
	}
	pol, err := d.Store.Policy()
	if err != nil {
		return nil, err
	}
	loc2 := a.Locale
	if loc2 == "" {
		loc2 = "en"
	}
	if i := strings.IndexAny(loc2, "-_"); i > 0 {
		loc2 = loc2[:i]
	}
	return &env{deps: d, shop: shop, loc: loc, staff: staff, services: svcs, policy: pol,
		t: tr{locale: loc2}, now: a.Now.UTC(), op: a.Operator}, nil
}

func (e *env) date(t time.Time) engine.Date {
	y, m, d := t.In(e.loc).Date()
	return engine.Date{Y: y, M: m, D: d}
}

func (e *env) today() engine.Date { return e.date(e.now) }

// bounds is the UTC span of a civil day in the shop zone. On a DST change
// day it is 23 or 25 hours long.
func (e *env) bounds(d engine.Date) (time.Time, time.Time) {
	return time.Date(d.Y, d.M, d.D, 0, 0, 0, 0, e.loc).UTC(),
		time.Date(d.Y, d.M, d.D+1, 0, 0, 0, 0, e.loc).UTC()
}

func addDays(d engine.Date, n int) engine.Date {
	t := time.Date(d.Y, d.M, d.D+n, 12, 0, 0, 0, time.UTC)
	return engine.Date{Y: t.Year(), M: t.Month(), D: t.Day()}
}

func isoDate(d engine.Date) string { return fmt.Sprintf("%04d-%02d-%02d", d.Y, int(d.M), d.D) }
func compactDate(d engine.Date) string {
	return fmt.Sprintf("%04d%02d%02d", d.Y, int(d.M), d.D)
}

func parseISODate(s string) (engine.Date, bool) {
	t, err := time.Parse("2006-01-02", strings.TrimSpace(s))
	if err != nil {
		return engine.Date{}, false
	}
	return engine.Date{Y: t.Year(), M: t.Month(), D: t.Day()}, true
}

func parseCompactDate(s string) (engine.Date, bool) {
	t, err := time.Parse("20060102", s)
	if err != nil {
		return engine.Date{}, false
	}
	return engine.Date{Y: t.Year(), M: t.Month(), D: t.Day()}, true
}

func before(a, b engine.Date) bool {
	return isoDate(a) < isoDate(b)
}

// hm formats a UTC instant as shop-local wall-clock time.
func (e *env) hm(t time.Time) string { return t.In(e.loc).Format("15:04") }

// weekday returns the localized weekday name of a civil date.
func (e *env) weekday(d engine.Date) string {
	return e.t.s(fmt.Sprintf("appt.cal.wd.%d", int(time.Date(d.Y, d.M, d.D, 12, 0, 0, 0, time.UTC).Weekday())))
}

func (e *env) dateLabel(d engine.Date) string { return e.weekday(d) + " " + isoDate(d) }

func (e *env) span(b Booking) string { return e.hm(b.Start) + "-" + e.hm(b.End) }

func (e *env) staffByID(id string) (Staff, bool) {
	for _, s := range e.staff {
		if s.ID == id {
			return s, true
		}
	}
	return Staff{}, false
}

func (e *env) serviceBySKU(sku string) (ServiceInfo, bool) {
	for _, s := range e.services {
		if s.SKU == sku {
			return s, true
		}
	}
	return ServiceInfo{}, false
}

func (e *env) staffName(id string) string {
	if s, ok := e.staffByID(id); ok && s.Name != "" {
		return s.Name
	}
	return id
}

func (e *env) clientName(b Booking) string {
	if b.FirstName == "" {
		return e.t.s("appt.cal.walkin_client")
	}
	return b.FirstName
}

func (e *env) serviceNames(b Booking) string {
	names := make([]string, 0, len(b.Lines))
	for _, l := range b.Lines {
		names = append(names, l.Name)
	}
	return strings.Join(names, ", ")
}

// idRE is the shape of an id that can travel inside an action name: a UUID or
// any lower-case alphanumeric id with dashes.
var idRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// token turns an appointment id into the tail of an action name. It returns
// "" for an id that cannot be carried (never the case for a UUID).
func token(id string) string {
	if !idRE.MatchString(id) || len(id) > 40 {
		return ""
	}
	return strings.ReplaceAll(id, "-", "_")
}

func untoken(t string) string { return strings.ReplaceAll(t, "_", "-") }

// holdEnd is the end of the time an appointment occupies.
func holdEnd(b Booking) time.Time { return b.End.Add(time.Duration(b.BufferMin) * time.Minute) }

// sortBookings orders by start, then id, so output never depends on the store.
func sortBookings(bs []Booking) {
	sort.SliceStable(bs, func(i, j int) bool {
		if !bs[i].Start.Equal(bs[j].Start) {
			return bs[i].Start.Before(bs[j].Start)
		}
		return bs[i].ID < bs[j].ID
	})
}

// overlapping returns the ids of holding bookings that overlap another
// holding booking of the same resource (a stored double booking).
func overlapping(bs []Booking) map[string]bool {
	out := map[string]bool{}
	for i, a := range bs {
		if !a.State.Holds() {
			continue
		}
		for _, b := range bs[i+1:] {
			if b.ResourceID == a.ResourceID && b.State.Holds() &&
				a.Start.Before(holdEnd(b)) && b.Start.Before(holdEnd(a)) {
				out[a.ID], out[b.ID] = true, true
			}
		}
	}
	return out
}

func engineAppts(bs []Booking) []engine.Appointment {
	out := make([]engine.Appointment, len(bs))
	for i, b := range bs {
		out[i] = b.Appointment
	}
	return out
}
