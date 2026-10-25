-- Target outcomes never shorten source reservations or distinct-target history.
CREATE TABLE network_contact_budget (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  realm text NOT NULL,
  scope text NOT NULL CHECK (scope IN ('user','node','local')),
  subject text NOT NULL,
  tokens double precision NOT NULL CHECK (tokens >= 0),
  refilled_at timestamptz NOT NULL,
  frozen_until timestamptz,
  cooldown_until timestamptz,
  UNIQUE (tenant_id, realm, scope, subject)
);
CREATE TABLE network_contact_submission (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  realm text NOT NULL,
  source_node text NOT NULL,
  source_local text NOT NULL,
  source_user text NOT NULL,
  target_node text NOT NULL,
  producer_epoch text NOT NULL,
  idempotency_key text NOT NULL,
  fingerprint text NOT NULL,
  created_at timestamptz NOT NULL,
  visible_until timestamptz NOT NULL,
  result_json text,
  error_code text,
  message_id uuid,
  UNIQUE (tenant_id, realm, source_node, source_local, producer_epoch, idempotency_key),
  FOREIGN KEY (tenant_id, message_id) REFERENCES network_message(tenant_id, id),
  CHECK (visible_until <= created_at + interval '24 hours')
);
CREATE INDEX network_contact_source_local ON network_contact_submission
  (tenant_id, realm, source_node, source_local, created_at);
CREATE INDEX network_contact_source_user ON network_contact_submission
  (tenant_id, realm, source_user, created_at);
SELECT auriga_enable_tenant_rls('network_contact_budget');
SELECT auriga_enable_tenant_rls('network_contact_submission');
