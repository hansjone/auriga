-- A Finish that overtakes Begin must remain effective even without a live
-- session/task row. Full-binding lookup prevents cross-binding cancellation.
CREATE TABLE iam.shared_task_end (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, lookup_key text NOT NULL,
 data jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz,
 UNIQUE(tenant_id,lookup_key), UNIQUE(tenant_id,id),
 CHECK(data->>'tenantId'=tenant_id::text),
 CHECK(data->>'ended'='true')
);
SELECT iam.auriga_enable_tenant_rls('iam.shared_task_end');
