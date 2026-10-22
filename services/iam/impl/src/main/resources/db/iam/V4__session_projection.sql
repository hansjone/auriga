-- Durable, coalesced work: no session value or credential is copied into the queue.
CREATE TABLE iam.session_projection (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    cookie_hash text NOT NULL,
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, cookie_hash)
);
SELECT iam.auriga_enable_tenant_rls('iam.session_projection');
CREATE INDEX session_projection_due ON iam.session_projection (tenant_id, next_attempt_at, id);

-- Infrastructure discovery only, like the common outbox: deliberately contains only tenant IDs.
-- Payload and work claims always use the tenant-scoped FORCE RLS table above.
CREATE TABLE iam.session_projection_tenant (tenant_id uuid PRIMARY KEY);
