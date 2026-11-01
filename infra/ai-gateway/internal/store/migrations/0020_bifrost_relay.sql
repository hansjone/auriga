-- The maintained Bifrost core has no transport/config-store dependency here.
-- Auriga reuses its existing tenant-scoped budget ledger and usage records.
CREATE TABLE ai.relay_provider (
 tenant_id uuid NOT NULL, id uuid NOT NULL, document jsonb NOT NULL,
 secret bytea, PRIMARY KEY (tenant_id,id)
);
CREATE TABLE ai.relay_pool (
 tenant_id uuid NOT NULL, id text NOT NULL, document jsonb NOT NULL,
 PRIMARY KEY (tenant_id,id)
);
CREATE TABLE ai.relay_key (
 tenant_id uuid NOT NULL, id uuid NOT NULL, name text NOT NULL,
 document jsonb NOT NULL, minute timestamptz NOT NULL DEFAULT date_trunc('minute',now()),
 requests bigint NOT NULL DEFAULT 0 CHECK (requests>=0),
 PRIMARY KEY (tenant_id,id), UNIQUE (tenant_id,name)
);
CREATE TABLE ai.relay_admin_request (
 tenant_id uuid NOT NULL, id text NOT NULL, fingerprint bytea NOT NULL,
 response bytea, PRIMARY KEY (tenant_id,id)
);
DO $migration$
DECLARE name text;
BEGIN
 FOREACH name IN ARRAY ARRAY['relay_provider','relay_pool','relay_key','relay_admin_request'] LOOP
  EXECUTE format('ALTER TABLE ai.%I ENABLE ROW LEVEL SECURITY',name);
  EXECUTE format('ALTER TABLE ai.%I FORCE ROW LEVEL SECURITY',name);
  EXECUTE format('CREATE POLICY tenant_isolation ON ai.%I USING (tenant_id = nullif(current_setting(''auriga.tenant_id'',true),'''')::uuid) WITH CHECK (tenant_id = nullif(current_setting(''auriga.tenant_id'',true),'''')::uuid)',name);
 END LOOP;
END $migration$;
