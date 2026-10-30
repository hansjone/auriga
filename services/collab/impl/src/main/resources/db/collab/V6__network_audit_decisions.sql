ALTER TABLE network_mail_item
  ADD COLUMN dispatch_audited boolean NOT NULL DEFAULT false,
  ADD COLUMN authorization_audit_state text;
-- Preserve complete error semantics for non-private idempotent results.
ALTER TABLE network_contact_submission
  ADD COLUMN error_http_status integer CHECK (error_http_status BETWEEN 400 AND 599),
  ADD COLUMN error_grpc_status text;
