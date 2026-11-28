CREATE TABLE resources (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL CHECK (kind IN ('staff', 'chair')),
  display_name TEXT NOT NULL CHECK (length(display_name) BETWEEN 1 AND 80),
  colour TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL CHECK (status IN ('active', 'inactive')),
  logbook_staff_ref TEXT NOT NULL DEFAULT ''
);

CREATE TABLE working_hours (
  resource_id TEXT NOT NULL,
  weekday INTEGER NOT NULL CHECK (weekday BETWEEN 0 AND 6),
  start_min INTEGER NOT NULL,
  end_min INTEGER NOT NULL,
  PRIMARY KEY (resource_id, weekday, start_min),
  CHECK (start_min >= 0 AND start_min < end_min AND end_min <= 1440)
);

CREATE TABLE time_off (
  id TEXT PRIMARY KEY,
  resource_id TEXT NOT NULL,
  from_utc INTEGER NOT NULL,
  to_utc INTEGER NOT NULL,
  reason TEXT NOT NULL CHECK (reason IN ('holiday', 'away', 'other')),
  CHECK (from_utc < to_utc)
);

CREATE INDEX time_off_resource ON time_off (resource_id);

CREATE TABLE services (
  sku TEXT PRIMARY KEY,
  duration_min INTEGER NOT NULL CHECK (duration_min > 0),
  buffer_min INTEGER NOT NULL DEFAULT 0 CHECK (buffer_min >= 0),
  all_resources INTEGER NOT NULL CHECK (all_resources IN (0, 1)),
  active INTEGER NOT NULL CHECK (active IN (0, 1))
);

CREATE TABLE service_resources (
  sku TEXT NOT NULL,
  resource_id TEXT NOT NULL,
  PRIMARY KEY (sku, resource_id)
);

CREATE TABLE settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
