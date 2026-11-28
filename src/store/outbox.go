package store

import "context"

// OutboxRow is one frozen appointment.completed payload.
type OutboxRow struct {
	AppointmentID string
	SaleRef       string
	Payload       []byte
}

// InsertOutbox stores the frozen payload once (a second call is a no-op).
func (r *Repository) InsertOutbox(ctx context.Context, appointmentID, saleRef string, payload []byte) error {
	_, err := r.db.Exec(ctx, SQLInsertOutbox, appointmentID, saleRef, string(payload))
	return err
}

// PendingOutbox lists up to 20 payloads event_publish has not accepted yet.
func (r *Repository) PendingOutbox(ctx context.Context) ([]OutboxRow, error) {
	rows, err := query(ctx, r.db, SQLPendingOutbox)
	if err != nil {
		return nil, err
	}
	out := make([]OutboxRow, len(rows))
	for i, w := range rows {
		out[i] = OutboxRow{AppointmentID: w.str("appointment_id"), SaleRef: w.str("sale_ref"), Payload: []byte(w.str("payload"))}
	}
	return out, nil
}

// MarkPublished records that event_publish accepted the payload.
func (r *Repository) MarkPublished(ctx context.Context, appointmentID string) error {
	_, err := r.db.Exec(ctx, SQLMarkPublished, appointmentID)
	return err
}

// UnoutboxedCompleted lists up to 20 current completed appointments that have
// no outbox row: the state moved, then the process stopped before the outbox
// write. The caller rebuilds the payload from the record.
func (r *Repository) UnoutboxedCompleted(ctx context.Context) ([]string, error) {
	rows, err := query(ctx, r.db, SQLUnoutboxed)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(rows))
	for i, w := range rows {
		out[i] = w.str("id")
	}
	return out, nil
}
