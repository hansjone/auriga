-- Disposable M2 credentials only; existing volumes need an explicit DBA migration.
CREATE ROLE auriga_collab_owner LOGIN PASSWORD 'e2e_collab_owner'
  NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
CREATE ROLE auriga_collab_app LOGIN PASSWORD 'e2e_collab_app'
  NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
CREATE SCHEMA collab AUTHORIZATION auriga_collab_owner;
GRANT USAGE ON SCHEMA collab TO auriga_collab_app;
ALTER ROLE auriga_collab_owner SET search_path TO collab;
ALTER ROLE auriga_collab_app SET search_path TO collab;
ALTER DEFAULT PRIVILEGES FOR ROLE auriga_collab_owner IN SCHEMA collab
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO auriga_collab_app;
ALTER DEFAULT PRIVILEGES FOR ROLE auriga_collab_owner IN SCHEMA collab
  GRANT USAGE, SELECT ON SEQUENCES TO auriga_collab_app;
