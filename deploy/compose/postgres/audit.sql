-- Dedicated roles are idempotently provisioned for fresh AND existing databases.
BEGIN;
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='auriga_audit_owner') THEN
  CREATE ROLE auriga_audit_owner LOGIN PASSWORD 'e2e_audit_owner' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
 END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='auriga_audit_app') THEN
  CREATE ROLE auriga_audit_app LOGIN PASSWORD 'e2e_audit_app' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
 END IF;
END $$;
CREATE SCHEMA IF NOT EXISTS audit AUTHORIZATION auriga_audit_owner;
GRANT USAGE,CREATE ON SCHEMA audit TO auriga_audit_owner;
GRANT USAGE ON SCHEMA audit TO auriga_audit_app;
ALTER ROLE auriga_audit_owner SET search_path TO audit;
ALTER ROLE auriga_audit_app SET search_path TO audit;
ALTER DEFAULT PRIVILEGES FOR ROLE auriga_audit_owner IN SCHEMA audit GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO auriga_audit_app;
ALTER DEFAULT PRIVILEGES FOR ROLE auriga_audit_owner IN SCHEMA audit GRANT USAGE,SELECT ON SEQUENCES TO auriga_audit_app;
COMMIT;
