CREATE TABLE msgnet_migration (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  realm text NOT NULL,
  permit_id text NOT NULL,
  permit text NOT NULL,
  archive text NOT NULL,
  receipt text NOT NULL,
  committed boolean NOT NULL,
  recorded_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE UNIQUE INDEX msgnet_migration_once ON msgnet_migration (tenant_id, realm, permit_id) WHERE committed;
SELECT auriga_enable_tenant_rls('msgnet_migration');

-- Serialize even old binaries at the database boundary before testing the durable barrier.
CREATE OR REPLACE FUNCTION msgnet_reject_legacy_publication() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  PERFORM pg_advisory_xact_lock(hashtextextended(NEW.tenant_id::text || ':network-keys:' || NEW.realm, 0));
  IF EXISTS (SELECT 1 FROM collab.msgnet_realm
      WHERE tenant_id = NEW.tenant_id AND realm = NEW.realm AND legacy_disabled) THEN
    RAISE EXCEPTION 'Message network v2 barrier rejects legacy publication' USING ERRCODE = '55000';
  END IF;
  RETURN NEW;
END;
$$;
