-- Deployment metadata contains no tenant/user data. The first admission pins
-- currency for this database; changing deployment currency requires migration.
CREATE TABLE ai.budget_deployment (
 singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
 currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$')
);
CREATE TABLE ai.budget_subject (
 tenant_id uuid NOT NULL, subject uuid NOT NULL,
 active bigint NOT NULL DEFAULT 0 CHECK (active BETWEEN 0 AND 9007199254740991),
 held_tokens bigint NOT NULL DEFAULT 0 CHECK (held_tokens BETWEEN 0 AND 9007199254740991),
 held_micros bigint NOT NULL DEFAULT 0 CHECK (held_micros BETWEEN 0 AND 9007199254740991),
 PRIMARY KEY (tenant_id,subject)
);
CREATE TABLE ai.budget_period (
 tenant_id uuid NOT NULL, subject uuid NOT NULL,
 kind text NOT NULL CHECK (kind IN ('day','month')), start_at timestamptz NOT NULL,
 spent bigint NOT NULL DEFAULT 0 CHECK (spent BETWEEN 0 AND 9007199254740991),
 held bigint NOT NULL DEFAULT 0 CHECK (held BETWEEN 0 AND 9007199254740991),
 CHECK (spent <= 9007199254740991-held),
 PRIMARY KEY (tenant_id,subject,kind,start_at),
 FOREIGN KEY (tenant_id,subject) REFERENCES ai.budget_subject(tenant_id,subject)
);
CREATE TABLE ai.budget_request (
 tenant_id uuid NOT NULL, subject uuid NOT NULL, request_id uuid NOT NULL,
 method text NOT NULL, path text NOT NULL, key_hash bytea CHECK (octet_length(key_hash)=32),
 fingerprint bytea NOT NULL CHECK (octet_length(fingerprint)=32),
 admission bytea NOT NULL, price jsonb NOT NULL,
 state text NOT NULL CHECK (state IN ('reserved','dispatching','completed','failed','cancelled','unknown')),
 revision bigint NOT NULL DEFAULT 1 CHECK (revision BETWEEN 1 AND 9007199254740991),
 slot_held boolean NOT NULL DEFAULT true, reconciliation_reason text,
 created_at timestamptz NOT NULL,
 PRIMARY KEY (tenant_id,subject,request_id),
 FOREIGN KEY (tenant_id,subject) REFERENCES ai.budget_subject(tenant_id,subject),
 UNIQUE (tenant_id,subject,method,path,key_hash)
);
CREATE TABLE ai.budget_usage (
 tenant_id uuid NOT NULL, subject uuid NOT NULL, request_id uuid NOT NULL,
 document jsonb NOT NULL, recorded_at timestamptz NOT NULL,
 PRIMARY KEY (tenant_id,subject,request_id),
 FOREIGN KEY (tenant_id,subject,request_id) REFERENCES ai.budget_request(tenant_id,subject,request_id)
);
CREATE INDEX budget_usage_page ON ai.budget_usage(tenant_id,subject,recorded_at,request_id);
CREATE TABLE ai.budget_outbox (
 tenant_id uuid NOT NULL, subject uuid NOT NULL, id uuid NOT NULL, request_id uuid NOT NULL,
 envelope jsonb NOT NULL, created_at timestamptz NOT NULL,
 PRIMARY KEY (tenant_id,id), UNIQUE (tenant_id,subject,request_id),
 FOREIGN KEY (tenant_id,subject,request_id) REFERENCES ai.budget_usage(tenant_id,subject,request_id)
);
DO $migration$
DECLARE name text;
BEGIN
 FOREACH name IN ARRAY ARRAY['budget_subject','budget_period','budget_request','budget_usage','budget_outbox'] LOOP
  EXECUTE format('ALTER TABLE ai.%I ENABLE ROW LEVEL SECURITY',name);
  EXECUTE format('ALTER TABLE ai.%I FORCE ROW LEVEL SECURITY',name);
  EXECUTE format('CREATE POLICY tenant_isolation ON ai.%I USING (tenant_id = nullif(current_setting(''auriga.tenant_id'',true),'''')::uuid) WITH CHECK (tenant_id = nullif(current_setting(''auriga.tenant_id'',true),'''')::uuid)',name);
 END LOOP;
END $migration$;
