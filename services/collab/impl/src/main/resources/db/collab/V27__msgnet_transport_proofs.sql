-- Public immutable history and exact completed-stage evidence; no private material.
CREATE TABLE msgnet_transport_history (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  realm text NOT NULL,
  bundle_digest text NOT NULL,
  generation numeric(20,0) NOT NULL,
  bundle_version numeric(20,0) NOT NULL,
  predecessor text NOT NULL,
  UNIQUE (tenant_id, realm, bundle_digest),
  UNIQUE (tenant_id, realm, generation, bundle_version)
);
CREATE TABLE msgnet_transport_transition (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  realm text NOT NULL,
  authority_digest text NOT NULL,
  generation numeric(20,0) NOT NULL,
  proof text NOT NULL,
  UNIQUE (tenant_id, realm, authority_digest, generation)
);
CREATE TABLE msgnet_transport_stage (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  realm text NOT NULL,
  bundle_digest text NOT NULL,
  readers text NOT NULL,
  UNIQUE (tenant_id, realm, bundle_digest)
);
SELECT auriga_enable_tenant_rls('msgnet_transport_history');
SELECT auriga_enable_tenant_rls('msgnet_transport_transition');
SELECT auriga_enable_tenant_rls('msgnet_transport_stage');
CREATE FUNCTION msgnet_proof_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'message network proof is immutable' USING ERRCODE = '55000';
END;
$$;
CREATE TRIGGER msgnet_history_immutable BEFORE UPDATE OR DELETE ON msgnet_transport_history
  FOR EACH ROW EXECUTE FUNCTION msgnet_proof_immutable();
CREATE TRIGGER msgnet_transition_immutable BEFORE UPDATE OR DELETE ON msgnet_transport_transition
  FOR EACH ROW EXECUTE FUNCTION msgnet_proof_immutable();
CREATE TRIGGER msgnet_stage_immutable BEFORE UPDATE OR DELETE ON msgnet_transport_stage
  FOR EACH ROW EXECUTE FUNCTION msgnet_proof_immutable();
