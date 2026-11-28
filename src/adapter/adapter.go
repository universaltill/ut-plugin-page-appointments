// Package adapter implements calendar.Store on store.Repository (ut-docs#7208,
// review items B4, N9, N13). The calendar decides (availability, conflicts,
// overrides, transitions) and hands the adapter prepared rows and events; the
// repository owns the write order, because the host refuses BEGIN/COMMIT and
// runs one statement per call:
//
//   - insert: lines, then the appointment row (the commit point; a superseding
//     row also retires the old one, the unique index admits one superseder),
//     then the events, then the queue entry is removed. A crash after the row
//     leaves a booking with missing events (store.Reconcile repairs it) and,
//     for a seated walk-in, a queue entry the queue never lists because its id
//     is an appointment id.
//   - update: the state projection moves first with an optimistic check
//     (a stale post is refused), then the events.
//
// A row that is superseded, missing or already moved is calendar.ErrGone.
package adapter

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/universaltill/ut-plugin-page-appointments/src/calendar"
	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
	"github.com/universaltill/ut-plugin-page-appointments/src/money"
	"github.com/universaltill/ut-plugin-page-appointments/src/store"
)

// Item is what the till's catalogue says about a service SKU.
type Item struct {
	Name   string
	Price  money.Money
	Active bool
}

// Catalogue resolves service SKUs to catalogue items (the host's
// catalog.items.v1 view). A SKU the catalogue does not know is absent from
// the map: the service is then not bookable.
type Catalogue interface {
	Items(skus []string) (map[string]Item, error)
}

// Store is calendar.Store over a repository.
type Store struct {
	Ctx        context.Context
	Repo       *store.Repository
	Catalogue  Catalogue
	OriginTill string
}

var _ calendar.Store = (*Store)(nil)

func (s *Store) ctx() context.Context {
	if s.Ctx == nil {
		return context.Background()
	}
	return s.Ctx
}

func gone(err error) error {
	if errors.Is(err, store.ErrNotCurrent) || errors.Is(err, store.ErrStale) || errors.Is(err, store.ErrNotFound) {
		return calendar.ErrGone
	}
	return err
}

// Shop returns the shop zone and the till that writes the rows.
func (s *Store) Shop() (calendar.Shop, error) {
	set, err := s.Repo.Settings(s.ctx())
	if err != nil {
		return calendar.Shop{}, err
	}
	return calendar.Shop{TimeZone: set.ShopZone, OriginTill: s.OriginTill}, nil
}

// Policy returns the scheduling policy.
func (s *Store) Policy() (engine.Policy, error) {
	set, err := s.Repo.Settings(s.ctx())
	return set.Policy, err
}

// Staff returns every resource with its hours and time off. No resource is
// provider-mastered yet: provider calendars are not built (ADR-0221).
func (s *Store) Staff() ([]calendar.Staff, error) {
	recs, err := s.Repo.Resources(s.ctx())
	if err != nil {
		return nil, err
	}
	out := make([]calendar.Staff, len(recs))
	for i, r := range recs {
		name := r.DisplayName
		if name == "" {
			name = r.ID
		}
		out[i] = calendar.Staff{Resource: r.Resource, Name: name}
	}
	return out, nil
}

// Services returns the configured services with the catalogue's name and live
// price. A service whose item is gone or inactive in the catalogue is
// returned inactive, so it can still be shown on old bookings but not booked.
func (s *Store) Services() ([]calendar.ServiceInfo, error) {
	svcs, err := s.Repo.Services(s.ctx())
	if err != nil {
		return nil, err
	}
	skus := make([]string, len(svcs))
	for i, v := range svcs {
		skus[i] = v.SKU
	}
	items := map[string]Item{}
	if len(skus) > 0 && s.Catalogue != nil {
		if items, err = s.Catalogue.Items(skus); err != nil {
			return nil, err
		}
	}
	out := make([]calendar.ServiceInfo, len(svcs))
	for i, v := range svcs {
		it, ok := items[v.SKU]
		if !ok {
			it = Item{Name: v.SKU}
		}
		v.Active = v.Active && ok && it.Active
		out[i] = calendar.ServiceInfo{Service: v, Name: it.Name, Price: it.Price}
	}
	return out, nil
}

