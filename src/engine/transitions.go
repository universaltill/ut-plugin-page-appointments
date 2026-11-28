package engine

import (
	"strings"
	"time"
	"unicode/utf8"
)

// allowedTransitions is the single source of truth for the state machine.
var allowedTransitions = map[State][]State{
	Booked:    {Confirmed, CheckedIn, CancelledByClient, CancelledByShop, NoShow},
	Confirmed: {CheckedIn, CancelledByClient, CancelledByShop, NoShow},
	// CheckedIn -> Booked undoes a mis-tap; the corrections below are additive
	// (appointment-v1 section 2, ADR-0221 B8, legal finding 7).
	CheckedIn: {InService, Booked, CancelledByClient, CancelledByShop},
	InService: {Completed, CancelledByShop},
	NoShow:    {CheckedIn},
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
	// Reason is mandatory for the two corrections no_show -> checked_in and
	// in_service -> cancelled_by_shop (at most MaxOverrideReasonRunes). It is
	// trimmed and recorded on the event.
	Reason string
	// ByManager is asserted by the host's permission layer; no_show ->
	// checked_in needs it.
	ByManager bool
}

// Transition moves a to in.To. Validation order: table, correction rules
// (manager, reason, actor), no_show grace,
// completed sale ref, then late-cancel flagging. On error the input
// appointment is returned unchanged with a zero Event.
func Transition(a Appointment, in TransitionInput, p Policy, ids IDGen) (Appointment, Event, error) {
	if !allowed(a.State, in.To) {
		return a, Event{}, ErrTransitionNotAllowed
	}
	out := a
	reason := ""
	if (a.State == NoShow && in.To == CheckedIn) || (a.State == InService && in.To == CancelledByShop) {
		if a.State == NoShow && !in.ByManager {
			return a, Event{}, ErrOverrideNotAllowed
		}
		reason = strings.TrimSpace(in.Reason)
		if reason == "" {
			return a, Event{}, ErrReasonRequired
		}
		if utf8.RuneCountInString(reason) > MaxOverrideReasonRunes || in.ActorUserID == "" {
			return a, Event{}, ErrInvalid
		}
	}
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
		Kind: KindStateChange, FromState: a.State, ToState: in.To, Reason: reason}
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
