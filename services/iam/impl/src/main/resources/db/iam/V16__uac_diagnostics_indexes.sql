CREATE INDEX uac_diagnostic_event_range ON iam.uac_diagnostics
  (tenant_id,(data->>'providerId'),(data->'event'->>'timestamp')) WHERE data->>'kind'='event';
CREATE INDEX uac_diagnostic_audit_pending ON iam.uac_diagnostics
  (tenant_id,id) WHERE data->>'auditPending'='true';
CREATE INDEX uac_diagnostic_expiry ON iam.uac_diagnostics (tenant_id,expires_at) WHERE expires_at IS NOT NULL;
