-- Internal persistence only; no service, policy revision or authorization endpoint is enabled.
-- Redeem rows are short lived. Retain their real PK/tuple and IAM ownership associations here.
CREATE TABLE iam.network_source_anchor (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  ingress_id uuid NOT NULL,
  connection bytea NOT NULL,
  realm numeric(20,0) NOT NULL CHECK (realm BETWEEN 1 AND 18446744073709551615),
  node numeric(20,0) NOT NULL CHECK (node BETWEEN 1 AND 18446744073709551615),
  owner_id uuid NOT NULL,
  device_id uuid NOT NULL,
  session_id uuid NOT NULL,
  UNIQUE (tenant_id,id)
);
SELECT iam.auriga_enable_tenant_rls('iam.network_source_anchor');

-- First anchor fixes the lineage identity independently of reconnecting transports.
CREATE TABLE iam.network_source_chain (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  registration_id varchar(128) NOT NULL CHECK (registration_id ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$'),
  registration bytea NOT NULL,
  scope smallint NOT NULL CHECK (scope=1),
  anchor_id uuid NOT NULL,
  head uuid,
  stopped boolean NOT NULL DEFAULT false,
  UNIQUE (tenant_id,id),
  UNIQUE (tenant_id,registration_id,scope),
  FOREIGN KEY (tenant_id,anchor_id) REFERENCES iam.network_source_anchor(tenant_id,id)
);
SELECT iam.auriga_enable_tenant_rls('iam.network_source_chain');

CREATE TABLE iam.network_source (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  chain_id uuid NOT NULL,
  anchor_id uuid NOT NULL,
  predecessor uuid,
  fact bytea NOT NULL,
  not_before timestamptz NOT NULL,
  expires_at timestamptz NOT NULL,
  state smallint NOT NULL CHECK (state IN (1,2,3)),
  revision numeric(20,0) NOT NULL CHECK (revision BETWEEN 1 AND 18446744073709551615),
  CHECK (not_before < expires_at AND date_trunc('second',not_before)=not_before AND date_trunc('second',expires_at)=expires_at),
  UNIQUE (tenant_id,id),
  UNIQUE (tenant_id,chain_id,id),
  UNIQUE NULLS NOT DISTINCT (tenant_id,chain_id,predecessor),
  FOREIGN KEY (tenant_id,chain_id) REFERENCES iam.network_source_chain(tenant_id,id),
  FOREIGN KEY (tenant_id,anchor_id) REFERENCES iam.network_source_anchor(tenant_id,id),
  FOREIGN KEY (tenant_id,chain_id,predecessor) REFERENCES iam.network_source(tenant_id,chain_id,id)
);
SELECT iam.auriga_enable_tenant_rls('iam.network_source');
ALTER TABLE iam.network_source_chain ADD FOREIGN KEY (tenant_id,id,head)
  REFERENCES iam.network_source(tenant_id,chain_id,id) DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE iam.network_source_operation (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  method varchar(8) NOT NULL CHECK (method IN ('Register','Revoke')),
  operation_id varchar(128) NOT NULL CHECK (operation_id ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$'),
  request_digest bytea NOT NULL CHECK (octet_length(request_digest)=32),
  request bytea NOT NULL,
  response bytea NOT NULL,
  source_id uuid NOT NULL,
  UNIQUE (tenant_id,method,operation_id),
  FOREIGN KEY (tenant_id,source_id) REFERENCES iam.network_source(tenant_id,id)
);
SELECT iam.auriga_enable_tenant_rls('iam.network_source_operation');

-- Runtime DML cannot erase tombstones, rebind immutable facts or revive terminal generations.
CREATE FUNCTION iam.network_source_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' OR TG_TABLE_NAME IN ('network_source_anchor','network_source_operation') THEN
    RAISE EXCEPTION 'permanent network source fact' USING ERRCODE='23514';
  END IF;
  IF TG_TABLE_NAME='network_source_chain' THEN
    IF (to_jsonb(NEW)-ARRAY['head','stopped']) IS DISTINCT FROM (to_jsonb(OLD)-ARRAY['head','stopped'])
       OR (OLD.stopped AND NOT NEW.stopped) THEN
      RAISE EXCEPTION 'immutable network source chain' USING ERRCODE='23514';
    END IF;
  ELSIF (to_jsonb(NEW)-ARRAY['state','revision']) IS DISTINCT FROM (to_jsonb(OLD)-ARRAY['state','revision'])
       OR OLD.state<>1 OR NEW.state NOT IN (2,3) OR NEW.revision<>OLD.revision+1 THEN
    RAISE EXCEPTION 'invalid network source transition' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER network_source_anchor_immutable BEFORE UPDATE OR DELETE ON iam.network_source_anchor
  FOR EACH ROW EXECUTE FUNCTION iam.network_source_immutable();
CREATE TRIGGER network_source_chain_immutable BEFORE UPDATE OR DELETE ON iam.network_source_chain
  FOR EACH ROW EXECUTE FUNCTION iam.network_source_immutable();
CREATE TRIGGER network_source_fact_immutable BEFORE UPDATE OR DELETE ON iam.network_source
  FOR EACH ROW EXECUTE FUNCTION iam.network_source_immutable();
CREATE TRIGGER network_source_operation_immutable BEFORE UPDATE OR DELETE ON iam.network_source_operation
  FOR EACH ROW EXECUTE FUNCTION iam.network_source_immutable();
