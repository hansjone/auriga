-- Successful CONNECT grants bind a server generation/CID/user key to a private ingress.
-- No raw ticket or access token is persisted. Expiry permits bounded retention cleanup.
CREATE TABLE iam.network_ingress (LIKE iam.rate_limit INCLUDING ALL);
SELECT iam.auriga_enable_tenant_rls('iam.network_ingress');
CREATE UNIQUE INDEX network_ingress_connection
  ON iam.network_ingress (tenant_id,(data->>'serverId'),(data->>'clientId'));
CREATE INDEX network_ingress_node_expiry
  ON iam.network_ingress (tenant_id,(data->>'realm'),(data->>'node'),expires_at);
