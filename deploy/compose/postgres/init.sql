-- M2 compose bootstrap roles (test passwords only; not for production).
-- iam/org: single role owns schema + runs migrations (combined profile).
-- workspace: separate owner (DDL/migrate) and app (DML) roles.

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'auriga_iam') THEN
    CREATE ROLE auriga_iam LOGIN PASSWORD 'e2e_iam_only' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'auriga_org') THEN
    CREATE ROLE auriga_org LOGIN PASSWORD 'e2e_org_only' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'auriga_workspace_owner') THEN
    CREATE ROLE auriga_workspace_owner LOGIN PASSWORD 'e2e_ws_owner' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'auriga_workspace_app') THEN
    CREATE ROLE auriga_workspace_app LOGIN PASSWORD 'e2e_ws_app' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
END $$;

CREATE SCHEMA IF NOT EXISTS iam AUTHORIZATION auriga_iam;
CREATE SCHEMA IF NOT EXISTS org AUTHORIZATION auriga_org;
CREATE SCHEMA IF NOT EXISTS workspace AUTHORIZATION auriga_workspace_owner;

GRANT USAGE, CREATE ON SCHEMA iam TO auriga_iam;
GRANT USAGE, CREATE ON SCHEMA org TO auriga_org;
GRANT USAGE, CREATE ON SCHEMA workspace TO auriga_workspace_owner;
GRANT USAGE ON SCHEMA workspace TO auriga_workspace_app;

ALTER ROLE auriga_iam SET search_path TO iam;
ALTER ROLE auriga_org SET search_path TO org;
ALTER ROLE auriga_workspace_owner SET search_path TO workspace;
ALTER ROLE auriga_workspace_app SET search_path TO workspace;

ALTER DEFAULT PRIVILEGES FOR ROLE auriga_workspace_owner IN SCHEMA workspace
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO auriga_workspace_app;
ALTER DEFAULT PRIVILEGES FOR ROLE auriga_workspace_owner IN SCHEMA workspace
  GRANT USAGE, SELECT ON SEQUENCES TO auriga_workspace_app;
