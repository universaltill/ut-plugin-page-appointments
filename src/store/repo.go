package store

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
	"github.com/universaltill/ut-plugin-page-appointments/src/money"
)

// KindPurge is the event kind written by the retention purge and by an
// erasure. It never carries a name.
const KindPurge = "purge"

// Purge reasons recorded on the purge event.
const (
	ReasonRetention = "retention"
	ReasonErasure   = "erasure"
	ReasonReconcile = "reconcile"
)

// Repository stores resources, services, settings and appointments. It is the
// only writer (the main till, ADR-0086). Appointment rows are immutable:
// reschedule writes a new row with SupersedesID, state changes append events.
// It is safe for concurrent use; writes are serialised on one mutex.
type Repository struct {
	db     DB
	clock  engine.Clock
	ids    engine.IDGen
	tillID string
	mu     sync.Mutex
	// pageSize is the keyset page of Purge and Reconcile (0 = DefaultPageSize).
	pageSize int
}

// New returns a Repository. tillID is the origin till of new bookings.
func New(db DB, clock engine.Clock, ids engine.IDGen, tillID string) *Repository {
	return &Repository{db: db, clock: clock, ids: ids, tillID: tillID}
}

// ResourceRecord is a resource with its display fields, hours and time off.
type ResourceRecord struct {
	engine.Resource
	DisplayName     string
	Colour          string
	LogbookStaffRef string
}

// TimeOffRecord is one time-off entry with its id.
type TimeOffRecord struct {
	ID, ResourceID string
	From, To       time.Time
	Reason         string
}

// Booking is an appointment with its currency and the one client field the
// contract allows, the first name. There is no phone, email or note.
type Booking struct {
	engine.Appointment
	FirstName string
	Currency  string
}

// Settings are the shop's scheduling settings (the engine Policy plus the shop
// zone, retention and currency).
type Settings struct {
	engine.Policy
	ShopZone      string
	RetentionDays int
	Currency      string
}

// DefaultSettings are the documented defaults (UK shop).
func DefaultSettings() Settings {
	return Settings{Policy: engine.DefaultPolicy(), ShopZone: "Europe/London",
		RetentionDays: DefaultRetentionDays, Currency: "GBP"}
}

// Location loads the shop zone.
func (s Settings) Location() (*time.Location, error) { return time.LoadLocation(s.ShopZone) }

// ---- resources -----------------------------------------------------------

