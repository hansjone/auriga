-- Long-lived identity witnesses, never subject to mailbox/dedup retention.
-- READY, registration and assignment serialize on the existing network_node row.
ALTER TABLE network_node
  ADD COLUMN local_high_water numeric(20,0) NOT NULL DEFAULT 0
    CHECK (local_high_water BETWEEN 0 AND 18446744073709551615),
  ADD COLUMN registration_revision numeric(20,0) NOT NULL DEFAULT 0
    CHECK (registration_revision BETWEEN 0 AND 18446744073709551615),
  ADD COLUMN local_identity_status text NOT NULL DEFAULT 'recovery_conflict'
    CHECK (local_identity_status IN
      ('healthy', 'node_identity_conflict', 'recovery_conflict', 'identity_rollback')),
  ADD COLUMN local_identity_reason text DEFAULT 'network.recovery_conflict',
  ADD COLUMN local_identity_observed_at timestamptz DEFAULT now(),
  ADD COLUMN local_boot_epoch bigint,
  ADD COLUMN local_boot_id text;

-- ADD COLUMN defaults cover old rows even under FORCE RLS without bypassing tenant isolation.
-- Only subsequently created nodes start healthy; never infer old history from live sessions.
ALTER TABLE network_node
  ALTER COLUMN local_identity_status SET DEFAULT 'healthy',
  ALTER COLUMN local_identity_reason DROP DEFAULT,
  ALTER COLUMN local_identity_observed_at DROP DEFAULT;

CREATE TABLE network_local_registration (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  realm text NOT NULL,
  node_id text NOT NULL,
  local_id numeric(20,0) NOT NULL CHECK (local_id BETWEEN 1 AND 18446744073709551615),
  allocation_op_id text NOT NULL CHECK (allocation_op_id ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$'),
  allocation_fingerprint text NOT NULL CHECK (allocation_fingerprint ~ '^sha256:[0-9a-f]{64}$'),
  registration_revision numeric(20,0) NOT NULL
    CHECK (registration_revision BETWEEN 1 AND 18446744073709551615),
  previous_high_water numeric(20,0) NOT NULL CHECK (previous_high_water >= 0 AND previous_high_water < local_id),
  granted_epoch bigint NOT NULL CHECK (granted_epoch > 0),
  granted_boot_id text NOT NULL,
  status text NOT NULL DEFAULT 'registered' CHECK (status IN ('registered', 'retired')),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, realm, node_id, local_id),
  UNIQUE (tenant_id, realm, node_id, allocation_op_id),
  UNIQUE (tenant_id, realm, node_id, registration_revision),
  UNIQUE (tenant_id, realm, node_id, local_id, id),
  FOREIGN KEY (tenant_id, realm, node_id) REFERENCES network_node (tenant_id, realm, node_id)
);

CREATE TABLE network_contact_assignment (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  contact_id uuid NOT NULL,
  realm text NOT NULL,
  node_id text NOT NULL,
  local_id numeric(20,0) NOT NULL,
  registration_id uuid NOT NULL,
  creation_attempt_id text NOT NULL CHECK (creation_attempt_id ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$'),
  decision_version bigint NOT NULL CHECK (decision_version > 0),
  granted_epoch bigint NOT NULL CHECK (granted_epoch > 0),
  granted_boot_id text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, contact_id),
  UNIQUE (tenant_id, registration_id),
  FOREIGN KEY (tenant_id, contact_id) REFERENCES network_contact (tenant_id, id),
  FOREIGN KEY (tenant_id, realm, node_id, local_id, registration_id)
    REFERENCES network_local_registration (tenant_id, realm, node_id, local_id, id)
);

SELECT auriga_enable_tenant_rls('network_local_registration');
SELECT auriga_enable_tenant_rls('network_contact_assignment');
