package app

import (
	"context"
	"encoding/json"
	"time"

	"github.com/universaltill/universal-till/sdk/plugin"

	"github.com/universaltill/ut-plugin-page-appointments/src/calendar"
	"github.com/universaltill/ut-plugin-page-appointments/src/snapshot"
	"github.com/universaltill/ut-plugin-page-appointments/src/store"
)

// Records is the plugin-records channel (records_role, snapshot_set,
// snapshot_get; permission records:sync, manifest records_forward.snapshots).
// A nil Records turns the replica snapshot off.
type Records interface {
	Role() (string, error)
	SnapshotSet(collection string, v any) (stored bool, err error)
	SnapshotGet(collection string, out any) (found bool, err error)
}

// HostRecords is Records over the Go guest SDK (the real host functions in
// the wasm guest, the fake host in tests).
type HostRecords struct{}

func (HostRecords) Role() (string, error)                     { return plugin.RecordsRole() }
func (HostRecords) SnapshotSet(c string, v any) (bool, error) { return plugin.SnapshotSet(c, v) }
func (HostRecords) SnapshotGet(c string, out any) (bool, error) {
	return plugin.SnapshotGet(c, out)
}

func (d Deps) isReplica() bool {
	if d.Records == nil {
		return false
	}
	role, err := d.Records.Role()
	return err == nil && role == "replica"
}

// publishSnapshot rebuilds the calendar snapshot and hands it to snapshot_set
// when it changed (design 7207 section 4). Only the main till writes; on a
// replica the call is skipped. An unchanged rebuild costs no write, so the
// replica pull does not move; an idle till refreshes as_of hourly.
func publishSnapshot(ctx context.Context, repo *store.Repository, d Deps, now time.Time) error {
	if d.Records == nil || d.isReplica() {
		return nil
	}
	set, err := repo.Settings(ctx)
	if err != nil {
		return err
	}
	zone, err := set.Location()
	if err != nil {
		return err
	}
	res, err := repo.Resources(ctx)
	if err != nil {
		return err
	}
	from, to := snapshot.Window(now, zone)
	bks, err := repo.Current(ctx, from, to, "")
	if err != nil {
		return err
	}
	next, err := snapshot.Build(snapshot.Input{Now: now, Zone: zone, Resources: res, Bookings: bks})
	if err != nil {
		return err
	}
	var prev json.RawMessage
	if found, err := d.Records.SnapshotGet(snapshot.Collection, &prev); err == nil && found &&
		!snapshot.Changed(prev, next, now) {
		return nil
	}
	_, err = d.Records.SnapshotSet(snapshot.Collection, json.RawMessage(next))
	return err
}

// replicaAnswer draws the read-only calendar from the main till's snapshot.
func replicaAnswer(d Deps, p askPayload, now time.Time) (any, error) {
	var raw json.RawMessage
	found, err := d.Records.SnapshotGet(snapshot.Collection, &raw)
	if err != nil {
		found = false // snapshot_get refused: show the empty state, never block
	}
	var pl snapshot.Payload
	if found {
		if pl, err = snapshot.Decode(raw); err != nil {
			found = false
		}
	}
	doc := calendar.ReplicaDocument(pl, found, calendar.ReplicaAsk{Action: p.Action, Params: p.Params, Locale: p.Locale, Now: now})
	return calendar.Answer{Document: doc}, nil
}
