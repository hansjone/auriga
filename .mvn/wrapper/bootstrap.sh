#!/bin/sh
# Run from the repository root. Retry tool acquisition, never a build or test.
set -eu
attempt=1
until ./mvnw -B --version; do
  if [ "$attempt" -ge 3 ]; then
    echo "Maven bootstrap failed after $attempt attempts" >&2
    exit 1
  fi
  echo "Maven bootstrap attempt $attempt failed; retrying" >&2
  sleep "$((attempt * 2))"
  attempt=$((attempt + 1))
done
