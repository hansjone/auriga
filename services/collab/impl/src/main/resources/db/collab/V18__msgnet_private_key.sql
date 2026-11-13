-- KEKs remain on the protected collab mount. Only authenticated ciphertext is persisted.
CREATE TABLE msgnet_private_key (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  realm text NOT NULL CHECK (realm ~ '^[1-9][0-9]{0,19}$'),
  kid text NOT NULL CHECK (length(kid) BETWEEN 1 AND 128),
  aad text NOT NULL,
  public_key_spki text NOT NULL,
  encrypted_key text NOT NULL,
  kek_id text NOT NULL CHECK (length(kek_id) BETWEEN 1 AND 128),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  UNIQUE (tenant_id, realm, kid),
  UNIQUE (tenant_id, realm, public_key_spki)
);
SELECT auriga_enable_tenant_rls('msgnet_private_key');
