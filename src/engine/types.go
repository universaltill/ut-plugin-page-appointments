package engine

import (
	"time"

	"github.com/universaltill/ut-plugin-page-appointments/src/money"
)

// State is an appointment lifecycle state.
type State string

// Appointment states.
const (
	Booked            State = "booked"
	Confirmed         State = "confirmed"
	CheckedIn         State = "checked_in"
	InService         State = "in_service"
	Completed         State = "completed"
	CancelledByClient State = "cancelled_by_client"
	CancelledByShop   State = "cancelled_by_shop"
	NoShow            State = "no_show"
)

// Holds reports whether an appointment in this state occupies its resource.
func (s State) Holds() bool {
	return s == Booked || s == Confirmed || s == CheckedIn || s == InService
}

// Terminal reports whether no transition leaves this state.
func (s State) Terminal() bool {
	return s == Completed || s == CancelledByClient || s == CancelledByShop || s == NoShow
}

// Date is a civil date in the shop's time zone.
type Date struct {
	Y int
	M time.Month
	D int
}

// Window is a working-hours window in minutes since local midnight,
// 0 <= StartMin < EndMin <= 1440.
type Window struct{ StartMin, EndMin int }

// TimeOff is a half-open UTC interval [From, To) in which a resource is away.
type TimeOff struct {
	From, To time.Time
	Reason   string
}

// Resource is a bookable chair, room or person.
type Resource struct {
	ID, Kind     string
	Active       bool
	WorkingHours map[time.Weekday][]Window
	TimeOff      []TimeOff
}

// Service is a bookable service.
type Service struct {
	SKU                    string
	DurationMin, BufferMin int
	ResourceIDs            []string // eligible resources; ignored when All
	All                    bool     // every active resource is eligible
	Active                 bool
}

// Line is one service line of an appointment. Amount is the line total (the
// quantity is already applied).
type Line struct {
	SKU, Name              string
	Qty                    int
	Amount                 money.Money
	DurationMin, BufferMin int
}

// Appointment is one booking. Start and End are UTC; End is Start plus the
// summed line durations (buffer excluded). It holds [Start, End+BufferMin).
type Appointment struct {
	ID, OriginTillID, Source, ResourceID string // Source: "own" | "provider"
	Start, End                           time.Time
	BufferMin                            int // buffer of the last line
	State                                State
	Lines                                []Line
	WalkIn                               bool
	Deposit                              money.Money
	SaleRef                              string
	LateCancel                           bool
	SupersedesID                         string
	CreatedAt                            time.Time
}

// holdEnd is the end of the interval the appointment occupies.
func (a Appointment) holdEnd() time.Time {
	return a.End.Add(time.Duration(a.BufferMin) * time.Minute)
}

// Event is an audit event returned by the engine as a value.
type Event struct {
	ID, AppointmentID, ActorUserID string
	At                             time.Time
	Kind                           string // "state_change" | "conflict_override"
	FromState, ToState             State
	Reason                         string
	ConflictingIDs                 []string // sorted ascending
}

// Event kinds.
const (
	KindStateChange      = "state_change"
	KindConflictOverride = "conflict_override"
)

// Clock supplies the current time; the engine never reads the system clock.
type Clock interface{ Now() time.Time }

// IDGen produces unique ids.
type IDGen func() string

// Override asks to write a conflicting appointment anyway. The host asserts
// ByManager from its permission layer; the engine only records the reason.
type Override struct {
	ByManager   bool
	Reason      string
	ActorUserID string
}

// Policy holds the shop's scheduling settings.
type Policy struct {
	SlotStepMin        int  // default 15; one of 5,10,15,20,30,60
	NoShowGraceMin     int  // default 15; < 0 invalid
	LateCancelHours    int  // default 24; 0 disables; < 0 invalid
	AllowDoubleBooking bool // default false
	OverrunMin         int  // default 5; < 0 invalid
}

// DefaultPolicy returns the documented defaults.
func DefaultPolicy() Policy {
	return Policy{SlotStepMin: 15, NoShowGraceMin: 15, LateCancelHours: 24, OverrunMin: 5}
}

// Validate checks the policy ranges.
func (p Policy) Validate() error {
	switch p.SlotStepMin {
	case 5, 10, 15, 20, 30, 60:
	default:
		return ErrInvalidPolicy
	}
	if p.NoShowGraceMin < 0 || p.LateCancelHours < 0 || p.OverrunMin < 0 {
		return ErrInvalidPolicy
	}
	return nil
}
