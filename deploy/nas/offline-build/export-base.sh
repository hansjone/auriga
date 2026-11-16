#!/usr/bin/env bash
set -euo pipefail

if [[ $# != 2 ]]; then
    echo "usage: $0 CANONICAL_LOCAL_IMAGE_TAG ABSOLUTE_ARCHIVE_PATH" >&2
    exit 2
fi
tag=$1
archive=$2
case "$tag" in
    golang:1.27.3|node:22-bookworm|eclipse-temurin:25-jdk|eclipse-temurin:25-jre|debian:bookworm-slim|alpine:3.22|gcr.io/distroless/static-debian12:nonroot|python:3.13.8-slim-bookworm) ;;
    *) echo "select a base used by the NAS recipes" >&2; exit 2 ;;
esac
case "$archive" in
    /*.tar.gz) ;;
    *) echo "archive must be an absolute .tar.gz path" >&2; exit 2 ;;
esac
if [[ -e "$archive" || -e "$archive.json" || -e "$archive.sha256" ]]; then
    echo "archive or metadata already exists" >&2
    exit 2
fi
if [[ "$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$tag")" != linux/amd64 ]]; then
    echo "the loaded base must be Linux amd64" >&2
    exit 2
fi
metadata_format='{"platform":"{{.Os}}/{{.Architecture}}","engineId":{{json .Id}},"repoDigests":{{json .RepoDigests}}}'
metadata=$(docker image inspect --format "$metadata_format" "$tag")
partial=$(mktemp "$archive.partial.XXXXXX")
trap 'rm -f -- "$partial"' EXIT
docker image save "$tag" | gzip -1 >"$partial"
test "$metadata" = "$(docker image inspect --format "$metadata_format" "$tag")"
mv -- "$partial" "$archive"
printf '%s\n' "$metadata" >"$archive.json"
(cd "$(dirname -- "$archive")" && sha256sum "$(basename -- "$archive")") >"$archive.sha256"
