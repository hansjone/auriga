-- One permanent minimal fact per committed redemption, independent of short-lived ingress/ticket.
-- Deliberately no FK to mutable/expiring identity or transport tables, and no TTL/GC.
CREATE TABLE iam.network_connection_anchor (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  ingress_id uuid NOT NULL,
  ingress_key varchar(26) NOT NULL CHECK (ingress_key ~ '^[a-z2-7]{26}$'),
  connection bytea NOT NULL CHECK (octet_length(connection)>0),
  realm numeric(20,0) NOT NULL CHECK (realm BETWEEN 1 AND 18446744073709551615),
  node numeric(20,0) NOT NULL CHECK (node BETWEEN 1 AND 18446744073709551615),
  owner_id uuid NOT NULL,
  device_id uuid NOT NULL,
  session_id uuid NOT NULL,
  redeemed_at timestamptz NOT NULL,
  CHECK (id::text ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
  UNIQUE (tenant_id,id),
  UNIQUE (tenant_id,ingress_id),
  UNIQUE (tenant_id,ingress_key)
);
SELECT iam.auriga_enable_tenant_rls('iam.network_connection_anchor');

CREATE FUNCTION iam.network_connection_anchor_immutable() RETURNS trigger
LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN
  RAISE EXCEPTION 'permanent network connection fact' USING ERRCODE='23514';
END $$;
CREATE TRIGGER network_connection_anchor_immutable
  BEFORE UPDATE OR DELETE ON iam.network_connection_anchor
  FOR EACH ROW EXECUTE FUNCTION iam.network_connection_anchor_immutable();
