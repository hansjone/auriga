-- Samples are usable only on the exact currently held NodeControl session.
-- Generation/time high water survives reconnects; a new holder needs a new sample.
CREATE TABLE shared_dsh_pool_memory (
 tenant_id uuid NOT NULL, pool_id uuid NOT NULL, node_id uuid NOT NULL,
 holder uuid NOT NULL, generation bigint NOT NULL CHECK(generation BETWEEN 1 AND 9007199254740991),
 observed_at bigint NOT NULL CHECK(observed_at BETWEEN 0 AND 9007199254740991),
 valid_until bigint NOT NULL CHECK(valid_until>observed_at AND valid_until-observed_at<=30),
 used_bytes bigint NOT NULL CHECK(used_bytes BETWEEN 0 AND 9007199254740991),
 limit_bytes bigint NOT NULL CHECK(limit_bytes BETWEEN 1 AND 9007199254740991),
 PRIMARY KEY(tenant_id,pool_id),
 FOREIGN KEY(tenant_id,pool_id) REFERENCES shared_dsh_pool(tenant_id,id),
 FOREIGN KEY(tenant_id,node_id) REFERENCES node(tenant_id,id)
);
SELECT auriga_enable_tenant_rls('shared_dsh_pool_memory');

-- Keep the full immutable binding on an active reservation; old unbound rows
-- stay blocking until their original cleanup, never adopted by a new task.
ALTER TABLE shared_dsh_active ADD COLUMN binding bytea;
ALTER TABLE shared_dsh_active ADD COLUMN node_id uuid;

-- Exact-binding durable end barriers also cover a Release arriving before Admit.
-- No live-session FK: revocation must not prevent safe cancellation.
CREATE TABLE shared_dsh_task_end (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, task_id uuid NOT NULL,
 binding_digest text NOT NULL CHECK(binding_digest ~ '^[a-f0-9]{64}$'),
 binding bytea NOT NULL, node_id uuid NOT NULL, ended_at timestamptz NOT NULL,
 UNIQUE(tenant_id,task_id,binding_digest),
 FOREIGN KEY(tenant_id,node_id) REFERENCES node(tenant_id,id)
);
SELECT auriga_enable_tenant_rls('shared_dsh_task_end');
