-- Hashes and public bindings only: private projection bytes remain in the KEK store.
ALTER TABLE msgnet_projection_nonce ADD COLUMN authorized_snapshot text;
ALTER TABLE msgnet_projection_nonce ADD COLUMN local_selector text;
ALTER TABLE msgnet_projection_nonce ADD COLUMN receipts jsonb NOT NULL DEFAULT '{}'::jsonb;
CREATE TABLE msgnet_projection_head (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  realm text NOT NULL,
  installation_id text NOT NULL,
  component text NOT NULL,
  selector text NOT NULL,
  UNIQUE (tenant_id, realm, installation_id, component)
);
SELECT auriga_enable_tenant_rls('msgnet_projection_head');
