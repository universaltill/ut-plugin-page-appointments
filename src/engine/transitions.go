package engine

import (
	"strings"
	"time"
)

// allowedTransitions is the single source of truth for the state machine.
var allowedTransitions = map[State][]State{
	Booked:    {Confirmed, CheckedIn, CancelledByClient, CancelledByShop, NoShow},
	Confirmed: {CheckedIn, CancelledByClient, CancelledByShop, NoShow},
	CheckedIn: {InService, CancelledByClient, CancelledByShop},
	InService: {Completed},
}

func allowed(from, to State) bool {
	for _, s := range allowedTransitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// TransitionInput describes a requested state change.
type TransitionInput struct {
	To          State
	ActorUserID string
	SaleRef     string
	Now         time.Time
}

// Transition moves a to in.To. Validation order: table, no_show grace,
// completed sale ref, then late-cancel flagging. On error the input
// appointment is returned unchanged with a zero Event.
func Transition(a Appointment, in TransitionInput, p Policy, ids IDGen) (Appointment, Event, error) {
	if !allowed(a.State, in.To) {
		return a, Event{}, ErrTransitionNotAllowed
	}
	out := a
	switch in.To {
	case NoShow:
		if p.NoShowGraceMin < 0 {
			return a, Event{}, ErrInvalidPolicy
		}
		if in.Now.Before(a.Start.Add(minutes(p.NoShowGraceMin))) {
			return a, Event{}, ErrNoShowTooEarly
		}
	case Completed:
		ref := strings.TrimSpace(in.SaleRef)
		if ref == "" {
			return a, Event{}, ErrSaleRefRequired
		}
		out.SaleRef = ref
	case CancelledByClient:
		if p.LateCancelHours < 0 {
			return a, Event{}, ErrInvalidPolicy
		}
		out.LateCancel = p.LateCancelHours > 0 &&
			a.Start.Sub(in.Now) < time.Duration(p.LateCancelHours)*time.Hour
	}
	out.State = in.To
	ev := Event{ID: ids(), AppointmentID: a.ID, ActorUserID: in.ActorUserID, At: in.Now,
		Kind: KindStateChange, FromState: a.State, ToState: in.To}
	return out, ev, nil
}

// NewWalkIn turns a draft into a checked-in own walk-in and returns its
// creation event (FromState "").
func NewWalkIn(a Appointment, actorUserID string, now time.Time, ids IDGen) (Appointment, Event, error) {
	if a.ID == "" || a.ResourceID == "" || !a.End.After(a.Start) || a.BufferMin < 0 {
		return Appointment{}, Event{}, ErrInvalid
	}
	a.State = CheckedIn
	a.WalkIn = true
	a.Source = "own"
	a.CreatedAt = now
	a.Lines = append([]Line(nil), a.Lines...)
	ev := Event{ID: ids(), AppointmentID: a.ID, ActorUserID: actorUserID, At: now,
		Kind: KindStateChange, FromState: "", ToState: CheckedIn}
	return a, ev, nil
}

// Supersede builds the replacement row for a reschedule. The caller runs
// CheckConflict on it (which ignores the superseded row).
func Supersede(old Appointment, newStart time.Time, newResourceID, newID string, ids IDGen) (Appointment, error) {
	if old.State != Booked && old.State != Confirmed && old.State != CheckedIn {
		return Appointment{}, ErrTransitionNotAllowed
	}
	if newResourceID == "" {
		return Appointment{}, ErrInvalid
	}
	if newID == "" && ids != nil {
		newID = ids()
	}
	if newID == "" {
		return Appointment{}, ErrInvalid
	}
	n := old
	n.ID = newID
	n.SupersedesID = old.ID
	n.ResourceID = newResourceID
	n.Start = newStart
	n.End = newStart.Add(old.End.Sub(old.Start))
	n.Lines = append([]Line(nil), old.Lines...)
	return n, nil
}
