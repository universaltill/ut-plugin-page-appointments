package store

// The complete set of statements the repository issues. They are exported so
// the statement-level test double (store/memdb) dispatches on the exact text.
const (
	SQLUpsertResource = `INSERT INTO resources (id, kind, display_name, colour, status, logbook_staff_ref) VALUES (?1, ?2, ?3, ?4, ?5, ?6) ON CONFLICT(id) DO UPDATE SET kind = ?2, display_name = ?3, colour = ?4, status = ?5, logbook_staff_ref = ?6`
	SQLListResources  = `SELECT id, kind, display_name, colour, status, logbook_staff_ref FROM resources ORDER BY id`

	SQLDeleteWorkingHours = `DELETE FROM working_hours WHERE resource_id = ?1`
	SQLInsertWorkingHour  = `INSERT INTO working_hours (resource_id, weekday, start_min, end_min) VALUES (?1, ?2, ?3, ?4)`
	SQLListWorkingHours   = `SELECT resource_id, weekday, start_min, end_min FROM working_hours ORDER BY resource_id, weekday, start_min`

	SQLInsertTimeOff = `INSERT INTO time_off (id, resource_id, from_utc, to_utc, reason) VALUES (?1, ?2, ?3, ?4, ?5)`
	SQLDeleteTimeOff = `DELETE FROM time_off WHERE id = ?1`
	SQLListTimeOff   = `SELECT id, resource_id, from_utc, to_utc, reason FROM time_off ORDER BY resource_id, from_utc, id`

	SQLUpsertService         = `INSERT INTO services (sku, duration_min, buffer_min, all_resources, active) VALUES (?1, ?2, ?3, ?4, ?5) ON CONFLICT(sku) DO UPDATE SET duration_min = ?2, buffer_min = ?3, all_resources = ?4, active = ?5`
	SQLDeleteServiceResource = `DELETE FROM service_resources WHERE sku = ?1`
	SQLInsertServiceResource = `INSERT INTO service_resources (sku, resource_id) VALUES (?1, ?2)`
	SQLListServices          = `SELECT sku, duration_min, buffer_min, all_resources, active FROM services ORDER BY sku`
	SQLListServiceResources  = `SELECT sku, resource_id FROM service_resources ORDER BY sku, resource_id`

	SQLUpsertSetting = `INSERT INTO settings (key, value) VALUES (?1, ?2) ON CONFLICT(key) DO UPDATE SET value = ?2`
	SQLListSettings  = `SELECT key, value FROM settings ORDER BY key`

	apptCols = `id, origin_till_id, source, resource_id, start_utc, end_utc, buffer_min, walk_in, state, client_first_name, currency, sale_ref, late_cancel, supersedes_id, created_at`

	SQLInsertAppointment = `INSERT INTO appointments (` + apptCols + `) VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11, ?12, ?13, ?14, ?15)`
	// SQLInsertAppointmentFree is SQLInsertAppointment guarded, in the same
	// statement, against an overlapping row on the same resource that holds
	// time (booked, confirmed, checked_in, in_service; never superseded;
	// neither the row itself nor the row it supersedes ?14). The host runs
	// several events of one plugin at once and has no transaction, so the
	// calendar's conflict check and the write are otherwise two calls: 0 rows
	// affected means another event took the slot in between. The overlap is
	// the engine's: [start, end + buffer) meets [?5, ?6 + ?7 min).
	SQLInsertAppointmentFree = `INSERT INTO appointments (` + apptCols + `) SELECT ?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11, ?12, ?13, ?14, ?15 WHERE NOT EXISTS (SELECT 1 FROM appointments e WHERE e.resource_id = ?4 AND e.id <> ?1 AND e.id <> ?14 AND e.state IN ('booked', 'confirmed', 'checked_in', 'in_service') AND e.start_utc < ?6 + ?7 * 60 AND e.end_utc + e.buffer_min * 60 > ?5 AND NOT EXISTS (SELECT 1 FROM appointments s WHERE s.supersedes_id = e.id))`
	SQLInsertLine            = `INSERT OR IGNORE INTO appointment_lines (appointment_id, line_no, sku, name_snapshot, qty, amount_minor, duration_min, buffer_min) VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8)`
	SQLGetAppointment        = `SELECT ` + apptCols + ` FROM appointments WHERE id = ?1`
	// SQLListCurrent returns the non-superseded rows whose hold interval
	// [start, end + buffer) meets [?1, ?2); ?3 is a resource id or ''.
	SQLListCurrent   = `SELECT ` + apptCols + ` FROM appointments a WHERE a.end_utc + a.buffer_min * 60 > ?1 AND a.start_utc < ?2 AND (?3 = '' OR a.resource_id = ?3) AND NOT EXISTS (SELECT 1 FROM appointments s WHERE s.supersedes_id = a.id) ORDER BY a.start_utc, a.id`
	SQLHasSuperseder = `SELECT id FROM appointments WHERE supersedes_id = ?1 LIMIT 1`
	SQLListLines     = `SELECT appointment_id, line_no, sku, name_snapshot, qty, amount_minor, duration_min, buffer_min FROM appointment_lines WHERE appointment_id = ?1 ORDER BY line_no`
	// SQLSetProjection moves the state projection; ?5 is the expected old state
	// (an optimistic check: 0 rows affected means someone else moved it).
	SQLSetProjection = `UPDATE appointments SET state = ?2, late_cancel = ?3, sale_ref = ?4 WHERE id = ?1 AND state = ?5`
	SQLInsertEvent   = `INSERT INTO appointment_events (id, appointment_id, at, actor_user_id, kind, from_state, to_state, reason, conflicting_ids) VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9)`
	SQLListEvents    = `SELECT id, appointment_id, at, actor_user_id, kind, from_state, to_state, reason, conflicting_ids FROM appointment_events WHERE appointment_id = ?1 ORDER BY rowid`

	// The purge, erasure and reconcile statements never read the table in one
	// result: the host caps a result at 1 MiB (plugindb MaxResultBytes). They
	// page by keyset (id > ?1 LIMIT ?2) and select only what they need.
	//
	// SQLNamedPage lists the ids of rows that still carry a first name.
	SQLNamedPage = `SELECT id FROM appointments WHERE client_first_name <> '' AND id > ?1 ORDER BY id LIMIT ?2`
	// SQLChainRow reads what the purge needs of one row.
	SQLChainRow  = `SELECT id, supersedes_id, state, end_utc, client_first_name <> '' AS named FROM appointments WHERE id = ?1`
	SQLBlankName = `UPDATE appointments SET client_first_name = '' WHERE id = ?1 AND client_first_name <> ''`
	// SQLStaleProjection lists, a page at a time, rows whose state projection
	// differs from the to_state of their last event (or that have no event).
	// "Last" is insert order (rowid), never the wall clock, which can step back.
	SQLStaleProjection = `SELECT a.id AS id, a.state AS state, COALESCE((SELECT e.to_state FROM appointment_events e WHERE e.appointment_id = a.id ORDER BY e.rowid DESC LIMIT 1), '') AS last_state FROM appointments a WHERE a.id > ?1 AND a.state <> COALESCE((SELECT e.to_state FROM appointment_events e WHERE e.appointment_id = a.id ORDER BY e.rowid DESC LIMIT 1), '') ORDER BY a.id LIMIT ?2`

	// The walk-in queue. A queue entry that has become an appointment (same
	// id, see calendar queueSeat) is never listed, so a crash between the
	// insert and the dequeue cannot leave a walk-in both seated and queued.
	SQLInsertQueue = `INSERT INTO walkin_queue (id, seq, arrived_utc, first_name, service_sku, duration_min, buffer_min, resource_ids) VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8)`
	SQLListQueue   = `SELECT id, seq, arrived_utc, first_name, service_sku, duration_min, buffer_min, resource_ids FROM walkin_queue q WHERE q.arrived_utc >= ?1 AND NOT EXISTS (SELECT 1 FROM appointments a WHERE a.id = q.id) ORDER BY q.seq, q.id LIMIT 500`
	SQLDeleteQueue = `DELETE FROM walkin_queue WHERE id = ?1`
	// SQLPurgeQueue removes every entry that arrived before ?1 (the start of
	// the shop's business day) and every entry already seated: a queue name has
	// no retention beyond the day (N9).
	SQLPurgeQueue = `DELETE FROM walkin_queue WHERE arrived_utc < ?1 OR id IN (SELECT id FROM appointments)`

	// The completion outbox: the frozen appointment.completed payload, written
	// after the state moved to completed (the host has no transaction), and
	// marked once event_publish accepted it. A completed row without an outbox
	// row (a crash in between) is found by SQLUnoutboxed and rebuilt.
	SQLInsertOutbox  = `INSERT OR IGNORE INTO completion_outbox (appointment_id, sale_ref, payload, published) VALUES (?1, ?2, ?3, 0)`
	SQLPendingOutbox = `SELECT appointment_id, sale_ref, payload FROM completion_outbox WHERE published = 0 ORDER BY appointment_id LIMIT 20`
	SQLMarkPublished = `UPDATE completion_outbox SET published = 1 WHERE appointment_id = ?1`
	SQLUnoutboxed    = `SELECT a.id AS id FROM appointments a WHERE a.state = 'completed' AND a.sale_ref <> '' AND NOT EXISTS (SELECT 1 FROM completion_outbox o WHERE o.appointment_id = a.id) AND NOT EXISTS (SELECT 1 FROM appointments s WHERE s.supersedes_id = a.id) ORDER BY a.id LIMIT 20`
)
