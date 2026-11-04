-- Tenant-local bounded recovery observations; no state/accounting changes.
CREATE INDEX budget_recovery_page ON ai.budget_request(tenant_id,request_id,subject)
 WHERE state IN ('reserved','dispatching') OR (state='unknown' AND slot_held);
