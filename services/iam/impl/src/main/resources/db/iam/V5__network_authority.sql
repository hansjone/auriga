-- Infrastructure allocator: nextval is deliberately nontransactional (aborted allocations burn).
-- Shift signed bigint into 1..uint64_max. Never cycle or reset on node/account deletion.
CREATE SEQUENCE iam.network_realm_number AS bigint
  MINVALUE -9223372036854775808 MAXVALUE 9223372036854775806
  START WITH -9223372036854775808 NO CYCLE CACHE 1;

CREATE TABLE iam.network_realm (
  tenant_id uuid PRIMARY KEY,
  realm numeric(20,0) NOT NULL UNIQUE CHECK (realm BETWEEN 1 AND 18446744073709551615),
  next_node numeric(20,0) NOT NULL DEFAULT 1
    CHECK (next_node BETWEEN 1 AND 18446744073709551616)
);
SELECT iam.auriga_enable_tenant_rls('iam.network_realm');

-- Node reservations update next_node in an independent committed transaction before exposure.
-- Rows are tombstones, not expirable sessions. No ON DELETE CASCADE, no reuse of device or node.
CREATE TABLE iam.network_node (
  tenant_id uuid NOT NULL REFERENCES iam.network_realm(tenant_id),
  node numeric(20,0) NOT NULL CHECK (node BETWEEN 1 AND 18446744073709551615),
  account_id uuid NOT NULL,
  device_id uuid NOT NULL,
  revoked boolean NOT NULL DEFAULT false,
  PRIMARY KEY (tenant_id,node),
  UNIQUE (tenant_id,device_id),
  FOREIGN KEY (tenant_id,account_id) REFERENCES iam.account(tenant_id,id),
  FOREIGN KEY (tenant_id,device_id) REFERENCES iam.device(tenant_id,id)
);
SELECT iam.auriga_enable_tenant_rls('iam.network_node');

-- Native IamStore records: only SHA-256 ticket digests, never device tokens or raw tickets.
CREATE TABLE iam.network_ticket (LIKE iam.rate_limit INCLUDING ALL);
SELECT iam.auriga_enable_tenant_rls('iam.network_ticket');
CREATE INDEX network_ticket_node_expiry ON iam.network_ticket (tenant_id,(data->>'node'),expires_at);
CREATE TABLE iam.network_policy (LIKE iam.rate_limit INCLUDING ALL);
SELECT iam.auriga_enable_tenant_rls('iam.network_policy');
CREATE TABLE iam.network_grant (LIKE iam.rate_limit INCLUDING ALL);
SELECT iam.auriga_enable_tenant_rls('iam.network_grant');
CREATE TABLE iam.network_config (LIKE iam.rate_limit INCLUDING ALL);
SELECT iam.auriga_enable_tenant_rls('iam.network_config');
