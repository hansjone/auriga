CREATE TABLE ai.provider (
 tenant_id uuid NOT NULL, id uuid NOT NULL, document jsonb NOT NULL,
 revision bigint NOT NULL CHECK (revision > 0), created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (tenant_id,id)
);
CREATE TABLE ai.credential (
 tenant_id uuid NOT NULL, id uuid NOT NULL, provider_id uuid NOT NULL,
 state text NOT NULL CHECK (state IN ('staged','active','retiring','revoked')),
 ciphertext bytea NOT NULL, cipher_version integer NOT NULL CHECK (cipher_version > 0),
 revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0), created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (tenant_id,id), FOREIGN KEY (tenant_id,provider_id) REFERENCES ai.provider(tenant_id,id)
);
CREATE UNIQUE INDEX credential_active ON ai.credential(tenant_id,provider_id) WHERE state='active';
CREATE TABLE ai.model (
 tenant_id uuid NOT NULL, id text NOT NULL, provider_id uuid NOT NULL, document jsonb NOT NULL,
 grants jsonb NOT NULL DEFAULT '{"items":[]}', revision bigint NOT NULL CHECK (revision > 0),
 created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY (tenant_id,id),
 FOREIGN KEY (tenant_id,provider_id) REFERENCES ai.provider(tenant_id,id)
);
CREATE TABLE ai.idempotency (
 tenant_id uuid NOT NULL, subject text NOT NULL, method text NOT NULL, path text NOT NULL, key text NOT NULL,
 fingerprint bytea NOT NULL, status integer NOT NULL, body bytea NOT NULL, etag text NOT NULL,
 expires_at timestamptz NOT NULL DEFAULT (now()+interval '24 hours'),
 PRIMARY KEY (tenant_id,subject,method,path,key)
);
DO $migration$
DECLARE name text;
BEGIN
 FOREACH name IN ARRAY ARRAY['provider','credential','model','idempotency'] LOOP
  EXECUTE format('ALTER TABLE ai.%I ENABLE ROW LEVEL SECURITY',name);
  EXECUTE format('ALTER TABLE ai.%I FORCE ROW LEVEL SECURITY',name);
  EXECUTE format('CREATE POLICY tenant_isolation ON ai.%I USING (tenant_id = nullif(current_setting(''auriga.tenant_id'',true),'''')::uuid) WITH CHECK (tenant_id = nullif(current_setting(''auriga.tenant_id'',true),'''')::uuid)',name);
 END LOOP;
END $migration$;
