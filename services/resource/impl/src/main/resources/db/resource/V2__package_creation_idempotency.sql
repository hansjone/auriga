-- Scoped operation result, committed atomically with the new package and its ACL.
CREATE TABLE resource_package_creation (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  actor_id uuid NOT NULL,
  operation text NOT NULL CHECK (operation = 'POST /api/resource/v1/packages'),
  request_key text NOT NULL CHECK (char_length(request_key) BETWEEN 1 AND 128),
  fingerprint text NOT NULL CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
  response jsonb NOT NULL CHECK (jsonb_typeof(response) = 'object'),
  expires_at timestamptz NOT NULL,
  UNIQUE (tenant_id,actor_id,operation,request_key)
);
SELECT auriga_enable_tenant_rls('resource_package_creation');
CREATE INDEX resource_package_creation_expiry ON resource_package_creation(tenant_id,expires_at);
