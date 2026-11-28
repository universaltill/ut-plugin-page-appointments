package store

import (
	"context"
	"time"

	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
)

type chainRow struct {
	id, supersedes string
	state          engine.State
	end            time.Time
	named          bool
}

// chains loads every row and links them: the chain of a row is every row
// reachable through supersedes links; its head is the row nothing supersedes.
func (r *Repository) chains(ctx context.Context) (rows map[string]*chainRow, head map[string]string, err error) {
	raw, err := query(ctx, r.db, SQLChainInfo)
	if err != nil {
		return nil, nil, err
	}
	rows = make(map[string]*chainRow, len(raw))
	next := map[string]string{} // id -> the row that supersedes it
	for _, w := range raw {
		c := &chainRow{id: w.str("id"), supersedes: w.str("supersedes_id"), state: engine.State(w.str("state")),
			end: time.Unix(w.i64("end_utc"), 0).UTC(), named: w.flag("named")}
		rows[c.id] = c
		if c.supersedes != "" {
			next[c.supersedes] = c.id
		}
	}
	head = make(map[string]string, len(rows))
	for id := range rows {
		h := id
		for i := 0; i <= len(rows); i++ { // bounded: a cycle cannot loop forever
			n, ok := next[h]
			if !ok {
				break
			}
			h = n
		}
		head[id] = h
	}
	return rows, head, nil
}

// PurgeResult counts what a purge did.
type PurgeResult struct {
	Chains int // chains whose names were blanked
	Rows   int // rows blanked
}

// Purge blanks client_first_name, in place, on every row of each appointment
// chain whose visit ended (or whose cancellation happened) at least the shop's
// retention period ago (default 90 days, at most 730). It writes one purge
// event per chain, without the name. It is idempotent and safe to call on
// every event or timer.
func (r *Repository) Purge(ctx context.Context) (PurgeResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	set, err := r.Settings(ctx)
	if err != nil {
		return PurgeResult{}, err
	}
	if err := ValidateRetentionDays(set.RetentionDays); err != nil {
		return PurgeResult{}, err
	}
	rows, head, err := r.chains(ctx)
	if err != nil {
		return PurgeResult{}, err
	}
	cutoff := r.clock.Now().Add(-time.Duration(set.RetentionDays) * 24 * time.Hour)
	byHead := map[string][]string{}
	for id, c := range rows {
		if c.named {
			byHead[head[id]] = append(byHead[head[id]], id)
		}
	}
	var res PurgeResult
	for _, h := range sortedKeys(byHead) {
		hc := rows[h]
		ref := hc.end
		if hc.state == engine.CancelledByClient || hc.state == engine.CancelledByShop {
			evs, err := r.Events(ctx, h)
			if err != nil {
				return res, err
			}
			if len(evs) > 0 {
				ref = evs[len(evs)-1].At
			}
		}
		if ref.After(cutoff) {
			continue
		}
		n, err := r.blank(ctx, h, byHead[h], hc.state, "", ReasonRetention)
		res.Rows += n
		if n > 0 {
			res.Chains++
		}
		if err != nil {
			return res, err
		}
	}
	return res, nil
}

// Erase blanks the first name on every row of the appointment's chain at once
// (a client's erasure request) and writes a purge event naming the actor.
func (r *Repository) Erase(ctx context.Context, appointmentID, actorUserID string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rows, head, err := r.chains(ctx)
	if err != nil {
		return 0, err
	}
	if _, ok := rows[appointmentID]; !ok {
		return 0, ErrNotFound
	}
	h := head[appointmentID]
	var named []string
	for id, c := range rows {
		if head[id] == h && c.named {
			named = append(named, id)
		}
	}
	sortStrings(named)
	return r.blank(ctx, h, named, rows[h].state, actorUserID, ReasonErasure)
}

func (r *Repository) blank(ctx context.Context, headID string, ids []string, state engine.State, actor, reason string) (int, error) {
	sortStrings(ids)
	total := 0
	for _, id := range ids {
		n, err := r.db.Exec(ctx, SQLBlankName, id)
		if err != nil {
			return total, err
		}
		total += int(n)
	}
	if total == 0 {
		return 0, nil
	}
	ev := engine.Event{ID: r.ids(), AppointmentID: headID, ActorUserID: actor, At: r.clock.Now(), Kind: KindPurge,
		FromState: state, ToState: state, Reason: reason}
	return total, r.insertEvent(ctx, ev)
}
