#!/bin/sh
(
set -eu
# Fresh volumes only. LiteLLM data is deliberately not migrated.
password=$(cat /run/secrets/bifrost_pg_password)
migration_password=$(cat /run/secrets/bifrost_pg_migration_password)
for value in "$password" "$migration_password"; do
  case "$value" in ''|*[!0-9a-f]*) exit 1 ;; esac
  [ "${#value}" -eq 64 ] || exit 1
done
[ "$password" != "$migration_password" ] || exit 1
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<SQL
CREATE ROLE auriga_bifrost_owner LOGIN PASSWORD '$migration_password' NOSUPERUSER NOCREATEDB NOCREATEROLE;
CREATE ROLE auriga_bifrost LOGIN PASSWORD '$password' NOSUPERUSER NOCREATEDB NOCREATEROLE;
CREATE DATABASE bifrost OWNER auriga_bifrost_owner ENCODING 'UTF8';
REVOKE ALL ON DATABASE bifrost FROM PUBLIC;
GRANT CONNECT ON DATABASE bifrost TO auriga_bifrost;
\connect bifrost
CREATE SCHEMA ai AUTHORIZATION auriga_bifrost_owner;
GRANT USAGE ON SCHEMA ai TO auriga_bifrost;
ALTER DEFAULT PRIVILEGES FOR ROLE auriga_bifrost_owner IN SCHEMA ai GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO auriga_bifrost;
SQL
)
