#!/bin/sh
# Idempotent provisioning for a fresh or existing PostgreSQL volume. No data migration.
(
set -eu
secret_dir=${BIFROST_SECRET_DIR:-/run/secrets}
password=${BIFROST_RUNTIME_PASSWORD:-$(cat "$secret_dir/bifrost_pg_password")}
migration_password=${BIFROST_MIGRATION_PASSWORD:-$(cat "$secret_dir/bifrost_pg_migration_password")}
for value in "$password" "$migration_password"; do
 case "$value" in ''|*[!0-9a-f]*) exit 1 ;; esac
 [ "${#value}" -eq 64 ] || exit 1
done
[ "$password" != "$migration_password" ] || exit 1
# One-off upgrade helpers connect to the existing server; entrypoint init uses its socket.
if [ -n "${PGHOST:-}" ]; then
 PGPASSWORD=${POSTGRES_PASSWORD:-}
 if [ -z "$PGPASSWORD" ] && [ -n "${POSTGRES_PASSWORD_FILE:-}" ]; then PGPASSWORD=$(cat "$POSTGRES_PASSWORD_FILE"); fi
 export PGPASSWORD
fi
"${BIFROST_PSQL:-psql}" -X -q -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<SQL
SELECT pg_advisory_lock(718245011);
DO \$guard\$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname IN ('auriga_bifrost_owner','auriga_bifrost') AND (rolsuper OR rolbypassrls OR rolcreatedb OR rolcreaterole)) THEN
  RAISE EXCEPTION 'existing relay role is privileged; operator review required';
 END IF;
 IF EXISTS(SELECT 1 FROM pg_database WHERE datname='bifrost' AND pg_get_userbyid(datdba)<>'auriga_bifrost_owner') THEN
  RAISE EXCEPTION 'existing relay database has a different owner';
 END IF;
END \$guard\$;
SELECT 'CREATE ROLE auriga_bifrost_owner LOGIN PASSWORD ''$migration_password'' NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE' WHERE NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='auriga_bifrost_owner') \gexec
SELECT 'CREATE ROLE auriga_bifrost LOGIN PASSWORD ''$password'' NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE' WHERE NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='auriga_bifrost') \gexec
SELECT 'CREATE DATABASE bifrost OWNER auriga_bifrost_owner ENCODING ''UTF8''' WHERE NOT EXISTS(SELECT 1 FROM pg_database WHERE datname='bifrost') \gexec
REVOKE ALL ON DATABASE bifrost FROM PUBLIC;
GRANT CONNECT ON DATABASE bifrost TO auriga_bifrost;
\connect bifrost
DO \$guard\$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_namespace WHERE nspname='ai' AND pg_get_userbyid(nspowner)<>'auriga_bifrost_owner') THEN
  RAISE EXCEPTION 'existing relay schema has a different owner';
 END IF;
END \$guard\$;
CREATE SCHEMA IF NOT EXISTS ai AUTHORIZATION auriga_bifrost_owner;
GRANT USAGE ON SCHEMA ai TO auriga_bifrost;
ALTER DEFAULT PRIVILEGES FOR ROLE auriga_bifrost_owner IN SCHEMA ai GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO auriga_bifrost;
GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA ai TO auriga_bifrost;
DO \$guard\$ BEGIN
 IF to_regclass('ai.schema_migration') IS NOT NULL THEN REVOKE ALL ON ai.schema_migration FROM auriga_bifrost; END IF;
END \$guard\$;
SQL
)
