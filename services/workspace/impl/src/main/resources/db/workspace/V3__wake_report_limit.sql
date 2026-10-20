-- Row-locked, persistent schedule report limit shared by all workspace replicas.
ALTER TABLE instance ADD COLUMN wake_reported_at timestamptz;
-- One trailing report per instance, including an explicit cancellation (NULL).
ALTER TABLE instance ADD COLUMN wake_report_pending boolean NOT NULL DEFAULT false;
ALTER TABLE instance ADD COLUMN pending_wake_at timestamptz;
