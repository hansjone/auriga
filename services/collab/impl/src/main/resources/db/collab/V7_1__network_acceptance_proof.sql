-- Stable acceptance evidence is unique per recipient/phase, independent of operation transport.
CREATE UNIQUE INDEX network_recipient_proof_binding ON network_recipient(tenant_id,id,delivery_id);
CREATE TABLE network_acceptance_proof (
  tenant_id uuid NOT NULL,
  recipient_id uuid NOT NULL,
  delivery_id uuid NOT NULL,
  accepted_by text NOT NULL CHECK (accepted_by IN ('system','session')),
  proof_json text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id,recipient_id,accepted_by),
  FOREIGN KEY (tenant_id,recipient_id,delivery_id) REFERENCES network_recipient(tenant_id,id,delivery_id)
);
SELECT auriga_enable_tenant_rls('network_acceptance_proof');
