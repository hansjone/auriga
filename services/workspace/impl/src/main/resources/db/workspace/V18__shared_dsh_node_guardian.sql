-- Immutable receipts from the fenced owning NodeControl session.
CREATE TABLE shared_dsh_node_guardian (
 tenant_id uuid NOT NULL, operation_id uuid NOT NULL, pool_id uuid NOT NULL,
 payload bytea NOT NULL, PRIMARY KEY(tenant_id,operation_id)
);
SELECT auriga_enable_tenant_rls('shared_dsh_node_guardian');
