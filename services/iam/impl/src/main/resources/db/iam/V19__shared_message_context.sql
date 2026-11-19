-- Distinct identity class: no fabricated device, owner or NATS connection.
CREATE TABLE iam.shared_message_context (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, lookup_key text NOT NULL, data jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz NOT NULL,
 UNIQUE(tenant_id,lookup_key), UNIQUE(tenant_id,id),
 realm numeric(20,0) GENERATED ALWAYS AS ((data->>'realm')::numeric) STORED NOT NULL CHECK(realm>0 AND realm<=18446744073709551615),
 node numeric(20,0) GENERATED ALWAYS AS ((data->>'node')::numeric) STORED NOT NULL CHECK(node>0 AND node<=18446744073709551615),
 UNIQUE(tenant_id,realm,node),
 native_key text GENERATED ALWAYS AS (data->>'nativeKey') STORED NOT NULL,
 UNIQUE(tenant_id,native_key),
 session_key text GENERATED ALWAYS AS (data->>'sessionKey') STORED NOT NULL,
 CHECK(data->'binding'->>'tenantId'=tenant_id::text)
);
CREATE UNIQUE INDEX shared_message_live_native
 ON iam.shared_message_context(tenant_id,session_key) WHERE data->>'revoked'='false';
SELECT iam.auriga_enable_tenant_rls('iam.shared_message_context');
