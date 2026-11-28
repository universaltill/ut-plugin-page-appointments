// Package calendar builds the Appointments plugin's day calendar as plugin
// view documents (ut-docs reference/plugin-views.md) and answers the
// ui.action.ask posts that act on it (#7114, ADR-0221).
//
// There is no calendar component in the view format, so the day is one table
// per staff member, with one button per bookable row (the till draws buttons
// at its own touch size; the format has no size field). Everything here is
// pure: the clock is the Ask's Now, ids come from an engine.IDGen and all
// persistence goes through the Store interface (adapter.Store on the plugin's
// own database; tests use an in-memory fake).
//
// Binding build rules (appointment-v1.md): no free-text note anywhere, a
// first name of at most 40 characters without digits or "@", an override
// reason of at most 500 runes with a "no client or health details" hint, and
// a confirm dialog before a no-show on a provider booking.
package calendar
