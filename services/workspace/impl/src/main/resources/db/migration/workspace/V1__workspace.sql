CREATE TABLE workspace_registry (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, resource_tenant uuid NOT NULL,
  kind text NOT NULL CHECK (kind IN ('tenant','node','token','instance'))
);
SELECT auriga_enable_tenant_rls('workspace_registry');
-- Platform routing metadata, accessed only in explicit PLATFORM_TENANT transactions.
-- It carries no credentials; every lookup is followed by tenant-scoped resource authorization.

CREATE TABLE node (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, name text NOT NULL, hostname text NOT NULL,
  os text NOT NULL, arch text NOT NULL, agent_version text NOT NULL,
  sealing_key bytea NOT NULL CHECK (octet_length(sealing_key)=32),
  certificate_serial text NOT NULL, certificate_expires_at timestamptz NOT NULL,
  revoked boolean NOT NULL DEFAULT false, schedulable boolean NOT NULL DEFAULT true,
  draining boolean NOT NULL DEFAULT false, hello bytea, usage bytea,
  labels jsonb NOT NULL DEFAULT '{}', last_seen_at timestamptz,
  joined_at timestamptz NOT NULL, UNIQUE(tenant_id,id)
);
SELECT auriga_enable_tenant_rls('node');

CREATE TABLE node_session (
  id uuid PRIMARY KEY,tenant_id uuid NOT NULL,node_id uuid NOT NULL,holder uuid NOT NULL,
  expires_at timestamptz NOT NULL,UNIQUE(tenant_id,node_id),
  FOREIGN KEY(tenant_id,node_id) REFERENCES node(tenant_id,id)
);
SELECT auriga_enable_tenant_rls('node_session');

CREATE TABLE join_token (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, name text,
  secret_hash bytea NOT NULL, expires_at timestamptz NOT NULL,
  created_by uuid NOT NULL, created_at timestamptz NOT NULL, used_at timestamptz,
  UNIQUE(tenant_id,id)
);
SELECT auriga_enable_tenant_rls('join_token');

CREATE TABLE workspace_lock (id uuid PRIMARY KEY, tenant_id uuid NOT NULL UNIQUE);
SELECT auriga_enable_tenant_rls('workspace_lock');

CREATE TABLE instance (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, owner_id uuid NOT NULL,
  name text NOT NULL, node_id uuid, requested_node_id uuid, spec bytea NOT NULL,
  generation bigint NOT NULL CHECK(generation>0), observed_generation bigint NOT NULL DEFAULT 0,
  phase text NOT NULL DEFAULT 'scheduling', ready boolean NOT NULL DEFAULT false,
  status bytea, always_on boolean NOT NULL DEFAULT false,
  migration_pending boolean NOT NULL DEFAULT false, deleted boolean NOT NULL DEFAULT false,
  purge_data boolean NOT NULL DEFAULT false, pinned_version text,
  resources_override bytea, restart_pending boolean NOT NULL DEFAULT false,
  deletion_actor uuid, deletion_event_sent boolean NOT NULL DEFAULT false,
  token_hash bytea NOT NULL, token_encrypted bytea NOT NULL,
  last_activity_at timestamptz NOT NULL, plugin_seen_at timestamptz,
  cpu_seen_at timestamptz, plugin_active boolean NOT NULL DEFAULT true,
  cpu_active boolean NOT NULL DEFAULT true, next_wake_at timestamptz,
  start_reason text NOT NULL DEFAULT 'user', stop_reason text NOT NULL DEFAULT 'user',
  started_at timestamptz, created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
  UNIQUE(tenant_id,id), FOREIGN KEY(tenant_id,node_id) REFERENCES node(tenant_id,id)
);
CREATE UNIQUE INDEX instance_one_per_owner ON instance(tenant_id,owner_id) WHERE NOT deleted;
SELECT auriga_enable_tenant_rls('instance');

CREATE TABLE instance_spec (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, instance_id uuid NOT NULL,
  generation bigint NOT NULL, spec bytea NOT NULL, created_at timestamptz NOT NULL,
  UNIQUE(tenant_id,instance_id,generation),
  FOREIGN KEY(tenant_id,instance_id) REFERENCES instance(tenant_id,id)
);
SELECT auriga_enable_tenant_rls('instance_spec');

CREATE TABLE workspace_job (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, instance_id uuid NOT NULL,
  kind text NOT NULL CHECK(kind IN ('wake','restart','delete')), due_at timestamptz NOT NULL,
  command bytea, done boolean NOT NULL DEFAULT false,
  attempts integer NOT NULL DEFAULT 0,failed boolean NOT NULL DEFAULT false,result bytea,
  last_error text,dispatched_session uuid,sent_at timestamptz,generation bigint NOT NULL DEFAULT 0,
  FOREIGN KEY(tenant_id,instance_id) REFERENCES instance(tenant_id,id)
);
CREATE INDEX workspace_job_due ON workspace_job(tenant_id,due_at) WHERE NOT done;
SELECT auriga_enable_tenant_rls('workspace_job');

CREATE TABLE workspace_setting (id uuid PRIMARY KEY,tenant_id uuid NOT NULL UNIQUE,default_version text NOT NULL);
SELECT auriga_enable_tenant_rls('workspace_setting');

CREATE TABLE workspace_request (
  id uuid PRIMARY KEY,tenant_id uuid NOT NULL,actor_id uuid NOT NULL,
  request_key bytea NOT NULL,request_hash bytea NOT NULL,response bytea NOT NULL,expires_at timestamptz NOT NULL,
  UNIQUE(tenant_id,actor_id,request_key)
);
SELECT auriga_enable_tenant_rls('workspace_request');
