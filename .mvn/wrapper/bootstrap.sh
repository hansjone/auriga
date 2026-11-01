#!/bin/sh
# Run from the repository root. Retry tool acquisition, never a build or test.
# A failed attempt can leave a truncated distribution behind; the wrapper would
# reuse it, so each retry first drops that one distribution. Checksums still apply.
set -eu
url=$(sed -n 's/^distributionUrl=//p' .mvn/wrapper/maven-wrapper.properties)
name=$(basename "$url" .zip)
dists="${MAVEN_USER_HOME:-$HOME/.m2}/wrapper/dists/$name"
attempt=1
until ./mvnw -B --version; do
  if [ "$attempt" -ge 3 ]; then
    echo "Maven bootstrap failed after $attempt attempts" >&2
    exit 1
  fi
  echo "Maven bootstrap attempt $attempt failed; retrying" >&2
  case "$name" in apache-maven-*) rm -rf -- "$dists" ;; esac
  sleep "$((attempt * 2))"
  attempt=$((attempt + 1))
done
