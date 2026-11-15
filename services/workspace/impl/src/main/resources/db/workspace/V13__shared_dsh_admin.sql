CREATE TABLE shared_dsh_entitlement (
 tenant_id uuid NOT NULL, member_id uuid NOT NULL, granted boolean NOT NULL DEFAULT false,
 revision bigint NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 PRIMARY KEY(tenant_id,member_id)
);
CREATE TABLE shared_dsh_rebuild_plan (
 tenant_id uuid NOT NULL, id uuid NOT NULL, revision bigint NOT NULL DEFAULT 1,
 started boolean NOT NULL DEFAULT false, request text NOT NULL,
 PRIMARY KEY(tenant_id,id)
);
CREATE TABLE shared_dsh_admin_request (
 tenant_id uuid NOT NULL, actor_id uuid NOT NULL, kind text NOT NULL, target text NOT NULL,
 idempotency_key text NOT NULL, fingerprint text NOT NULL, response text NOT NULL,
 etag text NOT NULL, created_at bigint NOT NULL,
 PRIMARY KEY(tenant_id,actor_id,kind,target,idempotency_key)
);
SELECT auriga_enable_tenant_rls('shared_dsh_entitlement');
SELECT auriga_enable_tenant_rls('shared_dsh_rebuild_plan');
SELECT auriga_enable_tenant_rls('shared_dsh_admin_request');
