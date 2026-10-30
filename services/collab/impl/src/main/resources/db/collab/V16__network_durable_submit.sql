-- Issued with the device's first mailbox association; stable across READY/reconnect.
ALTER TABLE network_node ADD COLUMN producer_epoch bigint GENERATED ALWAYS AS IDENTITY;
ALTER TABLE network_local_registration
  ADD COLUMN receive_enabled boolean NOT NULL DEFAULT false,
  ADD COLUMN receive_version bigint NOT NULL DEFAULT 1 CHECK (receive_version > 0);

CREATE TABLE network_submit (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  source_binding text NOT NULL,
  producer_epoch text NOT NULL,
  producer_key text NOT NULL,
  request_json text NOT NULL,
  message_id uuid NOT NULL,
  delivery_id uuid NOT NULL,
  recipient_id uuid NOT NULL,
  message_json text NOT NULL,
  source_context_json text NOT NULL,
  source_fence_json text NOT NULL,
  target_fence_json text NOT NULL,
  result_json text,
  created_at timestamptz NOT NULL DEFAULT now(),
  retain_until timestamptz NOT NULL DEFAULT now() + interval '7 days',
  UNIQUE (tenant_id, source_binding, producer_epoch, producer_key),
  FOREIGN KEY (tenant_id, source_binding) REFERENCES network_source_binding(tenant_id, source_binding)
);

CREATE TABLE network_fetch_continuation (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  token text NOT NULL,
  realm text NOT NULL,
  node_id text NOT NULL,
  epoch bigint NOT NULL,
  resume_cursor bigint NOT NULL,
  scan_after bigint NOT NULL,
  snapshot_head bigint NOT NULL,
  valid_until timestamptz NOT NULL DEFAULT now() + interval '7 days',
  UNIQUE (tenant_id, token)
);

CREATE TABLE network_attempt (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  recipient_id uuid NOT NULL,
  attempt_id text NOT NULL,
  request_json text NOT NULL,
  result_json text NOT NULL,
  UNIQUE (tenant_id, recipient_id, attempt_id),
  FOREIGN KEY (tenant_id, recipient_id) REFERENCES network_recipient(tenant_id, id)
);
SELECT auriga_enable_tenant_rls('network_submit');
SELECT auriga_enable_tenant_rls('network_fetch_continuation');
SELECT auriga_enable_tenant_rls('network_attempt');