// SaveResource upserts the resource row (not its hours or time off).
func (r *Repository) SaveResource(ctx context.Context, rec ResourceRecord) error {
	rec.DisplayName = strings.TrimSpace(rec.DisplayName)
	n := utf8.RuneCountInString(rec.DisplayName)
	if rec.ID == "" || n < 1 || n > 80 || (rec.Kind != "staff" && rec.Kind != "chair") {
		return ErrInvalidInput
	}
	status := "inactive"
	if rec.Active {
		status = "active"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err := r.db.Exec(ctx, SQLUpsertResource, rec.ID, rec.Kind, rec.DisplayName, rec.Colour, status, rec.LogbookStaffRef)
	return err
}

// SetWorkingHours replaces the weekly windows of a resource. Windows must
// satisfy 0 <= start < end <= 1440 and may not overlap on one weekday.
func (r *Repository) SetWorkingHours(ctx context.Context, resourceID string, hours map[time.Weekday][]engine.Window) error {
	for d := time.Sunday; d <= time.Saturday; d++ {
		ws := append([]engine.Window(nil), hours[d]...)
		for i, w := range ws {
			if w.StartMin < 0 || w.StartMin >= w.EndMin || w.EndMin > 1440 {
				return ErrInvalidInput
			}
			for _, o := range ws[:i] {
				if w.StartMin < o.EndMin && o.StartMin < w.EndMin {
					return ErrInvalidInput
				}
			}
		}
	}
	if resourceID == "" {
		return ErrInvalidInput
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.db.Exec(ctx, SQLDeleteWorkingHours, resourceID); err != nil {
		return err
	}
	for d := time.Sunday; d <= time.Saturday; d++ {
		for _, w := range hours[d] {
			if _, err := r.db.Exec(ctx, SQLInsertWorkingHour, resourceID, int(d), w.StartMin, w.EndMin); err != nil {
				return err
			}
		}
	}
	return nil
}

// AddTimeOff stores a time-off entry. The reason is from the fixed list ("" is
// "other"). It returns the new id.
func (r *Repository) AddTimeOff(ctx context.Context, resourceID string, from, to time.Time, reason string) (string, error) {
	reason, err := NormaliseTimeOffReason(reason)
	if err != nil {
		return "", err
	}
	if resourceID == "" || !to.After(from) {
		return "", ErrInvalidInput
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	id := r.ids()
	_, err = r.db.Exec(ctx, SQLInsertTimeOff, id, resourceID, from.Unix(), to.Unix(), reason)
	return id, err
}

// DeleteTimeOff removes one time-off entry.
func (r *Repository) DeleteTimeOff(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err := r.db.Exec(ctx, SQLDeleteTimeOff, id)
	return err
}

// TimeOff lists time-off entries (all resources when resourceID is "").
func (r *Repository) TimeOff(ctx context.Context, resourceID string) ([]TimeOffRecord, error) {
	rows, err := query(ctx, r.db, SQLListTimeOff)
	if err != nil {
		return nil, err
	}
	var out []TimeOffRecord
	for _, w := range rows {
		if resourceID != "" && w.str("resource_id") != resourceID {
			continue
		}
		out = append(out, TimeOffRecord{ID: w.str("id"), ResourceID: w.str("resource_id"),
			From: time.Unix(w.i64("from_utc"), 0).UTC(), To: time.Unix(w.i64("to_utc"), 0).UTC(), Reason: w.str("reason")})
	}
	return out, nil
}

// Resources returns every resource with its hours and time off, ordered by id.
func (r *Repository) Resources(ctx context.Context) ([]ResourceRecord, error) {
	rows, err := query(ctx, r.db, SQLListResources)
	if err != nil {
		return nil, err
	}
	hrows, err := query(ctx, r.db, SQLListWorkingHours)
	if err != nil {
		return nil, err
	}
	tos, err := r.TimeOff(ctx, "")
	if err != nil {
		return nil, err
	}
	out := make([]ResourceRecord, 0, len(rows))
	idx := map[string]int{}
	for _, w := range rows {
		rec := ResourceRecord{
			Resource:    engine.Resource{ID: w.str("id"), Kind: w.str("kind"), Active: w.str("status") == "active", WorkingHours: map[time.Weekday][]engine.Window{}},
			DisplayName: w.str("display_name"), Colour: w.str("colour"), LogbookStaffRef: w.str("logbook_staff_ref"),
		}
		idx[rec.ID] = len(out)
		out = append(out, rec)
	}
	for _, h := range hrows {
		if i, ok := idx[h.str("resource_id")]; ok {
			d := time.Weekday(h.i64("weekday"))
			out[i].WorkingHours[d] = append(out[i].WorkingHours[d], engine.Window{StartMin: int(h.i64("start_min")), EndMin: int(h.i64("end_min"))})
		}
	}
	for _, t := range tos {
		if i, ok := idx[t.ResourceID]; ok {
			out[i].TimeOff = append(out[i].TimeOff, engine.TimeOff{From: t.From, To: t.To, Reason: t.Reason})
		}
	}
	return out, nil
}

// Resource returns one resource or ErrNotFound.
func (r *Repository) Resource(ctx context.Context, id string) (ResourceRecord, error) {
	all, err := r.Resources(ctx)
	if err != nil {
		return ResourceRecord{}, err
	}
	for _, x := range all {
		if x.ID == id {
			return x, nil
		}
	}
	return ResourceRecord{}, ErrNotFound
}

// ---- services ------------------------------------------------------------

// SaveService upserts a service and its eligible resources.
func (r *Repository) SaveService(ctx context.Context, s engine.Service) error {
	if s.SKU == "" || s.DurationMin <= 0 || s.BufferMin < 0 {
		return ErrInvalidInput
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.db.Exec(ctx, SQLUpsertService, s.SKU, s.DurationMin, s.BufferMin, b2i(s.All), b2i(s.Active)); err != nil {
		return err
	}
	if _, err := r.db.Exec(ctx, SQLDeleteServiceResource, s.SKU); err != nil {
		return err
	}
	if s.All {
		return nil
	}
	seen := map[string]bool{}
	for _, id := range s.ResourceIDs {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if _, err := r.db.Exec(ctx, SQLInsertServiceResource, s.SKU, id); err != nil {
			return err
		}
	}
	return nil
}

// Services lists the services ordered by SKU.
func (r *Repository) Services(ctx context.Context) ([]engine.Service, error) {
	rows, err := query(ctx, r.db, SQLListServices)
	if err != nil {
		return nil, err
	}
	srows, err := query(ctx, r.db, SQLListServiceResources)
	if err != nil {
		return nil, err
	}
	out := make([]engine.Service, 0, len(rows))
	idx := map[string]int{}
	for _, w := range rows {
		idx[w.str("sku")] = len(out)
		out = append(out, engine.Service{SKU: w.str("sku"), DurationMin: int(w.i64("duration_min")),
			BufferMin: int(w.i64("buffer_min")), All: w.flag("all_resources"), Active: w.flag("active")})
	}
	for _, s := range srows {
		if i, ok := idx[s.str("sku")]; ok {
			out[i].ResourceIDs = append(out[i].ResourceIDs, s.str("resource_id"))
		}
	}
	return out, nil
}

// ---- settings ------------------------------------------------------------

// Settings returns the stored settings over the defaults.
func (r *Repository) Settings(ctx context.Context) (Settings, error) {
	rows, err := query(ctx, r.db, SQLListSettings)
	if err != nil {
		return Settings{}, err
	}
	s := DefaultSettings()
	for _, w := range rows {
		v := w.str("value")
		n, _ := strconv.Atoi(v)
		switch w.str("key") {
		case "shop_zone":
			s.ShopZone = v
		case "currency":
			s.Currency = v
		case "retention_days":
			s.RetentionDays = n
		case "slot_step_min":
			s.SlotStepMin = n
		case "no_show_grace_min":
			s.NoShowGraceMin = n
		case "late_cancel_hours":
			s.LateCancelHours = n
		case "overrun_min":
			s.OverrunMin = n
		case "allow_double_booking":
			s.AllowDoubleBooking = v == "1"
		}
	}
	return s, nil
}

// SaveSettings validates and stores the settings.
func (r *Repository) SaveSettings(ctx context.Context, s Settings) error {
	if err := s.Policy.Validate(); err != nil {
		return err
	}
	if err := ValidateRetentionDays(s.RetentionDays); err != nil {
		return err
	}
	if _, err := s.Location(); err != nil || s.ShopZone == "" || len(s.Currency) != 3 {
		return ErrInvalidInput
	}
	kv := [][2]string{
		{"shop_zone", s.ShopZone}, {"currency", s.Currency},
		{"retention_days", strconv.Itoa(s.RetentionDays)}, {"slot_step_min", strconv.Itoa(s.SlotStepMin)},
		{"no_show_grace_min", strconv.Itoa(s.NoShowGraceMin)}, {"late_cancel_hours", strconv.Itoa(s.LateCancelHours)},
		{"overrun_min", strconv.Itoa(s.OverrunMin)}, {"allow_double_booking", strconv.Itoa(b2i(s.AllowDoubleBooking))},
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range kv {
		if _, err := r.db.Exec(ctx, SQLUpsertSetting, p[0], p[1]); err != nil {
			return err
		}
	}
	return nil
}

// ---- appointments --------------------------------------------------------

// NewBooking asks to create an own appointment. A non-nil Override writes the
// booking over a conflict (manager only, see engine.CheckConflict).
type NewBooking struct {
	Appointment engine.Appointment // ID empty: generated; Source forced to "own"
	FirstName   string
	ActorUserID string
	Override    *engine.Override
}

func (r *Repository) validateLines(a engine.Appointment) error {
	if len(a.Lines) == 0 {
		return ErrInvalidInput
	}
	for _, l := range a.Lines {
		if l.SKU == "" || l.Qty <= 0 || l.DurationMin <= 0 || l.BufferMin < 0 {
			return ErrInvalidInput
		}
	}
	return nil
}

// Create writes a new own appointment after the engine conflict check. A
// walk-in (Appointment.WalkIn) is created checked_in, any other booking
// booked. Order of writes: lines, then the appointment row (the commit
// point), then events; a failed attempt leaves only unreferenced lines, which
// a retry with the same id reuses.
func (r *Repository) Create(ctx context.Context, nb NewBooking) (Booking, error) {
	name, err := ValidateFirstName(nb.FirstName)
	if err != nil {
		return Booking{}, err
	}
	a := nb.Appointment
	if !a.Deposit.IsZero() {
		return Booking{}, ErrDepositReserved
	}
	if err := r.validateLines(a); err != nil {
		return Booking{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	set, err := r.Settings(ctx)
	if err != nil {
		return Booking{}, err
	}
	now := r.clock.Now()
	provided := a.ID != ""
	if !provided {
		a.ID = r.ids()
	}
	a.Source, a.OriginTillID, a.SupersedesID = "own", r.tillID, ""
	a.SaleRef, a.LateCancel = "", false
	a.Start, a.End = a.Start.UTC(), a.End.UTC()
	var created engine.Event
	if a.WalkIn {
		a, created, err = engine.NewWalkIn(a, nb.ActorUserID, now, r.ids)
		if err != nil {
			return Booking{}, err
		}
	} else {
		if !a.End.After(a.Start) || a.ResourceID == "" || a.BufferMin < 0 {
			return Booking{}, engine.ErrInvalid
		}
		a.State, a.CreatedAt = engine.Booked, now
		created = engine.Event{ID: r.ids(), AppointmentID: a.ID, ActorUserID: nb.ActorUserID, At: now,
			Kind: engine.KindStateChange, ToState: engine.Booked}
	}
	b := Booking{Appointment: a, FirstName: name, Currency: set.Currency}
	if provided {
		if existing, found, err := r.existingForCreate(ctx, b); err != nil || found {
			return existing, err
		}
	}
	return r.insertChecked(ctx, b, set, nb.Override, created)
}

// existingForCreate handles a caller-supplied id (a retry after a lost
// answer). If the appointment exists and the request describes the same
// booking, that booking comes back and nothing is written; if it differs,
// ErrIDExists. Lines left behind by a crash before the appointment row (no
// row, only lines) must equal the new lines, else ErrIDExists: lines of an
// immutable booking are never added to. The caller holds r.mu.
func (r *Repository) existingForCreate(ctx context.Context, b Booking) (Booking, bool, error) {
	cur, err := r.get(ctx, b.ID)
	switch {
	case err == nil:
		if cur.ResourceID == b.ResourceID && cur.Start.Equal(b.Start) && cur.End.Equal(b.End) &&
			cur.BufferMin == b.BufferMin && cur.WalkIn == b.WalkIn && sameLines(cur.Lines, b.Lines) {
			return cur, true, nil
		}
		return Booking{}, false, ErrIDExists
	case errors.Is(err, ErrNotFound):
	default:
		return Booking{}, false, err
	}
	orphan := Booking{Appointment: engine.Appointment{ID: b.ID}}
	if err := r.loadLines(ctx, &orphan); err != nil {
		return Booking{}, false, err
	}
	if len(orphan.Lines) > 0 && !sameLines(orphan.Lines, b.Lines) {
		return Booking{}, false, ErrIDExists
	}
	return Booking{}, false, nil
}

func sameLines(a, b []engine.Line) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.SKU != y.SKU || x.Name != y.Name || x.Qty != y.Qty || x.Amount.Minor() != y.Amount.Minor() ||
			x.DurationMin != y.DurationMin || x.BufferMin != y.BufferMin {
			return false
		}
	}
	return true
}

// insertChecked runs the conflict check against the stored current rows and
// writes the booking. The caller holds r.mu.
func (r *Repository) insertChecked(ctx context.Context, b Booking, set Settings, ov *engine.Override, created engine.Event) (Booking, error) {
	hold := b.End.Add(time.Duration(b.BufferMin) * time.Minute)
	existing, err := r.listCurrent(ctx, b.Start, hold, b.ResourceID)
	if err != nil {
		return Booking{}, err
	}
	res, err := engine.CheckConflict(b.Appointment, appointments(existing), set.Policy, ov, r.clock, r.ids)
	if err != nil {
		return Booking{}, err
	}
	for i, l := range b.Lines {
		if _, err := r.db.Exec(ctx, SQLInsertLine, b.ID, i, l.SKU, l.Name, l.Qty, l.Amount.Minor(), l.DurationMin, l.BufferMin); err != nil {
			return Booking{}, err
		}
	}
	if _, err := r.db.Exec(ctx, SQLInsertAppointment, b.ID, b.OriginTillID, b.Source, b.ResourceID,
		b.Start.Unix(), b.End.Unix(), b.BufferMin, b2i(b.WalkIn), string(b.State), b.FirstName, b.Currency,
		b.SaleRef, b2i(b.LateCancel), b.SupersedesID, b.CreatedAt.Unix()); err != nil {
		return Booking{}, err
	}
	if err := r.insertEvent(ctx, created); err != nil {
		return Booking{}, err
	}
	for _, ev := range res.Events {
		if err := r.insertEvent(ctx, ev); err != nil {
			return Booking{}, err
		}
	}
	return b, nil
}

func appointments(bs []Booking) []engine.Appointment {
	out := make([]engine.Appointment, len(bs))
	for i, b := range bs {
		out[i] = b.Appointment
	}
	return out
}

// Reschedule supersedes the appointment with a new row at newStart on
// newResourceID (the old row stops holding time and takes no further
// transition). The new row's CreatedAt is the time of the reschedule and it
// keeps the state, the lines and the first name.
func (r *Repository) Reschedule(ctx context.Context, id string, newStart time.Time, newResourceID, actorUserID string, ov *engine.Override) (Booking, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	old, err := r.get(ctx, id)
	if err != nil {
		return Booking{}, err
	}
	if cur, err := r.isCurrent(ctx, id); err != nil {
		return Booking{}, err
	} else if !cur {
		return Booking{}, ErrNotCurrent
	}
	set, err := r.Settings(ctx)
	if err != nil {
		return Booking{}, err
	}
	n, err := engine.Supersede(old.Appointment, newStart.UTC(), newResourceID, r.ids(), r.ids)
	if err != nil {
		return Booking{}, err
	}
	now := r.clock.Now()
	n.CreatedAt = now // Supersede keeps the old CreatedAt; a reschedule is created now
	created := engine.Event{ID: r.ids(), AppointmentID: n.ID, ActorUserID: actorUserID, At: now,
		Kind: engine.KindStateChange, ToState: n.State}
	return r.insertChecked(ctx, Booking{Appointment: n, FirstName: old.FirstName, Currency: old.Currency}, set, ov, created)
}

// Transition applies an engine transition to a current appointment. The state
// projection is moved first, with an optimistic check on the old state (a
// concurrent change returns ErrStale), then the event is appended. If the
// event write fails, Reconcile appends it later.
func (r *Repository) Transition(ctx context.Context, id string, in engine.TransitionInput) (Booking, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, err := r.get(ctx, id)
	if err != nil {
		return Booking{}, err
	}
	if cur, err := r.isCurrent(ctx, id); err != nil {
		return Booking{}, err
	} else if !cur {
		return Booking{}, ErrNotCurrent
	}
	set, err := r.Settings(ctx)
	if err != nil {
		return Booking{}, err
	}
	if in.Now.IsZero() {
		in.Now = r.clock.Now()
	}
	out, ev, err := engine.Transition(b.Appointment, in, set.Policy, r.ids)
	if err != nil {
		return Booking{}, err
	}
	events := []engine.Event{ev}
	if b.State == engine.NoShow && in.To == engine.CheckedIn {
		// The chair is held again. The manager's correction is always allowed
		// (a mistake must be undoable); an overlap with a booking that took
		// the slot meanwhile is recorded as a conflict_override event with the
		// manager's reason, whatever the double-booking setting (the engine
		// already required the manager, the reason and the actor).
		existing, err := r.listCurrent(ctx, out.Start, out.End.Add(time.Duration(out.BufferMin)*time.Minute), out.ResourceID)
		if err != nil {
			return Booking{}, err
		}
		pol := set.Policy
		pol.AllowDoubleBooking = true
		ov := &engine.Override{ByManager: true, Reason: ev.Reason, ActorUserID: in.ActorUserID}
		res, err := engine.CheckConflict(out, appointments(existing), pol, ov, r.clock, r.ids)
		if err != nil {
			return Booking{}, err
		}
		events = append(events, res.Events...)
	}
	n, err := r.db.Exec(ctx, SQLSetProjection, id, string(out.State), b2i(out.LateCancel), out.SaleRef, string(b.State))
	if err != nil {
		return Booking{}, err
	}
	if n == 0 {
		return Booking{}, ErrStale
	}
	for _, e := range events {
		if err := r.insertEvent(ctx, e); err != nil {
			return Booking{}, err
		}
	}
	b.Appointment = out
	return b, nil
}

// Get returns one appointment (superseded rows included) with its lines.
func (r *Repository) Get(ctx context.Context, id string) (Booking, error) { return r.get(ctx, id) }

func (r *Repository) get(ctx context.Context, id string) (Booking, error) {
	rows, err := query(ctx, r.db, SQLGetAppointment, id)
	if err != nil {
		return Booking{}, err
	}
	if len(rows) == 0 {
		return Booking{}, ErrNotFound
	}
	b := toBooking(rows[0])
	if err := r.loadLines(ctx, &b); err != nil {
		return Booking{}, err
	}
	return b, nil
}

func (r *Repository) isCurrent(ctx context.Context, id string) (bool, error) {
	rows, err := query(ctx, r.db, SQLHasSuperseder, id)
	return len(rows) == 0, err
}

func (r *Repository) loadLines(ctx context.Context, b *Booking) error {
	rows, err := query(ctx, r.db, SQLListLines, b.ID)
	if err != nil {
		return err
	}
	b.Lines = make([]engine.Line, 0, len(rows))
	for _, w := range rows {
		b.Lines = append(b.Lines, engine.Line{SKU: w.str("sku"), Name: w.str("name_snapshot"), Qty: int(w.i64("qty")),
			Amount: money.FromMinor(w.i64("amount_minor")), DurationMin: int(w.i64("duration_min")), BufferMin: int(w.i64("buffer_min"))})
	}
	return nil
}

func toBooking(w row) Booking {
	return Booking{
		Appointment: engine.Appointment{
			ID: w.str("id"), OriginTillID: w.str("origin_till_id"), Source: w.str("source"), ResourceID: w.str("resource_id"),
			Start: time.Unix(w.i64("start_utc"), 0).UTC(), End: time.Unix(w.i64("end_utc"), 0).UTC(),
			BufferMin: int(w.i64("buffer_min")), State: engine.State(w.str("state")), WalkIn: w.flag("walk_in"),
			SaleRef: w.str("sale_ref"), LateCancel: w.flag("late_cancel"), SupersedesID: w.str("supersedes_id"),
			CreatedAt: time.Unix(w.i64("created_at"), 0).UTC(),
		},
		FirstName: w.str("client_first_name"), Currency: w.str("currency"),
	}
}

// listCurrent returns the current (non-superseded) rows whose hold interval
// meets [from, to), without lines. resourceID "" means every resource.
func (r *Repository) listCurrent(ctx context.Context, from, to time.Time, resourceID string) ([]Booking, error) {
	rows, err := query(ctx, r.db, SQLListCurrent, from.Unix(), to.Unix(), resourceID)
	if err != nil {
		return nil, err
	}
	out := make([]Booking, len(rows))
	for i, w := range rows {
		out[i] = toBooking(w)
	}
	return out, nil
}

// Current lists the current appointments whose hold meets [from, to), with
// lines, ordered by start then id. resourceID "" means every resource. A
// superseded row is never returned, so the result can be passed to the engine
// as "existing".
func (r *Repository) Current(ctx context.Context, from, to time.Time, resourceID string) ([]Booking, error) {
	out, err := r.listCurrent(ctx, from, to, resourceID)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if err := r.loadLines(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (r *Repository) insertEvent(ctx context.Context, ev engine.Event) error {
	ids := ev.ConflictingIDs
	if ids == nil {
		ids = []string{}
	}
	b, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(ctx, SQLInsertEvent, ev.ID, ev.AppointmentID, ev.At.Unix(), ev.ActorUserID, ev.Kind,
		string(ev.FromState), string(ev.ToState), ev.Reason, string(b))
	return err
}

// Events lists the events of an appointment in order.
func (r *Repository) Events(ctx context.Context, id string) ([]engine.Event, error) {
	rows, err := query(ctx, r.db, SQLListEvents, id)
	if err != nil {
		return nil, err
	}
	out := make([]engine.Event, len(rows))
	for i, w := range rows {
		var ids []string
		_ = json.Unmarshal([]byte(w.str("conflicting_ids")), &ids)
		out[i] = engine.Event{ID: w.str("id"), AppointmentID: w.str("appointment_id"), ActorUserID: w.str("actor_user_id"),
			At: time.Unix(w.i64("at"), 0).UTC(), Kind: w.str("kind"), FromState: engine.State(w.str("from_state")),
			ToState: engine.State(w.str("to_state")), Reason: w.str("reason"), ConflictingIDs: ids}
	}
	return out, nil
}

// Reconcile repairs a state projection that disagrees with the last event of
// its row (a crash between the two writes of Transition): it appends the
// missing event. "Last" is insert order, not the wall clock, so a clock that
// stepped back never causes a repair. It reads the stale rows a page at a
// time (host result cap) and returns how many rows were repaired.
func (r *Repository) Reconcile(ctx context.Context) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	after := ""
	for {
		rows, err := query(ctx, r.db, SQLStaleProjection, after, r.page())
		if err != nil {
			return n, err
		}
		for _, w := range rows {
			after = w.str("id")
			ev := engine.Event{ID: r.ids(), AppointmentID: after, At: r.clock.Now(), Kind: engine.KindStateChange,
				FromState: engine.State(w.str("last_state")), ToState: engine.State(w.str("state")), Reason: ReasonReconcile}
			if err := r.insertEvent(ctx, ev); err != nil {
				return n, err
			}
			n++
		}
		if len(rows) < r.page() {
			return n, nil
		}
	}
}
