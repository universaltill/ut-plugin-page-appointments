CREATE TABLE appointments (
  id TEXT PRIMARY KEY,
  origin_till_id TEXT NOT NULL,
  source TEXT NOT NULL CHECK (source IN ('own', 'provider')),
  resource_id TEXT NOT NULL,
  start_utc INTEGER NOT NULL,
  end_utc INTEGER NOT NULL,
  buffer_min INTEGER NOT NULL CHECK (buffer_min >= 0),
  walk_in INTEGER NOT NULL CHECK (walk_in IN (0, 1)),
  state TEXT NOT NULL CHECK (state IN ('booked', 'confirmed', 'checked_in', 'in_service', 'completed', 'cancelled_by_client', 'cancelled_by_shop', 'no_show')),
  client_first_name TEXT NOT NULL DEFAULT '' CHECK (length(client_first_name) <= 40),
  currency TEXT NOT NULL,
  sale_ref TEXT NOT NULL DEFAULT '',
  late_cancel INTEGER NOT NULL DEFAULT 0 CHECK (late_cancel IN (0, 1)),
  supersedes_id TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  CHECK (end_utc > start_utc)
);

CREATE INDEX appointments_resource_start ON appointments (resource_id, start_utc);

CREATE INDEX appointments_supersedes ON appointments (supersedes_id);

CREATE TABLE appointment_lines (
  appointment_id TEXT NOT NULL,
  line_no INTEGER NOT NULL,
  sku TEXT NOT NULL,
  name_snapshot TEXT NOT NULL,
  qty INTEGER NOT NULL CHECK (qty > 0),
  amount_minor INTEGER NOT NULL,
  duration_min INTEGER NOT NULL CHECK (duration_min > 0),
  buffer_min INTEGER NOT NULL CHECK (buffer_min >= 0),
  PRIMARY KEY (appointment_id, line_no)
);

CREATE TABLE appointment_events (
  id TEXT PRIMARY KEY,
  appointment_id TEXT NOT NULL,
  at INTEGER NOT NULL,
  actor_user_id TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL CHECK (kind IN ('state_change', 'conflict_override', 'purge')),
  from_state TEXT NOT NULL DEFAULT '',
  to_state TEXT NOT NULL DEFAULT '',
  reason TEXT NOT NULL DEFAULT '',
  conflicting_ids TEXT NOT NULL DEFAULT '[]',
  CHECK (kind <> 'conflict_override' OR actor_user_id <> '')
);

CREATE INDEX appointment_events_appointment ON appointment_events (appointment_id, at);

CREATE TRIGGER appointments_booking_data_immutable
BEFORE UPDATE ON appointments
WHEN NEW.id IS NOT OLD.id
  OR NEW.origin_till_id IS NOT OLD.origin_till_id
  OR NEW.source IS NOT OLD.source
  OR NEW.resource_id IS NOT OLD.resource_id
  OR NEW.start_utc IS NOT OLD.start_utc
  OR NEW.end_utc IS NOT OLD.end_utc
  OR NEW.buffer_min IS NOT OLD.buffer_min
  OR NEW.walk_in IS NOT OLD.walk_in
  OR NEW.currency IS NOT OLD.currency
  OR NEW.supersedes_id IS NOT OLD.supersedes_id
  OR NEW.created_at IS NOT OLD.created_at
  OR (NEW.client_first_name <> OLD.client_first_name AND NEW.client_first_name <> '')
BEGIN
  SELECT RAISE(ABORT, 'appointment booking data is immutable');
END;

CREATE TRIGGER appointments_never_deleted
BEFORE DELETE ON appointments
BEGIN
  SELECT RAISE(ABORT, 'appointments are never deleted');
END;

CREATE TRIGGER appointment_lines_immutable
BEFORE UPDATE ON appointment_lines
BEGIN
  SELECT RAISE(ABORT, 'appointment lines are immutable');
END;

CREATE TRIGGER appointment_lines_never_deleted
BEFORE DELETE ON appointment_lines
BEGIN
  SELECT RAISE(ABORT, 'appointment lines are never deleted');
END;

CREATE TRIGGER appointment_events_append_only_update
BEFORE UPDATE ON appointment_events
BEGIN
  SELECT RAISE(ABORT, 'appointment events are append-only');
END;

CREATE TRIGGER appointment_events_append_only_delete
BEFORE DELETE ON appointment_events
BEGIN
  SELECT RAISE(ABORT, 'appointment events are append-only');
END;
