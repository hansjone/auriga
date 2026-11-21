-- A prepared pool remains on its original authenticated node across reconnects.
-- Moving it requires a separate, explicit stop/recovery protocol.
CREATE TABLE shared_dsh_pool_node (
 tenant_id uuid NOT NULL, pool_id uuid NOT NULL, node_id uuid NOT NULL,
 PRIMARY KEY(tenant_id,pool_id),
 FOREIGN KEY(tenant_id,pool_id) REFERENCES shared_dsh_pool(tenant_id,id),
 FOREIGN KEY(tenant_id,node_id) REFERENCES node(tenant_id,id)
);
CREATE INDEX shared_dsh_pool_node_owner ON shared_dsh_pool_node(tenant_id,node_id);
SELECT auriga_enable_tenant_rls('shared_dsh_pool_node');
