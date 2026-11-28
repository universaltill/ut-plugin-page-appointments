package store

import (
	"context"
	"time"

	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
)

// DefaultPageSize is the number of rows a purge or reconcile page reads. A
// page row is small (an id, or an id with two states), so a page stays far
// below the host's 1 MiB result cap.
const DefaultPageSize = 200

// maxChain bounds a walk along supersedes links (a cycle cannot loop forever).
const maxChain = 10000

// SetPageSize changes the page size of Purge and Reconcile (tests use a tiny
// one); n < 1 restores the default.
func (r *Repository) SetPageSize(n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n < 1 {
		n = DefaultPageSize
	}
	r.pageSize = n
}

func (r *Repository) page() int {
	if r.pageSize < 1 {
		return DefaultPageSize
	}
	return r.pageSize
}

type chainRow struct {
	id, supersedes string
	state          engine.State
	end            time.Time
	named          bool
}

func (r *Repository) chainRow(ctx context.Context, id string) (*chainRow, error) {
	rows, err := query(ctx, r.db, SQLChainRow, id)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	w := rows[0]
	return &chainRow{id: w.str("id"), supersedes: w.str("supersedes_id"), state: engine.State(w.str("state")),
		end: time.Unix(w.i64("end_utc"), 0).UTC(), named: w.flag("named")}, nil
}

// chainOf resolves one row's chain without reading the table: forward along
// the superseder links to the head (the row nothing supersedes), then back
// along supersedes_id to the oldest row. It returns the head and every row.
func (r *Repository) chainOf(ctx context.Context, id string) (head *chainRow, all []*chainRow, err error) {
	h := id
	for i := 0; ; i++ {
		if i > maxChain {
			return nil, nil, ErrInvalidInput
		}
		rows, err := query(ctx, r.db, SQLHasSuperseder, h)
		if err != nil {
			return nil, nil, err
		}
		if len(rows) == 0 {
			break
		}
		h = rows[0].str("id")
	}
	seen := map[string]bool{}
	for cur := h; cur != "" && !seen[cur]; {
		seen[cur] = true
		c, err := r.chainRow(ctx, cur)
		if err != nil {
			return nil, nil, err
		}
		if head == nil {
			head = c
		}
		all = append(all, c)
		cur = c.supersedes
		if len(all) > maxChain {
			return nil, nil, ErrInvalidInput
		}
	}
	return head, all, nil
}

func namedIDs(all []*chainRow) []string {
	var out []string
	for _, c := range all {
		if c.named {
			out = append(out, c.id)
		}
	}
	return out
}

// PurgeResult counts what a purge did.
type PurgeResult struct {
	Chains int // chains whose names were blanked
	Rows   int // rows blanked
}

// Purge blanks client_first_name, in place, on every row of each appointment
// chain whose visit ended (or whose cancellation happened) at least the shop's
// retention period ago (default 90 days, at most 730). It writes one purge
// event per chain, without the name, before it blanks. It is idempotent. It
// pages by keyset over the rows that still carry a name, so it works at any
// table size under the host's result cap. Nothing calls it yet: a trigger (for
// example on every event) is part of wiring the slice to a till.
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
	cutoff := r.clock.Now().Add(-time.Duration(set.RetentionDays) * 24 * time.Hour)
	var res PurgeResult
	seen := map[string]bool{} // rows of chains already looked at in this run
	after := ""
	for {
		page, err := query(ctx, r.db, SQLNamedPage, after, r.page())
		if err != nil {
			return res, err
		}
		for _, w := range page {
			id := w.str("id")
			after = id
			if seen[id] {
				continue
			}
			head, all, err := r.chainOf(ctx, id)
			if err != nil {
				return res, err
			}
			for _, c := range all {
				seen[c.id] = true
			}
			ref := head.end
			if head.state == engine.CancelledByClient || head.state == engine.CancelledByShop {
				evs, err := r.Events(ctx, head.id)
				if err != nil {
					return res, err
				}
				for i := len(evs) - 1; i >= 0; i-- { // last event in insert order that is not a purge
					if evs[i].Kind != KindPurge {
						ref = evs[i].At
						break
					}
				}
			}
			if ref.After(cutoff) {
				continue
			}
			n, err := r.blank(ctx, head.id, namedIDs(all), head.state, "", ReasonRetention)
			res.Rows += n
			if n > 0 {
				res.Chains++
			}
			if err != nil {
				return res, err
			}
		}
		if len(page) < r.page() {
			return res, nil
		}
	}
}

// Erase blanks the first name on every row of the appointment's chain at once
// (a client's erasure request) and writes a purge event naming the actor. It
// loads that one chain, never the table.
func (r *Repository) Erase(ctx context.Context, appointmentID, actorUserID string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	head, all, err := r.chainOf(ctx, appointmentID)
	if err != nil {
		return 0, err
	}
	return r.blank(ctx, head.id, namedIDs(all), head.state, actorUserID, ReasonErasure)
}

// blank writes the purge event first (it holds no name), then blanks the
// rows. A crash in between leaves the event and a still-named chain: the
// rerun finds the event and blanks without writing a second one.
func (r *Repository) blank(ctx context.Context, headID string, ids []string, state engine.State, actor, reason string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	sortStrings(ids)
	evs, err := r.Events(ctx, headID)
	if err != nil {
		return 0, err
	}
	written := false
	for _, e := range evs {
		if e.Kind == KindPurge && e.Reason == reason {
			written = true
		}
	}
	if !written {
		ev := engine.Event{ID: r.ids(), AppointmentID: headID, ActorUserID: actor, At: r.clock.Now(), Kind: KindPurge,
			FromState: state, ToState: state, Reason: reason}
		if err := r.insertEvent(ctx, ev); err != nil {
			return 0, err
		}
	}
	total := 0
	for _, id := range ids {
		n, err := r.db.Exec(ctx, SQLBlankName, id)
		if err != nil {
			return total, err
		}
		total += int(n)
	}
	return total, nil
}
