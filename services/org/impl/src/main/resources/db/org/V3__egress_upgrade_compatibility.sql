-- Preserve the untouched v1 seed; saved legacy rows need a new wire digest/version.
-- Only the migration owner bypasses FORCE RLS, within this transactional migration.
ALTER TABLE org.policy NO FORCE ROW LEVEL SECURITY;
UPDATE org.policy SET version=version+1
WHERE key='egress' AND NOT (
  version=1 AND updated_by IS NULL AND value IN (
    '{}'::jsonb, '{"categories":[],"allowDomains":[],"denyDomains":[]}'::jsonb
  )
);
ALTER TABLE org.policy FORCE ROW LEVEL SECURITY;

ALTER TABLE org.egress_policy_delivery
  ADD COLUMN report_error text CHECK(report_error IN ('invalid_report')),
  ADD COLUMN report_error_at timestamptz;
-- A failed rollback attempt legitimately reports an applied version newer than the candidate.
ALTER TABLE org.egress_policy_report
  DROP CONSTRAINT egress_policy_report_check;
ALTER TABLE org.egress_policy_report
  ADD CONSTRAINT egress_policy_report_applied_version_check
    CHECK(applied_version>=0 AND (result='failed' OR applied_version=version));
