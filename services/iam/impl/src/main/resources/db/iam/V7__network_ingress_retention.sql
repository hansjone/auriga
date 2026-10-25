-- Infrastructure tenant directory, following session_projection_tenant. It
-- contains no connection payload; every cleanup enters a business tenant RLS scope.
CREATE TABLE iam.network_ingress_tenant (tenant_id uuid PRIMARY KEY);
-- Flyway runs this PostgreSQL migration transactionally. ALTER keeps an
-- ACCESS EXCLUSIVE lock until FORCE is restored and the transaction commits;
-- the runtime never observes a relaxed policy. Only tenant IDs are copied.
ALTER TABLE iam.network_ingress NO FORCE ROW LEVEL SECURITY;
INSERT INTO iam.network_ingress_tenant SELECT DISTINCT tenant_id FROM iam.network_ingress;
ALTER TABLE iam.network_ingress FORCE ROW LEVEL SECURITY;
CREATE INDEX network_ingress_expiry ON iam.network_ingress (tenant_id,expires_at,id);