func toCalendar(b store.Booking) calendar.Booking {
	return calendar.Booking{Appointment: b.Appointment, FirstName: b.FirstName}
}

// Bookings returns the current (never superseded) rows overlapping [from, to).
func (s *Store) Bookings(from, to time.Time) ([]calendar.Booking, error) {
	rows, err := s.Repo.Current(s.ctx(), from, to, "")
	if err != nil {
		return nil, err
	}
	out := make([]calendar.Booking, len(rows))
	for i, b := range rows {
		out[i] = toCalendar(b)
	}
	return out, nil
}

// Booking returns one current row. A superseded row is "not found" (N13): a
// repeated post of a move must not act on the old row.
func (s *Store) Booking(id string) (calendar.Booking, bool, error) {
	b, err := s.Repo.Get(s.ctx(), id)
	if errors.Is(err, store.ErrNotFound) {
		return calendar.Booking{}, false, nil
	}
	if err != nil {
		return calendar.Booking{}, false, err
	}
	cur, err := s.Repo.IsCurrent(s.ctx(), id)
	if err != nil || !cur {
		return calendar.Booking{}, false, err
	}
	return toCalendar(b), true, nil
}

// Queue returns today's waiting walk-ins in arrival order.
func (s *Store) Queue() ([]calendar.Queued, error) {
	day, err := s.Repo.DayStart(s.ctx())
	if err != nil {
		return nil, err
	}
	rows, err := s.Repo.Queue(s.ctx(), day)
	if err != nil {
		return nil, err
	}
	out := make([]calendar.Queued, len(rows))
	for i, q := range rows {
		out[i] = calendar.Queued{FirstName: q.FirstName, ServiceSKU: q.ServiceSKU, Walkin: engine.Walkin{
			ID: q.ID, Seq: q.Seq, Arrived: q.Arrived, DurationMin: q.DurationMin, BufferMin: q.BufferMin, ResourceIDs: q.ResourceIDs}}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, nil
}

// Commit applies one change in the crash-safe order described on the package.
func (s *Store) Commit(ch calendar.Change) error {
	ctx := s.ctx()
	switch {
	case ch.Insert != nil:
		b := store.Booking{Appointment: ch.Insert.Appointment, FirstName: ch.Insert.FirstName}
		if ch.Superseded != "" {
			if b.SupersedesID == "" {
				b.SupersedesID = ch.Superseded
			}
			old, err := s.Repo.Get(ctx, ch.Superseded)
			if err != nil {
				return gone(err)
			}
			b.Currency = old.Currency // a move never changes the record's currency
		}
		if err := s.Repo.Insert(ctx, b, ch.Events); err != nil {
			return gone(err)
		}
		if ch.Dequeue != "" {
			return s.Repo.Dequeue(ctx, ch.Dequeue)
		}
		return nil
	case ch.Update != nil:
		cur, err := s.Repo.Get(ctx, ch.Update.ID)
		if err != nil {
			return gone(err)
		}
		return gone(s.Repo.SetState(ctx, ch.Update.ID, cur.State, ch.Update.Appointment, ch.Events))
	case ch.Enqueue != nil:
		w := ch.Enqueue
		return s.Repo.Enqueue(ctx, store.QueueEntry{ID: w.ID, Seq: w.Seq, Arrived: w.Arrived, FirstName: w.FirstName,
			ServiceSKU: w.ServiceSKU, DurationMin: w.DurationMin, BufferMin: w.BufferMin, ResourceIDs: w.ResourceIDs})
	case ch.Dequeue != "":
		return s.Repo.Dequeue(ctx, ch.Dequeue)
	}
	return nil
}
