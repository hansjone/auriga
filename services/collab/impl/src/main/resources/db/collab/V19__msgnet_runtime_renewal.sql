CREATE TABLE msgnet_realm (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  realm text NOT NULL,
  root_control text NOT NULL,
  runtime_authority text NOT NULL,
  publication text,
  announcement text,
  generation numeric(20,0) NOT NULL CHECK (generation BETWEEN 1 AND 18446744073709551615),
  keyset_version numeric(20,0) NOT NULL CHECK (keyset_version BETWEEN 0 AND 18446744073709551615),
  paused boolean NOT NULL DEFAULT false,
  legacy_disabled boolean NOT NULL DEFAULT false,
  failure_code text NOT NULL DEFAULT 'none',
  UNIQUE (tenant_id, realm)
);
CREATE TABLE msgnet_key_history (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  realm text NOT NULL,
  kid text NOT NULL,
  public_key_spki text NOT NULL,
  key_material text NOT NULL,
  first_publication text NOT NULL,
  UNIQUE (tenant_id, realm, kid),
  UNIQUE (tenant_id, realm, public_key_spki)
);
CREATE TABLE msgnet_publication_history (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  realm text NOT NULL,
  generation numeric(20,0) NOT NULL,
  keyset_version numeric(20,0) NOT NULL,
  publication text NOT NULL,
  announcement text NOT NULL,
  pending_push boolean NOT NULL DEFAULT true,
  committed_at timestamptz NOT NULL,
  UNIQUE (tenant_id, realm, generation, keyset_version)
);
CREATE TABLE msgnet_control_target (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  realm text NOT NULL,
  target_id text NOT NULL,
  identity_id text NOT NULL,
  identity_spki text NOT NULL,
  control_package text NOT NULL,
  receipt text,
  enforcement_confirmed boolean NOT NULL DEFAULT false,
  isolation_record text NOT NULL,
  UNIQUE (tenant_id, realm, target_id)
);
CREATE TABLE msgnet_projection_nonce (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  realm text NOT NULL,
  installation_id text NOT NULL,
  nonce text NOT NULL,
  UNIQUE (tenant_id, realm, installation_id, nonce)
);
SELECT auriga_enable_tenant_rls('msgnet_realm');
SELECT auriga_enable_tenant_rls('msgnet_key_history');
SELECT auriga_enable_tenant_rls('msgnet_publication_history');
SELECT auriga_enable_tenant_rls('msgnet_control_target');
SELECT auriga_enable_tenant_rls('msgnet_projection_nonce');

-- Enforce the barrier for older operator binaries which do not know msgnet_realm.
CREATE FUNCTION msgnet_reject_legacy_publication() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF EXISTS (SELECT 1 FROM collab.msgnet_realm
      WHERE tenant_id = NEW.tenant_id AND realm = NEW.realm AND legacy_disabled) THEN
    RAISE EXCEPTION 'Message network v2 barrier rejects legacy publication' USING ERRCODE = '55000';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER msgnet_legacy_barrier BEFORE INSERT OR UPDATE ON network_key_publication
  FOR EACH ROW EXECUTE FUNCTION msgnet_reject_legacy_publication();
