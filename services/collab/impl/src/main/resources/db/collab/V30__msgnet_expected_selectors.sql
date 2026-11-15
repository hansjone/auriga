-- Frozen readers and C-validated material bindings, independent of completion evidence.
CREATE TABLE msgnet_transport_selector (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  realm text NOT NULL,
  bundle_digest text NOT NULL,
  reader_id text NOT NULL,
  identity_id text NOT NULL,
  selector text,
  authorization_snapshot text,
  ambiguous boolean NOT NULL DEFAULT false,
  UNIQUE (tenant_id, realm, bundle_digest, reader_id),
  CHECK ((selector IS NULL) = (authorization_snapshot IS NULL))
);
-- K3 used identity_id as reader_id. Preserve every historical cycle independently of live pins.
-- local_selector is the C-validated binding (not receipt JSON); do not choose among conflicts.
-- Flyway runs this transaction as the owning migration role. Its DDL locks prevent concurrent
-- access while owner visibility is enabled for the cross-tenant backfill; FORCE is restored below.
ALTER TABLE msgnet_transport_history NO FORCE ROW LEVEL SECURITY;
ALTER TABLE msgnet_projection_nonce NO FORCE ROW LEVEL SECURITY;
WITH approvals AS (
  SELECT n.*, dense_rank() OVER (
    PARTITION BY tenant_id, realm, installation_id, local_selector::jsonb ->> 'bundle_digest'
    ORDER BY (local_selector::jsonb ->> 'material_revision')::numeric DESC
  ) AS revision_rank
  FROM msgnet_projection_nonce n
  WHERE n.local_selector IS NOT NULL AND n.authorized_snapshot IS NOT NULL
)
INSERT INTO msgnet_transport_selector
  (id, tenant_id, realm, bundle_digest, reader_id, identity_id, selector, authorization_snapshot, ambiguous)
SELECT gen_random_uuid(), h.tenant_id, h.realm, h.bundle_digest, r.id, r.id,
  CASE WHEN a.variants = 1 THEN a.selector END,
  CASE WHEN a.variants = 1 THEN a.snapshot END,
  coalesce(a.variants > 1, false)
FROM msgnet_transport_history h
CROSS JOIN LATERAL jsonb_array_elements_text(h.predecessor::jsonb #> '{bundle,payload,required_reader_ids}') r(id)
LEFT JOIN LATERAL (
  SELECT count(DISTINCT n.local_selector) AS variants, min(n.local_selector) AS selector,
    min(n.authorized_snapshot) AS snapshot
  FROM approvals n
  WHERE n.tenant_id = h.tenant_id AND n.realm = h.realm AND n.installation_id = r.id
    AND n.revision_rank = 1
    AND n.local_selector::jsonb ->> 'bundle_digest' = h.bundle_digest
) a ON true;
ALTER TABLE msgnet_transport_history FORCE ROW LEVEL SECURITY;
ALTER TABLE msgnet_projection_nonce FORCE ROW LEVEL SECURITY;
SELECT auriga_enable_tenant_rls('msgnet_transport_selector');

CREATE FUNCTION msgnet_selector_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'message network selector is immutable' USING ERRCODE = '55000';
  END IF;
  IF OLD.selector IS NOT NULL OR OLD.ambiguous
    OR (to_jsonb(NEW) - 'selector' - 'authorization_snapshot') IS DISTINCT FROM
       (to_jsonb(OLD) - 'selector' - 'authorization_snapshot')
    OR NEW.selector IS NULL OR NEW.authorization_snapshot IS NULL THEN
    RAISE EXCEPTION 'message network selector is immutable' USING ERRCODE = '55000';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER msgnet_selector_immutable BEFORE UPDATE OR DELETE ON msgnet_transport_selector
  FOR EACH ROW EXECUTE FUNCTION msgnet_selector_immutable();
