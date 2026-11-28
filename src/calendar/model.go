package calendar

import (
	"time"

	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
	"github.com/universaltill/ut-plugin-page-appointments/src/money"
)

// Booking is an appointment plus the one personal field the till keeps.
// There is no phone, email or note field by design (appointment-v1 section 1
// and 7): do not add one.
type Booking struct {
	engine.Appointment
	FirstName string
}

// Staff is a bookable resource with its display name. A provider-mastered
// resource takes walk-ins only; its future bookings come from the provider.
type Staff struct {
	engine.Resource
	Name             string
	ProviderMastered bool
}

// ServiceInfo is a service with the catalogue name and the live price shown
// as the quote on the booking line.
type ServiceInfo struct {
	engine.Service
	Name  string
	Price money.Money
}

// Shop holds the shop facts the calendar needs.
type Shop struct {
	TimeZone   string // IANA name, e.g. "Europe/London"
	OriginTill string // id of the main till writing the rows
}

// Queued is a walk-in waiting for a chair.
type Queued struct {
	engine.Walkin
	FirstName  string
	ServiceSKU string
}

// Change is one write the calendar needs. The host refuses BEGIN/COMMIT for a
// guest and runs one statement per call, so a Store cannot use a transaction:
// adapter.Store applies a Change in a crash-safe order (insert row, then
// events, then the queue entry goes; an update moves the state projection
// first, then appends events) and store.Reconcile repairs a crash in between.
// A seated walk-in reuses its queue id, so the queue never lists a walk-in
// that is already an appointment.
type Change struct {
	// Insert is a new appointment row (booking, walk-in, replacement row).
	Insert *Booking
	// Superseded is the id of the row Insert replaces; it stops holding time.
	Superseded string
	// Update replaces the projected state (and LateCancel/SaleRef) of an
	// existing row. Booking data never changes in place.
	Update *Booking
	// From is the state the decision behind Update was made on. The write is
	// guarded on it (UPDATE ... WHERE state = From): if another event moved
	// the row meanwhile, Commit returns ErrGone and nothing changes. An
	// Update without From is refused.
	From engine.State
	// Events are appended to the audit log, in order.
	Events []engine.Event
	// Dequeue removes a queued walk-in; Enqueue adds one.
	Dequeue string
	Enqueue *Queued
}

// Store is what the calendar needs from persistence; adapter.Store implements
// it on store.Repository (ut-docs#7208). Bookings returns the current rows
// (never superseded ones, any state) that overlap [from, to). Booking returns
// a current row only: a superseded or missing row is "not found", so a
// repeated post of a move cannot act on the old row. A Store may also
// implement Eraser and ReceiptDesk (desk.go) to offer the erase and
// complete-with-receipt buttons.
type Store interface {
	Shop() (Shop, error)
	Staff() ([]Staff, error)
	Services() ([]ServiceInfo, error)
	Policy() (engine.Policy, error)
	Bookings(from, to time.Time) ([]Booking, error)
	Booking(id string) (Booking, bool, error)
	Queue() ([]Queued, error)
	Commit(Change) error
}

// Operator is the signed-in person, as the host sends it.
type Operator struct {
	UserID      string   `json:"user_id"`
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
}

// IsManager reports whether the host's role may do manager corrections. A
// missing operator (sign-in off) means "no role limits" (plugin-views.md).
func (o *Operator) IsManager() bool {
	if o == nil {
		return true
	}
	switch o.Role {
	case "manager", "admin", "owner":
		return true
	}
	return false
}

// ActorID is the id recorded on events; empty when nobody is signed in.
func (o *Operator) ActorID() string {
	if o == nil {
		return ""
	}
	return o.UserID
}

// Ask is a ui.view.ask or ui.action.ask payload. Now is set by the host.
type Ask struct {
	View     string            `json:"view"`
	Action   string            `json:"action"`
	Form     map[string]any    `json:"form"`
	Params   map[string]string `json:"params"`
	Locale   string            `json:"locale"`
	Operator *Operator         `json:"operator"`
	Now      time.Time         `json:"-"`
}

// Deps are the handler's collaborators.
type Deps struct {
	Store Store
	IDs   engine.IDGen
	// Route is the plugin's own calendar page, the redirect target of the
	// sell-screen overlay's "Open calendar" button.
	Route string
}
