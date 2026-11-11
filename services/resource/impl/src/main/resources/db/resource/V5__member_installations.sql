CREATE TABLE resource_member_installation (
  tenant_id uuid NOT NULL,member_id uuid NOT NULL,client_id uuid NOT NULL,package_id uuid NOT NULL,
  version_id uuid NOT NULL,digest text NOT NULL,release_sequence bigint NOT NULL,
  high_water bigint NOT NULL,level text NOT NULL,desired text NOT NULL,
  state text NOT NULL DEFAULT 'applying',revision bigint NOT NULL DEFAULT 1,
  reported_revision bigint,reported_at timestamptz,error_code text,
  PRIMARY KEY(tenant_id,member_id,client_id,package_id),
  FOREIGN KEY(tenant_id,package_id,version_id) REFERENCES resource_version(tenant_id,package_id,id),
  CHECK (desired IN ('enabled','disabled','removed')),
  CHECK (state IN ('applying','applied','failed')),
  CHECK (revision BETWEEN 1 AND 9007199254740991),
  CHECK (high_water BETWEEN 1 AND 9007199254740991)
);
SELECT auriga_enable_tenant_rls('resource_member_installation');

CREATE TABLE resource_member_ticket (
  tenant_id uuid NOT NULL,member_id uuid NOT NULL,client_id uuid NOT NULL,
  ticket_hash text NOT NULL,package_id uuid NOT NULL,version_id uuid NOT NULL,digest text NOT NULL,
  envelope jsonb NOT NULL,expires_at timestamptz NOT NULL,consumed_at timestamptz,
  PRIMARY KEY(tenant_id,ticket_hash),
  FOREIGN KEY(tenant_id,package_id,version_id) REFERENCES resource_version(tenant_id,package_id,id)
);
SELECT auriga_enable_tenant_rls('resource_member_ticket');
CREATE INDEX resource_member_ticket_expiry ON resource_member_ticket(expires_at);
