-- Existing nodes may have incomplete source history. Only new nodes default to no gap.
ALTER TABLE network_node ADD COLUMN source_operation_recovery_gap boolean NOT NULL DEFAULT true;
ALTER TABLE network_node ALTER COLUMN source_operation_recovery_gap SET DEFAULT false;

CREATE TABLE network_source_lineage (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  realm text NOT NULL,
  node_id text NOT NULL,
  registration_id uuid NOT NULL,
  native_binding_json text NOT NULL,
  stopped boolean NOT NULL DEFAULT false,
  stop_version bigint NOT NULL DEFAULT 0,
  permission_version bigint NOT NULL DEFAULT 1,
  head_binding text,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, realm, node_id, registration_id),
  FOREIGN KEY (tenant_id, registration_id) REFERENCES network_local_registration(tenant_id, id)
);

CREATE TABLE network_source_operation (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  method text NOT NULL CHECK (method IN ('Register', 'Revoke')),
  operation_id text NOT NULL,
  realm text NOT NULL,
  node_id text NOT NULL,
  business_json text NOT NULL,
  request_digest text NOT NULL,
  original_frame bytea NOT NULL,
  original_connection bytea NOT NULL,
  iam_request bytea,
  iam_request_json text,
  iam_response bytea,
  lineage_id uuid,
  witness_json text,
  epoch bigint NOT NULL,
  boot_id text NOT NULL,
  stop_version bigint,
  permission_version bigint,
  status text NOT NULL CHECK (status IN ('pending','registered','not_accepted','not_committed','revoked')),
  result_json text,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, method, operation_id),
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, lineage_id) REFERENCES network_source_lineage(tenant_id, id)
);

CREATE TABLE network_source_outbox (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  operation_id uuid NOT NULL,
  completed boolean NOT NULL DEFAULT false,
  attempts integer NOT NULL DEFAULT 0,
  next_attempt_at timestamptz NOT NULL DEFAULT now(),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, operation_id),
  FOREIGN KEY (tenant_id, operation_id) REFERENCES network_source_operation(tenant_id, id)
);

CREATE TABLE network_source_binding (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  source_binding text NOT NULL,
  lineage_id uuid NOT NULL,
  operation_id uuid NOT NULL,
  creation_receipt bytea NOT NULL,
  observation bytea,
  observation_request bytea,
  observation_connection bytea,
  observation_witness text,
  observed_at timestamptz,
  valid_until timestamptz,
  epoch bigint,
  boot_id text,
  stop_version bigint,
  permission_version bigint,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, source_binding),
  FOREIGN KEY (tenant_id, lineage_id) REFERENCES network_source_lineage(tenant_id, id),
  FOREIGN KEY (tenant_id, operation_id) REFERENCES network_source_operation(tenant_id, id)
);

CREATE TABLE network_source_witness (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  realm text NOT NULL,
  node_id text NOT NULL,
  witness_id text NOT NULL,
  witness_digest text NOT NULL,
  witness_json text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, realm, node_id, witness_id)
);

-- Persisted leases bound concurrent requests across processes, with an independent revoke slot.
CREATE TABLE network_source_slot (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  realm text NOT NULL,
  node_id text NOT NULL,
  slot text NOT NULL CHECK (slot IN ('regular','revoke')),
  holder uuid NOT NULL,
  valid_until timestamptz NOT NULL,
  UNIQUE (tenant_id, realm, node_id, slot)
);

SELECT auriga_enable_tenant_rls('network_source_lineage');
SELECT auriga_enable_tenant_rls('network_source_operation');
SELECT auriga_enable_tenant_rls('network_source_outbox');
SELECT auriga_enable_tenant_rls('network_source_binding');
SELECT auriga_enable_tenant_rls('network_source_witness');
SELECT auriga_enable_tenant_rls('network_source_slot');

-- Platform-owned routing index contains only tenant identifiers, never source facts or requests.
-- A transaction-local trigger inserts this wakeup atomically with each durable outbox row.
CREATE TABLE network_source_worker_tenant (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  business_tenant uuid NOT NULL UNIQUE,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (tenant_id='00000000-0000-0000-0000-000000000000'::uuid)
);
SELECT auriga_enable_tenant_rls('network_source_worker_tenant');

CREATE FUNCTION network_source_wake_worker() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER
SET search_path=collab,pg_temp
SET "auriga.tenant_id"='00000000-0000-0000-0000-000000000000'
AS $$
BEGIN
  INSERT INTO network_source_worker_tenant(id,tenant_id,business_tenant)
  VALUES (NEW.id,'00000000-0000-0000-0000-000000000000',NEW.tenant_id)
  ON CONFLICT (business_tenant) DO NOTHING;
  RETURN NEW;
END;
$$;
REVOKE ALL ON FUNCTION network_source_wake_worker() FROM PUBLIC;
CREATE TRIGGER network_source_wakeup AFTER INSERT ON network_source_outbox
FOR EACH ROW EXECUTE FUNCTION network_source_wake_worker();
