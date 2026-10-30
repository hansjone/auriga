#!/bin/sh
(
set -eu
# This init hook runs only on a fresh database volume, after existing role setup.
password=$(cat /run/secrets/litellm_pg_password)
case "$password" in ''|*[!0-9a-f]*) exit 1 ;; esac
[ "${#password}" -eq 64 ]
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<SQL
CREATE ROLE auriga_litellm LOGIN PASSWORD '$password' NOSUPERUSER NOCREATEDB NOCREATEROLE;
CREATE DATABASE litellm OWNER auriga_litellm;
REVOKE ALL ON DATABASE litellm FROM PUBLIC;
GRANT CONNECT ON DATABASE litellm TO auriga_litellm;
SQL
)
