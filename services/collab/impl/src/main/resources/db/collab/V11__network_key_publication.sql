-- Root-approved public material only. The application role must not own this table.
-- Updating a release and making its latest complete revocation state pending is atomic.
CREATE TABLE network_key_publication (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  realm text NOT NULL CHECK (realm ~ '^[1-9][0-9]{0,19}$'),
  keyset_version numeric(20, 0) NOT NULL CHECK (
    keyset_version > 0 AND keyset_version <= 18446744073709551615),
  signed_keyset text NOT NULL CHECK (octet_length(signed_keyset) <= 65536),
  announcement text NOT NULL CHECK (octet_length(announcement) <= 65536),
  first_published jsonb NOT NULL,
  pending_push boolean NOT NULL DEFAULT true,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, realm)
);
SELECT auriga_enable_tenant_rls('network_key_publication');
