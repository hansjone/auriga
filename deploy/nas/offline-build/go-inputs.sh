#!/bin/sh
set -eu

if [ "$#" -lt 2 ] || [ "$#" -gt 3 ]; then
    echo "usage: $0 prepare|build|locks|deps ABSOLUTE_EMPTY_OUTPUT_DIRECTORY [PROGRAM]" >&2
    exit 2
fi
mode=$1
out=$2
program=${3:-all}
case "$out" in
    /*) ;;
    *) echo "use an absolute output directory" >&2; exit 2 ;;
esac
case "$mode" in
    prepare)
        if [ "$program" != all ]; then
            echo "prepare records all NAS Go programs" >&2
            exit 2
        fi
        ;;
    build)
        case "$program" in
            all|auriga-edge|auriga-ai-gateway|auriga-network-broker|bifrost|auriga-egress|auriga-agent|runtime-release|auriga-network-probe|auriga-panel|auriga-nas-bootstrap) ;;
            *) echo "unknown NAS Go program: $program" >&2; exit 2 ;;
        esac
        ;;
    locks|deps)
        if [ "$program" != all ]; then
            echo "$mode takes no program" >&2
            exit 2
        fi
        ;;
    *) echo "choose prepare, build, locks or deps" >&2; exit 2 ;;
esac
mkdir -p "$out"
if [ -n "$(ls -A "$out")" ]; then
    echo "output directory is not empty" >&2
    exit 2
fi
src=$(CDPATH='' cd -- "$(dirname -- "$0")/../../.." && pwd)
scratch=$(mktemp -d)
trap 'rm -rf -- "$scratch"' EXIT
export GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOENV=off GOTOOLCHAIN=local
export GOMODCACHE="$scratch/modules" GOCACHE="$scratch/build"
export GOFLAGS=-mod=readonly
if [ "$(go version)" != 'go version go1.27.3 linux/amd64' ]; then
    echo "Go 1.27.3 linux/amd64 is required" >&2
    exit 2
fi

snapshot() (
    cd "$src"
    find infra deploy/nas/bootstrap deploy/bootstrap third_party/bifrost/core \
        -type f \( -name go.mod -o -name go.sum -o -name go.work -o -name go.work.sum \) \
        -print | LC_ALL=C sort | xargs sha256sum
)

# locks: print the source-lock inventory a prepare run would record, nothing else.
# Lets callers decide offline whether the dependency inputs are still current.
if [ "$mode" = locks ]; then
    snapshot >"$out/source-locks.sha256"
    exit 0
fi

record_run() (
    directory=$1
    workspace=$2
    output=$3
    shift 3
    if [ "$program" != all ] && [ "$program" != "$output" ]; then
        exit 0
    fi
    cd "$src/$directory"
    export GOWORK="$workspace"
    if [ "$mode" = prepare ]; then
        go list -deps "$@" >/dev/null
    elif [ "$mode" = deps ]; then
        # Input fingerprint (site deploy D6): every package directory the program
        # compiles, offline against the verified dependency inputs. Embedded files
        # always live under their package directory, which the CLI hashes whole.
        go list -deps -f "$output {{.Dir}}" "$@" >>"$out/deps.txt"
    else
        go build -trimpath -o "$out/$output" "$@"
    fi
)

if [ "$mode" = prepare ]; then
    export GOPRIVATE= GONOPROXY=none GONOSUMDB=none GOINSECURE=none
    # AURIGA_GO_PREPARE_PROXY lets a site use a mirror; the checksum database
    # (GOSUMDB) is never relaxed and go.sum still decides what is accepted.
    upstream_proxy=${AURIGA_GO_PREPARE_PROXY:-https://proxy.golang.org}
    export GOPROXY="$upstream_proxy" GOSUMDB=sum.golang.org
    if [ -n "${AURIGA_GO_PREPARE_CACHE:-}" ]; then
        prepare_cache=$(CDPATH='' cd -- "$AURIGA_GO_PREPARE_CACHE" && pwd)
        GOPROXY="file://$prepare_cache,$upstream_proxy"
    fi
else
    case "${AURIGA_GO_PROXY_DIR:-}" in
        /*) ;;
        *) echo "set AURIGA_GO_PROXY_DIR to the absolute path of the extracted dependency directory" >&2; exit 2 ;;
    esac
    inputs=$(CDPATH='' cd -- "$AURIGA_GO_PROXY_DIR" && pwd)
    (cd "$inputs" && sha256sum --quiet -c SHA256SUMS)
    export GOPRIVATE= GONOPROXY=none GONOSUMDB=none GOINSECURE=none
    export GOPROXY="file://$inputs/download" GOSUMDB=off
fi
snapshot >"$scratch/source-locks"
if [ "$mode" != prepare ]; then
    cmp "$scratch/source-locks" "$inputs/source-locks.sha256"
fi

work="$src/infra/go.work"
record_run infra/edge "$work" auriga-edge .
record_run infra/ai-gateway "$work" auriga-ai-gateway .
record_run infra/ai-gateway "$work" auriga-network-broker github.com/nats-io/nats-server/v2
record_run infra/ai-gateway "$work" bifrost ./cmd/relay-backend
record_run infra/egress "$work" auriga-egress .
record_run infra/agent "$work" auriga-agent .
record_run infra "$work" runtime-release ./agent/cmd/runtime-release
record_run deploy/bootstrap off auriga-network-probe ./cmd/network-probe
record_run deploy/bootstrap off auriga-panel ./cmd/panel
record_run deploy/nas/bootstrap off auriga-nas-bootstrap .

snapshot >"$scratch/final-locks"
cmp "$scratch/source-locks" "$scratch/final-locks"
if [ "$mode" = prepare ]; then
    # Keep exact proxy responses for the modules actually resolved. Exclude
    # cache locks, package lists, and the checksum database's own cache.
    vendor="$out/download"
    mkdir "$vendor"
    (cd "$GOMODCACHE/cache/download" &&
        find . -path ./sumdb -prune -o -type f \
            \( -name '*.info' -o -name '*.mod' -o -name '*.zip' \) -print0 |
        tar -cf - --null -T -) | tar -xf - -C "$vendor"
    cp -- "$scratch/source-locks" "$out/source-locks.sha256"
    (cd "$out" &&
        find download -type f -print | LC_ALL=C sort | xargs sha256sum > SHA256SUMS &&
        sha256sum source-locks.sha256 >> SHA256SUMS)
elif [ "$mode" = build ]; then
    if [ "$program" = all ]; then
        (cd "$out" && sha256sum auriga-* bifrost runtime-release > SHA256SUMS)
    else
        (cd "$out" && sha256sum "$program" > SHA256SUMS)
    fi
fi
