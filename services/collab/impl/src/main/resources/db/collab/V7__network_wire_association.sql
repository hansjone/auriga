-- Wire association and proof records share the mailbox transaction and node lock.
ALTER TABLE network_node ADD COLUMN wire_binding_json text;
ALTER TABLE network_node ADD COLUMN wire_ready_json text;
ALTER TABLE network_node ADD COLUMN wire_incarnation text;
ALTER TABLE network_node ADD COLUMN wire_nonce text;
ALTER TABLE network_node ADD COLUMN wire_status text;

CREATE TABLE network_wire_ingress (
  tenant_id uuid NOT NULL,
  realm text NOT NULL,
  node_id text NOT NULL,
  ingress_id text NOT NULL,
  PRIMARY KEY (tenant_id, ingress_id),
  FOREIGN KEY (tenant_id, realm, node_id) REFERENCES network_node (tenant_id, realm, node_id)
);
SELECT auriga_enable_tenant_rls('network_wire_ingress');

CREATE TABLE network_wire_receipt (
  tenant_id uuid NOT NULL,
  realm text NOT NULL,
  node_id text NOT NULL,
  operation_id text NOT NULL,
  request_json text NOT NULL,
  result_json text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, realm, node_id, operation_id),
  FOREIGN KEY (tenant_id, realm, node_id) REFERENCES network_node (tenant_id, realm, node_id)
);
SELECT auriga_enable_tenant_rls('network_wire_receipt');
