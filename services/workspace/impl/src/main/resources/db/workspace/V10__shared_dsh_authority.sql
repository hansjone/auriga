-- Shared DSH control state. No changes to other service schemas.
CREATE TABLE shared_dsh_pool (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, mode text NOT NULL CHECK(mode IN ('shared','dedicated')),
 authorization_digest text NOT NULL, state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','ready','blocked')),
 memory_percent integer NOT NULL DEFAULT 0 CHECK(memory_percent BETWEEN 0 AND 100),
 UNIQUE(tenant_id,id)
);
CREATE TABLE shared_dsh_placement (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, member_id uuid NOT NULL,
 pool_id uuid NOT NULL, mode text NOT NULL CHECK(mode IN ('shared','dedicated')),
 epoch bigint NOT NULL DEFAULT 1 CHECK(epoch BETWEEN 1 AND 9007199254740991),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 auth_generation bigint NOT NULL DEFAULT 1 CHECK(auth_generation BETWEEN 1 AND 9007199254740991),
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','ready','switching','unavailable')),
 UNIQUE(tenant_id,member_id), UNIQUE(tenant_id,id),
 FOREIGN KEY(tenant_id,pool_id) REFERENCES shared_dsh_pool(tenant_id,id)
);
CREATE TABLE shared_dsh_operation (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, placement_id uuid NOT NULL,
 actor_id uuid NOT NULL, idempotency_key text NOT NULL CHECK(length(idempotency_key) BETWEEN 1 AND 128),
 fingerprint text NOT NULL, source_binding bytea NOT NULL, target_pool uuid NOT NULL,
 target_mode text NOT NULL CHECK(target_mode IN ('shared','dedicated')),
 state text NOT NULL DEFAULT 'draining', action text NOT NULL DEFAULT 'cooperative-cancel',
 action_started_at bigint NOT NULL, intent bytea NOT NULL, failure_code text,
 sessions_discarded boolean NOT NULL DEFAULT false, archive_id uuid, archive_expires_at bigint,
 UNIQUE(tenant_id,actor_id,idempotency_key), UNIQUE(tenant_id,id),
 FOREIGN KEY(tenant_id,placement_id) REFERENCES shared_dsh_placement(tenant_id,id),
 FOREIGN KEY(tenant_id,target_pool) REFERENCES shared_dsh_pool(tenant_id,id)
);
CREATE UNIQUE INDEX shared_dsh_one_operation ON shared_dsh_operation(tenant_id,placement_id)
 WHERE state NOT IN ('succeeded','failed');
CREATE TABLE shared_dsh_session (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, placement_id uuid NOT NULL, binding bytea NOT NULL,
 FOREIGN KEY(tenant_id,placement_id) REFERENCES shared_dsh_placement(tenant_id,id)
);
CREATE TABLE shared_dsh_active (
 tenant_id uuid NOT NULL, member_id uuid NOT NULL, pool_id uuid NOT NULL, task_id uuid NOT NULL,
 PRIMARY KEY(tenant_id,member_id), UNIQUE(tenant_id,task_id),
 FOREIGN KEY(tenant_id,pool_id) REFERENCES shared_dsh_pool(tenant_id,id)
);
-- Transactional delivery queue: retry exact immutable payload until receiver confirms it.
CREATE TABLE shared_dsh_outbox (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, kind text NOT NULL, payload bytea NOT NULL,
 created_at bigint NOT NULL, delivered boolean NOT NULL DEFAULT false
);
CREATE TABLE shared_dsh_maintenance (
 tenant_id uuid NOT NULL, member_id uuid NOT NULL, continue_dedicated boolean NOT NULL,
 state text NOT NULL DEFAULT 'export-window' CHECK(state IN ('export-window','accepted','succeeded','blocked')),
 operation_id uuid, PRIMARY KEY(tenant_id,member_id)
);
SELECT auriga_enable_tenant_rls('shared_dsh_pool');
SELECT auriga_enable_tenant_rls('shared_dsh_placement');
SELECT auriga_enable_tenant_rls('shared_dsh_operation');
SELECT auriga_enable_tenant_rls('shared_dsh_session');
SELECT auriga_enable_tenant_rls('shared_dsh_active');
SELECT auriga_enable_tenant_rls('shared_dsh_outbox');
SELECT auriga_enable_tenant_rls('shared_dsh_maintenance');

-- Store claims, never signed bearer tickets. Expired/consumed tickets require a new key.
CREATE TABLE shared_dsh_open (
 tenant_id uuid NOT NULL, member_id uuid NOT NULL, idempotency_key text NOT NULL,
 session_id uuid NOT NULL, jti uuid NOT NULL UNIQUE, binding bytea NOT NULL, created_at bigint NOT NULL,
 PRIMARY KEY(tenant_id,member_id,idempotency_key)
);
SELECT auriga_enable_tenant_rls('shared_dsh_open');

CREATE TABLE shared_dsh_resource_view (
 tenant_id uuid NOT NULL, placement_id uuid NOT NULL, view_id uuid NOT NULL,
 payload bytea NOT NULL, report bytea,
 PRIMARY KEY(tenant_id,placement_id),
 FOREIGN KEY(tenant_id,placement_id) REFERENCES shared_dsh_placement(tenant_id,id)
);
SELECT auriga_enable_tenant_rls('shared_dsh_resource_view');
