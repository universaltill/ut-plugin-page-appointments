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
	SQLInsertLine        = `INSERT OR IGNORE INTO appointment_lines (appointment_id, line_no, sku, name_snapshot, qty, amount_minor, duration_min, buffer_min) VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8)`
	SQLGetAppointment    = `SELECT ` + apptCols + ` FROM appointments WHERE id = ?1`
	// SQLListCurrent returns the non-superseded rows whose hold interval
	// [start, end + buffer) meets [?1, ?2); ?3 is a resource id or ''.
	SQLListCurrent   = `SELECT ` + apptCols + ` FROM appointments a WHERE a.end_utc + a.buffer_min * 60 > ?1 AND a.start_utc < ?2 AND (?3 = '' OR a.resource_id = ?3) AND NOT EXISTS (SELECT 1 FROM appointments s WHERE s.supersedes_id = a.id) ORDER BY a.start_utc, a.id`
	SQLHasSuperseder = `SELECT id FROM appointments WHERE supersedes_id = ?1 LIMIT 1`
	SQLListLines     = `SELECT appointment_id, line_no, sku, name_snapshot, qty, amount_minor, duration_min, buffer_min FROM appointment_lines WHERE appointment_id = ?1 ORDER BY line_no`
	// SQLSetProjection moves the state projection; ?5 is the expected old state
	// (an optimistic check: 0 rows affected means someone else moved it).
	SQLSetProjection = `UPDATE appointments SET state = ?2, late_cancel = ?3, sale_ref = ?4 WHERE id = ?1 AND state = ?5`
	SQLInsertEvent   = `INSERT INTO appointment_events (id, appointment_id, at, actor_user_id, kind, from_state, to_state, reason, conflicting_ids) VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9)`
	SQLListEvents    = `SELECT id, appointment_id, at, actor_user_id, kind, from_state, to_state, reason, conflicting_ids FROM appointment_events WHERE appointment_id = ?1 ORDER BY at, rowid`

	// SQLChainInfo lists every appointment row with what the purge needs.
	SQLChainInfo = `SELECT id, supersedes_id, state, end_utc, client_first_name <> '' AS named FROM appointments ORDER BY id`
	SQLBlankName = `UPDATE appointments SET client_first_name = '' WHERE id = ?1 AND client_first_name <> ''`
	// SQLStaleProjection lists rows whose state projection differs from the
	// to_state of their last event (or that have no event).
	SQLStaleProjection = `SELECT a.id AS id, a.state AS state, COALESCE((SELECT e.to_state FROM appointment_events e WHERE e.appointment_id = a.id ORDER BY e.at DESC, e.rowid DESC LIMIT 1), '') AS last_state FROM appointments a ORDER BY a.id`
)
