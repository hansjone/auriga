-- Reuses the ordinary immutable mailbox rows. This is an atomic producer-key receipt,
-- not a second delivery/attempt engine; a crash commits both the message and receipt or neither.
CREATE TABLE network_shared_submit (
 tenant_id uuid NOT NULL, context_id uuid NOT NULL, producer_key uuid NOT NULL,
 request_digest text NOT NULL, result_json text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(tenant_id,context_id,producer_key)
);
SELECT auriga_enable_tenant_rls('network_shared_submit');
