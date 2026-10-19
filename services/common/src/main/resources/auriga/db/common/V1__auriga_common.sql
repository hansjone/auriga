-- Infrastructure tables and helpers shared by every service (applied by common into the
-- service's own schema, history table auriga_common_schema_history).

-- Tenant row level security -------------------------------------------------------------

-- Current tenant of the transaction (set by TenantTransactionManager with SET LOCAL semantics).
-- Unset or empty means "no tenant": policies then match no rows.
CREATE OR REPLACE FUNCTION ${flyway:defaultSchema}.auriga_current_tenant() RETURNS uuid
  LANGUAGE sql STABLE PARALLEL SAFE
  AS $$ SELECT NULLIF(current_setting('auriga.tenant_id', true), '')::uuid $$;

-- Enables tenant isolation on a business table that has a tenant_id uuid column:
--   SELECT auriga_enable_tenant_rls('member');
-- FORCE makes the policy apply to the table owner (the service role) as well. The service role
-- must not be a superuser and must not have BYPASSRLS.
CREATE OR REPLACE FUNCTION ${flyway:defaultSchema}.auriga_enable_tenant_rls(target regclass) RETURNS void
  LANGUAGE plpgsql
  AS $$
BEGIN
  EXECUTE format('ALTER TABLE %s ENABLE ROW LEVEL SECURITY', target);
  EXECUTE format('ALTER TABLE %s FORCE ROW LEVEL SECURITY', target);
  EXECUTE format('DROP POLICY IF EXISTS auriga_tenant_isolation ON %s', target);
  EXECUTE format(
    'CREATE POLICY auriga_tenant_isolation ON %s USING (tenant_id = ${flyway:defaultSchema}.auriga_current_tenant()) '
    'WITH CHECK (tenant_id = ${flyway:defaultSchema}.auriga_current_tenant())',
    target);
END
$$;

-- Outbox ----------------------------------------------------------------------------------

CREATE TABLE ${flyway:defaultSchema}.outbox_event (
  id uuid PRIMARY KEY,
  seq bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
  tenant_id uuid NOT NULL,
  event_type text NOT NULL,
  -- Ordering key: events with the same key are delivered in seq order.
  subject_key text NOT NULL,
  nats_subject text NOT NULL,
  -- The CloudEvents envelope exactly as published.
  envelope text NOT NULL,
  created_at timestamptz NOT NULL,
  published_at timestamptz,
  attempts integer NOT NULL DEFAULT 0,
  next_attempt_at timestamptz NOT NULL,
  last_error text
);

CREATE INDEX outbox_event_pending_by_key
  ON ${flyway:defaultSchema}.outbox_event (subject_key, seq) WHERE published_at IS NULL;
CREATE INDEX outbox_event_pending_due
  ON ${flyway:defaultSchema}.outbox_event (next_attempt_at, seq) WHERE published_at IS NULL;
CREATE INDEX outbox_event_published
  ON ${flyway:defaultSchema}.outbox_event (published_at) WHERE published_at IS NOT NULL;

-- Consumer side -------------------------------------------------------------------------------

-- One row per (consumer, event): written in the same transaction as the handler's changes.
CREATE TABLE ${flyway:defaultSchema}.processed_event (
  consumer text NOT NULL,
  event_id uuid NOT NULL,
  tenant_id uuid NOT NULL,
  event_type text NOT NULL,
  processed_at timestamptz NOT NULL,
  PRIMARY KEY (consumer, event_id)
);

CREATE INDEX processed_event_processed_at
  ON ${flyway:defaultSchema}.processed_event (processed_at);

-- Events a consumer gave up on after the configured number of deliveries.
CREATE TABLE ${flyway:defaultSchema}.dead_letter_event (
  id uuid PRIMARY KEY,
  consumer text NOT NULL,
  event_id uuid,
  tenant_id uuid,
  event_type text,
  payload text NOT NULL,
  error text,
  attempts integer NOT NULL,
  created_at timestamptz NOT NULL
);
