#!/bin/sh
set -eu
# Read files inside the container. No secret value enters compose interpolation.
LITELLM_MASTER_KEY=$(cat /run/secrets/litellm_master)
LITELLM_SALT_KEY=$(cat /run/secrets/litellm_salt)
pg_password=$(cat /run/secrets/litellm_pg_password)
DATABASE_URL="postgresql://auriga_litellm:$pg_password@postgres:5432/litellm"
export LITELLM_MASTER_KEY LITELLM_SALT_KEY DATABASE_URL
unset pg_password
exec /app/docker/prod_entrypoint.sh --config /etc/litellm/config.yaml --host 0.0.0.0 --port 4000
