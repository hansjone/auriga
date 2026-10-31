CREATE TABLE iam.managed_device (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, lookup_key text NOT NULL,
  data jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz,
  UNIQUE (tenant_id, lookup_key), UNIQUE (tenant_id, id)
);
SELECT iam.auriga_enable_tenant_rls('iam.managed_device');

-- A managed network lineage has no browser identity provider or browser login.
ALTER TABLE iam.session ALTER COLUMN provider_id DROP NOT NULL;
ALTER TABLE iam.session ADD CONSTRAINT session_provider_or_managed CHECK (
  provider_id IS NOT NULL OR COALESCE((
    data->>'kind'='managed_device' AND data->>'audience'='portal' AND data ? 'managedInstance' AND data ? 'managedVersion'
    AND NOT data ? 'accessToken' AND NOT data ? 'csrfHash'
  ),false)
);
