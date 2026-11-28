package store

import (
	"context"
	"encoding/json"
	"time"
)

// QueueEntry is a walk-in waiting for a chair. Its first name lives only for
// the business day: PurgeQueue deletes it (appointment-v1 retention, N9).
type QueueEntry struct {
	ID          string
	Seq         int64
	Arrived     time.Time
	FirstName   string
	ServiceSKU  string
	DurationMin int
	BufferMin   int
	ResourceIDs []string // empty: any resource
}

// Enqueue adds a walk-in to the queue (one statement, atomic).
func (r *Repository) Enqueue(ctx context.Context, q QueueEntry) error {
	name, err := ValidateFirstName(q.FirstName)
	if err != nil {
		return err
	}
	if q.ID == "" || q.ServiceSKU == "" || q.DurationMin <= 0 || q.BufferMin < 0 {
		return ErrInvalidInput
	}
	ids := q.ResourceIDs
	if ids == nil {
		ids = []string{}
	}
	b, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(ctx, SQLInsertQueue, q.ID, q.Seq, q.Arrived.Unix(), name, q.ServiceSKU, q.DurationMin, q.BufferMin, string(b))
	return err
}

// Dequeue removes a queue entry; a missing one is not an error (idempotent).
func (r *Repository) Dequeue(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx, SQLDeleteQueue, id)
	return err
}

// Queue lists the waiting walk-ins in arrival order. Entries that arrived
// before since (the start of the business day) and entries already seated are
// not listed.
func (r *Repository) Queue(ctx context.Context, since time.Time) ([]QueueEntry, error) {
	rows, err := query(ctx, r.db, SQLListQueue, since.Unix())
	if err != nil {
		return nil, err
	}
	out := make([]QueueEntry, len(rows))
	for i, w := range rows {
		var ids []string
		_ = json.Unmarshal([]byte(w.str("resource_ids")), &ids)
		out[i] = QueueEntry{ID: w.str("id"), Seq: w.i64("seq"), Arrived: time.Unix(w.i64("arrived_utc"), 0).UTC(),
			FirstName: w.str("first_name"), ServiceSKU: w.str("service_sku"), DurationMin: int(w.i64("duration_min")),
			BufferMin: int(w.i64("buffer_min")), ResourceIDs: ids}
	}
	return out, nil
}

// PurgeQueue deletes the entries that arrived before the start of the shop's
// current business day, and those already seated. It returns the count.
func (r *Repository) PurgeQueue(ctx context.Context, before time.Time) (int, error) {
	n, err := r.db.Exec(ctx, SQLPurgeQueue, before.Unix())
	return int(n), err
}

// DayStart is the start of the shop's current business day (local midnight in
// the shop zone) as a UTC instant.
func (r *Repository) DayStart(ctx context.Context) (time.Time, error) {
	set, err := r.Settings(ctx)
	if err != nil {
		return time.Time{}, err
	}
	loc, err := set.Location()
	if err != nil {
		return time.Time{}, err
	}
	y, m, d := r.clock.Now().In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc).UTC(), nil
}
