#!/bin/sh
set -eu

if [ "$#" -ne 4 ]; then
    echo "usage: $0 SERVICE IMAGE_TAG LOCAL_BASE_IMAGE ABSOLUTE_ARTIFACT_DIRECTORY" >&2
    exit 2
fi
service=$1
tag=$2
base=$3
artifacts=$4
case "$artifacts" in
    /*) ;;
    *) echo "use an absolute artifact directory" >&2; exit 2 ;;
esac
src=$(CDPATH='' cd -- "$(dirname -- "$0")/../../.." && pwd)
recipes="$src/deploy/nas/offline-build"
scratch=$(mktemp -d)
trap 'rm -rf -- "$scratch"' EXIT

check_hashes() (
    cd "$1"
    sha256sum --quiet -c SHA256SUMS
)
stage_go() {
    check_hashes "$artifacts/go"
    mkdir "$scratch/go"
    for program in "$@"; do
        cp -- "$artifacts/go/$program" "$scratch/go/"
    done
}
stage_system() {
    check_hashes "$artifacts/system/$service"
    cp -a -- "$artifacts/system/$service" "$scratch/system"
}

(cd "$src" && sha256sum --quiet -c "$recipes/upstream-recipes.sha256")
if [ "$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$base")" != linux/amd64 ]; then
    echo "load the approved linux/amd64 base first" >&2
    exit 2
fi
case "$service" in
    agent)
        build_arg=AGENT_BASE
        stage_go auriga-agent
        stage_system
        cp -- "$src/deploy/nas/docker/agent-entrypoint.sh" "$scratch/"
        ;;
    bifrost)
        build_arg=BIFROST_BASE
        stage_go bifrost
        stage_system
        cp -- "$src/third_party/bifrost/LICENSE" "$src/third_party/bifrost/THIRD_PARTY_NOTICES.md" \
            "$src/deploy/nas/bifrost/config.json" "$scratch/"
        ;;
    edge)
        build_arg=EDGE_BASE
        stage_go auriga-edge auriga-ai-gateway auriga-egress auriga-network-broker auriga-network-probe auriga-panel
        check_hashes "$artifacts/web"
        mkdir "$scratch/web"
        cp -a -- "$artifacts/web/panel" "$artifacts/web/portal" "$artifacts/web/admin" "$scratch/web/"
        mkdir -p "$scratch/directories/policies" "$scratch/directories/reports"
        ;;
    platform)
        build_arg=PLATFORM_BASE
        check_hashes "$artifacts/java"
        mkdir "$scratch/java"
        cp -- "$artifacts/java/platform.jar" "$scratch/java/"
        stage_system
        ;;
    operator)
        build_arg=OPERATOR_BASE
        stage_system
        (cd "$src" && sha256sum --quiet -c "$scratch/system/source-locks.sha256")
        cp -- "$src/deploy/nas/test/admin-bootstrap-requirements.txt" "$scratch/requirements.txt"
        ;;
    *) echo "unknown NAS service" >&2; exit 2 ;;
esac
DOCKER_BUILDKIT=1 docker build --platform=linux/amd64 --network=none --pull=false --no-cache \
    --provenance=false --sbom=false \
    --target "$service" --build-arg "$build_arg=$base" -f "$recipes/Dockerfile.assembly" \
    -t "$tag" "$scratch"
