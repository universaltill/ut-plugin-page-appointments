// Package engine is the pure scheduling core of the Appointments plugin.
//
// Purity rules (enforced by purity_test.go): no time.Now (a Clock or an
// explicit "now" argument is passed in), no network, disk, database, random or
// goroutine use, and every amount is money.Money. Output slices are sorted by
// a stated key, never by map iteration order. The engine returns events as
// values; it never persists or publishes them.
package engine
