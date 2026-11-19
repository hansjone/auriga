CREATE TABLE iam.shared_task (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, lookup_key text NOT NULL, data jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz NOT NULL,
 UNIQUE(tenant_id,lookup_key), UNIQUE(tenant_id,id),
 platform_session_id uuid GENERATED ALWAYS AS ((data->'binding'->>'platformSessionId')::uuid) STORED NOT NULL,
 FOREIGN KEY(tenant_id,platform_session_id) REFERENCES iam.session(tenant_id,id),
 CHECK (lookup_key=id::text AND data->>'taskId'=id::text),
 CHECK (data->>'tenantId'=tenant_id::text)
);
CREATE TABLE iam.shared_capability (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, lookup_key text NOT NULL, data jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz NOT NULL,
 UNIQUE(tenant_id,lookup_key), UNIQUE(tenant_id,id),
 task_id uuid GENERATED ALWAYS AS ((data->'claims'->>'taskId')::uuid) STORED NOT NULL,
 FOREIGN KEY(tenant_id,task_id) REFERENCES iam.shared_task(tenant_id,id),
 CHECK (lookup_key=id::text AND data->'claims'->>'jti'=id::text),
 CHECK (data->'claims'->'binding'->>'tenantId'=tenant_id::text)
);
CREATE INDEX shared_capability_task ON iam.shared_capability(tenant_id,task_id,expires_at);
SELECT iam.auriga_enable_tenant_rls('iam.shared_task');
SELECT iam.auriga_enable_tenant_rls('iam.shared_capability');
