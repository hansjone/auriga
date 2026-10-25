-- Portal visibility checks all registered source nodes owned by the authenticated user.
CREATE INDEX network_node_owner_directory ON iam.network_node (tenant_id,account_id,node)
  WHERE NOT revoked;
