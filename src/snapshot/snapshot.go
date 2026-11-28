// Package snapshot builds the read-only appointment calendar the main till
// publishes to replica tills (ut-docs design 7207, ADR-0086: the main till is
// the sole writer). It is pure: no host call, no clock, no storage; the caller
// passes the current bookings and hands the bytes to snapshot_set.
//
// The payload has a closed key set. It carries a client's first name and the
// service names, and never a contact detail, note, deposit, amount, sale
// reference, late-cancel flag, origin till or provider id; the builder reads
// only the fields it copies, and the tests pin the exact keys.
package snapshot

import (
	"encoding/json"
	"errors"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
	"github.com/universaltill/ut-plugin-page-appointments/src/store"
)

// Collection is the plugin-records snapshot collection (manifest
// records_forward.snapshots).
const Collection = "calendar"

// Payload bounds (design 7207 section 3). The channel allows 64 KiB per
// collection; MaxBytes leaves 4 KiB of headroom.
const (
	Version       = 1
	MaxBytes      = 60 * 1024
	WindowDays    = 3 // shop-local today and the next two days
	MaxResources  = 20
	MaxServices   = 3
	MaxNameRunes  = 40 // first name and each service name
	MaxResNameLen = 80
	MaxColourLen  = 16
	// RefreshAfter is how old as_of may get on an idle till before Changed
	// asks for a write: the replica cannot tell an idle main till from an
	// unreachable one, so the main till says it is alive.
	RefreshAfter = 60 * time.Minute
)

// Input is what Build reads. Bookings must be the current appointments (a
// superseded row is not current); Resources are the shop's resources.
type Input struct {
	Now       time.Time
	Zone      *time.Location
	Resources []store.ResourceRecord
	Bookings  []store.Booking
}

type resourceOut struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Colour string `json:"colour"`
}

type apptOut struct {
	ID  string   `json:"id"`
	R   string   `json:"r"`
	S   string   `json:"s"`
	E   string   `json:"e"`
	B   int      `json:"b"`
	St  string   `json:"st"`
	W   bool     `json:"w"`
	N   string   `json:"n"`
	Svc []string `json:"svc"`
}

type payload struct {
	V            int           `json:"v"`
	AsOf         string        `json:"as_of"`
	Zone         string        `json:"zone"`
	FromUTC      string        `json:"from_utc"`
	ToUTC        string        `json:"to_utc"`
	Truncated    bool          `json:"truncated"`
	Resources    []resourceOut `json:"resources"`
	Appointments []apptOut     `json:"appointments"`
}

// Window is [from, to): shop-local today 00:00 to the WindowDays-th local
// midnight, as UTC instants. Computed on civil dates, so a daylight-saving
// day is 23 or 25 hours, never assumed 24.
func Window(now time.Time, loc *time.Location) (from, to time.Time) {
	y, m, d := now.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc).UTC(), time.Date(y, m, d+WindowDays, 0, 0, 0, 0, loc).UTC()
}

// cut shortens s to at most n runes.
func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// Build returns the snapshot bytes for in.
func Build(in Input) ([]byte, error) {
	if in.Zone == nil {
		return nil, errors.New("snapshot: no shop zone")
	}
	if in.Now.IsZero() {
		return nil, errors.New("snapshot: no current time")
	}
	from, to := Window(in.Now, in.Zone)
	p := payload{
		V: Version, AsOf: ts(in.Now.Truncate(time.Second)), Zone: in.Zone.String(),
		FromUTC: ts(from), ToUTC: ts(to),
		Resources: []resourceOut{}, Appointments: []apptOut{},
	}

	recs := append([]store.ResourceRecord(nil), in.Resources...)
	sort.Slice(recs, func(i, j int) bool { return recs[i].ID < recs[j].ID })
	// listed: the resources the payload carries; overCap: active resources
	// left out by MaxResources. An appointment is sent only on a listed
	// resource, so the replica never draws a row on a missing `r`; rows on
	// an over-cap resource are a truncation, rows on an inactive or unknown
	// one are simply not shown.
	listed, overCap := map[string]bool{}, map[string]bool{}
	for _, r := range recs {
		if !r.Active {
			continue
		}
		if len(p.Resources) == MaxResources {
			overCap[r.ID] = true
			continue
		}
		listed[r.ID] = true
		p.Resources = append(p.Resources, resourceOut{
			ID: r.ID, Kind: r.Kind, Name: cut(r.DisplayName, MaxResNameLen), Colour: cut(r.Colour, MaxColourLen),
		})
	}

	rows := make([]apptOut, 0, len(in.Bookings))
	droppedByCap := false
	for _, b := range in.Bookings {
		a := b.Appointment
		if a.Source != "own" || a.State == engine.CancelledByClient || a.State == engine.CancelledByShop {
			continue
		}
		if !a.Start.Before(to) || !a.End.After(from) {
			continue
		}
		if !listed[a.ResourceID] {
			droppedByCap = droppedByCap || overCap[a.ResourceID]
			continue
		}
		svc := []string{}
		for _, l := range a.Lines {
			if len(svc) == MaxServices {
				break
			}
			svc = append(svc, cut(l.Name, MaxNameRunes))
		}
		rows = append(rows, apptOut{
			ID: a.ID, R: a.ResourceID, S: ts(a.Start), E: ts(a.End), B: a.BufferMin,
			St: string(a.State), W: a.WalkIn, N: cut(b.FirstName, MaxNameRunes), Svc: svc,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].S != rows[j].S {
			return rows[i].S < rows[j].S
		}
		return rows[i].ID < rows[j].ID
	})

	// Keep the longest prefix (the earliest rows) that fits.
	fit := func(n int) ([]byte, error) {
		p.Appointments = rows[:n]
		p.Truncated = n < len(rows) || droppedByCap
		return json.Marshal(p)
	}
	raw, err := fit(len(rows))
	if err != nil {
		return nil, err
	}
	if len(raw) <= MaxBytes {
		return raw, nil
	}
	lo, hi := 0, len(rows) // rows[:lo] fits (checked below), rows[:hi] does not
	if raw, err = fit(0); err != nil {
		return nil, err
	}
	if len(raw) > MaxBytes {
		return nil, errors.New("snapshot: resources alone exceed the cap")
	}
	for hi-lo > 1 {
		mid := (lo + hi) / 2
		b, err := fit(mid)
		if err != nil {
			return nil, err
		}
		if len(b) <= MaxBytes {
			lo = mid
		} else {
			hi = mid
		}
	}
	return fit(lo)
}

// Changed reports whether next should replace prev now (design 7207 section
// 4): there is no readable previous snapshot, the window moved, the content
// (everything but as_of) differs, or prev's as_of is RefreshAfter old.
func Changed(prev, next []byte, now time.Time) bool {
	var p, n payload
	if json.Unmarshal(prev, &p) != nil || json.Unmarshal(next, &n) != nil || p.V != Version {
		return true
	}
	if p.FromUTC != n.FromUTC || p.ToUTC != n.ToUTC {
		return true
	}
	asOf, err := time.Parse(time.RFC3339, p.AsOf)
	if err != nil || now.Sub(asOf) >= RefreshAfter {
		return true
	}
	p.AsOf = n.AsOf
	a, err1 := json.Marshal(p)
	b, err2 := json.Marshal(n)
	return err1 != nil || err2 != nil || string(a) != string(b)
}
