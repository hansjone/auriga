CREATE TABLE msgnet_transport (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  realm text NOT NULL,
  bundle text NOT NULL,
  UNIQUE (tenant_id, realm)
);
CREATE TABLE msgnet_transport_leaf (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  realm text NOT NULL,
  identity_id text NOT NULL,
  role text NOT NULL,
  ca_id text NOT NULL,
  kid text NOT NULL,
  leaf text NOT NULL,
  UNIQUE (tenant_id, realm, identity_id, role)
);
CREATE TABLE msgnet_projection_receipt (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  realm text NOT NULL,
  installation_id text NOT NULL,
  bundle_version numeric(20,0) NOT NULL,
  receipt text NOT NULL,
  UNIQUE (tenant_id, realm, installation_id, bundle_version)
);
SELECT auriga_enable_tenant_rls('msgnet_transport');
SELECT auriga_enable_tenant_rls('msgnet_transport_leaf');
SELECT auriga_enable_tenant_rls('msgnet_projection_receipt');
