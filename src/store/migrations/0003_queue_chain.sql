CREATE TABLE walkin_queue (
  id TEXT PRIMARY KEY,
  seq INTEGER NOT NULL,
  arrived_utc INTEGER NOT NULL,
  first_name TEXT NOT NULL DEFAULT '' CHECK (length(first_name) <= 40),
  service_sku TEXT NOT NULL,
  duration_min INTEGER NOT NULL CHECK (duration_min > 0),
  buffer_min INTEGER NOT NULL CHECK (buffer_min >= 0),
  resource_ids TEXT NOT NULL DEFAULT '[]'
);

CREATE INDEX walkin_queue_arrived ON walkin_queue (arrived_utc);

CREATE UNIQUE INDEX appointments_one_superseder ON appointments (supersedes_id) WHERE supersedes_id <> '';

CREATE TABLE completion_outbox (
  appointment_id TEXT PRIMARY KEY,
  sale_ref TEXT NOT NULL,
  payload TEXT NOT NULL,
  published INTEGER NOT NULL DEFAULT 0 CHECK (published IN (0, 1))
);
