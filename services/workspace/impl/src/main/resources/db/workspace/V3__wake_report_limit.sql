-- Row-locked, persistent schedule report limit shared by all workspace replicas.
ALTER TABLE instance ADD COLUMN wake_reported_at timestamptz;
-- One trailing report per instance, including an explicit cancellation (NULL).
ALTER TABLE instance ADD COLUMN wake_report_pending boolean NOT NULL DEFAULT false;
ALTER TABLE instance ADD COLUMN pending_wake_at timestamptz;
-- Finished wake jobs are pruned by creation order (UUIDv7 id) in the decision loop.
CREATE INDEX workspace_job_done_wake ON workspace_job(tenant_id,id) WHERE done AND kind='wake';
