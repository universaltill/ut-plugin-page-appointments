package engine

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// MaxOverrideReasonRunes bounds an override reason.
const MaxOverrideReasonRunes = 500

// Current drops every row that another row in rows supersedes (names in its
// SupersedesID). A superseded row never holds time (appointment-v1 section 3),
// so every reader of "existing" appointments (CheckConflict, Availability,
// EstimateNearestFree) applies it. A store that can only return part of a
// chain must filter superseded rows itself. The input is not modified and the
// order of the remaining rows is kept.
func Current(rows []Appointment) []Appointment {
	gone := make(map[string]bool, len(rows))
	for _, r := range rows {
		if r.SupersedesID != "" {
			gone[r.SupersedesID] = true
		}
	}
	out := make([]Appointment, 0, len(rows))
	for _, r := range rows {
		if !gone[r.ID] {
			out = append(out, r)
		}
	}
	return out
}

// WriteResult is the outcome of CheckConflict.
type WriteResult struct {
	Events         []Event
	ConflictingIDs []string
}

// CheckConflict decides whether cand may be written next to existing. It does
// not check working hours or time off (a manager may book outside them). A nil
// ov means no override is attempted. With conflicts and no valid override it
// returns ErrConflict (ConflictingIDs filled).
func CheckConflict(cand Appointment, existing []Appointment, p Policy, ov *Override,
	clock Clock, ids IDGen) (WriteResult, error) {
	if cand.ID == "" || cand.ResourceID == "" || !cand.End.After(cand.Start) || cand.BufferMin < 0 {
		return WriteResult{}, ErrInvalid
	}
	end := cand.holdEnd()
	var conflicts []string
	for _, e := range Current(existing) {
		if e.ResourceID != cand.ResourceID || !e.State.Holds() || e.ID == cand.ID ||
			(cand.SupersedesID != "" && e.ID == cand.SupersedesID) {
			continue
		}
		if overlaps(cand.Start, end, e.Start, e.holdEnd()) {
			conflicts = append(conflicts, e.ID)
		}
	}
	if len(conflicts) == 0 {
		return WriteResult{}, nil
	}
	sort.Strings(conflicts)
	res := WriteResult{ConflictingIDs: conflicts}
	if ov == nil {
		return res, ErrConflict
	}
	if !p.AllowDoubleBooking || !ov.ByManager {
		return res, ErrOverrideNotAllowed
	}
	reason := strings.TrimSpace(ov.Reason)
	if reason == "" {
		return res, ErrReasonRequired
	}
	if utf8.RuneCountInString(reason) > MaxOverrideReasonRunes {
		return res, ErrInvalid
	}
	// An override event with no actor is a weak audit record (F14).
	if strings.TrimSpace(ov.ActorUserID) == "" {
		return res, ErrInvalid
	}
	res.Events = []Event{{
		ID: ids(), AppointmentID: cand.ID, ActorUserID: ov.ActorUserID, At: clock.Now(),
		Kind: KindConflictOverride, FromState: cand.State, ToState: cand.State,
		Reason: reason, ConflictingIDs: conflicts,
	}}
	return res, nil
}
