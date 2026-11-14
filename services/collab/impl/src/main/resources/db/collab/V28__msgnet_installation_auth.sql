ALTER TABLE msgnet_control_target ADD COLUMN identity_revision numeric(20,0) NOT NULL DEFAULT 0;
CREATE TABLE msgnet_installation_nonce (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  realm text NOT NULL,
  identity_id text NOT NULL,
  nonce text NOT NULL,
  UNIQUE (tenant_id, realm, identity_id, nonce)
);
SELECT auriga_enable_tenant_rls('msgnet_installation_nonce');

-- Keep completed old-identity isolation after an independent pin replacement.
CREATE TABLE msgnet_isolation_enforcement (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  realm text NOT NULL,
  identity_id text NOT NULL,
  control_digest text NOT NULL,
  isolation_record text NOT NULL,
  UNIQUE (tenant_id, realm, identity_id, control_digest)
);
SELECT auriga_enable_tenant_rls('msgnet_isolation_enforcement');
