#!/bin/sh
# Exercise the public NAS entry points with an isolated recipe and Docker fixture.
set -eu
root=$(CDPATH= cd -P -- "$(dirname -- "$0")/.." && pwd)
temp=$(mktemp -d)
trap 'rm -rf -- "$temp"' EXIT INT TERM
export NAS_ROOT="$temp/package" AURIGA_DATA_ROOT="$temp/install/auriga-fixture"
export FIXTURE_LOG="$temp/actions"
mkdir -p "$NAS_ROOT/scripts" "$NAS_ROOT/bin" "$temp/bin" "$temp/bundle" "$AURIGA_DATA_ROOT/config/materials" \
  "$AURIGA_DATA_ROOT/config/runtime-source/s-fixture" "$AURIGA_DATA_ROOT/data/agent/agent" "$AURIGA_DATA_ROOT/secrets"
cp "$root/"*compose.yml "$NAS_ROOT/"
for script in lib oneclick-compose start runtime-upgrade installer mount-access mount-access-entry; do
  cp "$root/scripts/$script.sh" "$NAS_ROOT/scripts/"
done
# Rendering, host preflight and emergency delivery are outside the Compose fixture.
for script in preflight network-preflight render render-collab emergency; do
  printf '#!/bin/sh\nexit 0\n' >"$NAS_ROOT/scripts/$script.sh"
done
touch "$AURIGA_DATA_ROOT/.auriga-initialized" "$temp/access.json"
printf '%s\n' '{"schemaVersion":1,"origin":"https://resources.example.com","keys":[]}' >"$temp/trust.json"
cp "$temp/trust.json" "$AURIGA_DATA_ROOT/data/agent/agent/resource-trust.json"
for leaf in platform edge agent audit-db bifrost-audit; do
  : >"$AURIGA_DATA_ROOT/config/$leaf.env"
done
cat >"$NAS_ROOT/bin/auriga-nas-bootstrap" <<'EOF'
#!/bin/sh
set -eu
[ "$1" = runtime-upgrade ] && [ "$2" = -action ]
case "$3" in
  path) cat "$AURIGA_DATA_ROOT/runtime.path" ;;
  stage)
    printf '%s\n' "$AURIGA_DATA_ROOT/config/runtime-source/s-fixture" >"$AURIGA_DATA_ROOT/runtime.path"
    echo stage >>"$FIXTURE_LOG" ;;
  commit) echo commit >>"$FIXTURE_LOG" ;;
  *) exit 91 ;;
esac
EOF
chmod 0755 "$NAS_ROOT/bin/auriga-nas-bootstrap"
cat >"$temp/bin/docker" <<'EOF'
#!/bin/sh
set -eu
case "$1 ${2:-}" in
  'compose version') exit 0 ;;
  'image inspect')
    case "$4" in *Config.User*) echo 0 ;; *) echo linux/amd64 ;; esac
    exit 0 ;;
  'run '*)
    case " $* " in
      *' export-env '*)
        tar -C "$AURIGA_DATA_ROOT/config" -cf - platform.env edge.env agent.env audit-db.env bifrost-audit.env \
          -C "$AURIGA_DATA_ROOT" runtime.path bifrost.enabled collab.enabled managed.identity.enabled \
          collab.rotation.path collab.rotation.available ;;
      *' runtime-path '*|*' runtime-stage '*|*' runtime-commit '*)
        for operation; do :; done
        "$NAS_ROOT/bin/auriga-nas-bootstrap" runtime-upgrade -action "${operation#runtime-}" ;;
      *' prepare-socket '*) ;;
      *' /probe-entry.sh platform '*)
        case " $* " in
          *" type=bind,src=$AURIGA_DATA_ROOT/secrets/resource-downgrade,dst=/probe/resource-downgrade,readonly "*)
            [ -f "$AURIGA_DATA_ROOT/secrets/resource-downgrade/downgrade.pk8" ]
            echo probe-key >>"$FIXTURE_LOG" ;;
          *) echo probe-default >>"$FIXTURE_LOG" ;;
        esac ;;
      *' /probe-entry.sh '*) ;;
      *) exit 92 ;;
    esac ;;
  'ps '*) ;;
  'compose -f')
    model=default
    runtime=base
    oneclick=no
    shift
    while :; do
      case "$1" in
        -p|--env-file) shift 2 ;;
        -f)
          case "$2" in
            */resource-downgrade-compose.yml)
              [ "$AURIGA_RESOURCE_DOWNGRADE_KID" = test-only-downgrade ]
              model=enabled ;;
            */runtime.yml)
              grep -Fq "$AURIGA_DATA_ROOT/config/runtime-source/s-fixture/templates:/etc/auriga/templates:ro" "$2"
              runtime=updated ;;
            */oneclick-compose.yml) oneclick=yes ;;
          esac
          shift 2 ;;
        *) break ;;
      esac
    done
    printf '%s %s %s %s\n' "$model" "$runtime" "$oneclick" "$*" >>"$FIXTURE_LOG" ;;
  *) exit 93 ;;
esac
EOF
chmod 0755 "$temp/bin/docker"
export PATH="$temp/bin:$PATH"
unset NAS_INSTALLER_ACTIVE AURIGA_NAS_BOOTSTRAP_BINARY
export AURIGA_RESOURCE_DOWNGRADE_KID=inherited-fixture-kid
for leaf in bifrost.enabled collab.enabled managed.identity.enabled; do
  printf '0\n' >"$AURIGA_DATA_ROOT/$leaf"
done
: >"$AURIGA_DATA_ROOT/collab.rotation.path"
printf '1\n' >"$AURIGA_DATA_ROOT/collab.rotation.available"
for mode in host container; do
  if [ "$mode" = host ]; then
    export NAS_ALLOW_NONROOT=1
    oneclick=false
    expected=no
  else
    unset NAS_ALLOW_NONROOT
    oneclick=true
    expected=yes
  fi
  stack="$AURIGA_DATA_ROOT/stack.env"
  cat >"$stack" <<EOF
AURIGA_DATA_ROOT=$AURIGA_DATA_ROOT
AURIGA_ACCESS_FILE=$temp/access.json
AURIGA_INSTALL_MODE=$mode
AURIGA_INSTALLER_IMAGE=sha256:$(printf '%064d' 1)
AURIGA_IMAGE_PLATFORM=sha256:$(printf '%064d' 2)
AURIGA_IMAGE_REGISTRY=sha256:$(printf '%064d' 3)
AURIGA_HOST_PUBLISH_PLATFORM_HTTP=28081
AURIGA_ONECLICK_PUBLISH=$oneclick
EOF
  chmod 0600 "$stack"
  printf '%s\n' "$AURIGA_DATA_ROOT/config/materials" >"$AURIGA_DATA_ROOT/runtime.path"
  rm -f "$AURIGA_DATA_ROOT/secrets/resource-downgrade/downgrade.pk8"
  : >"$FIXTURE_LOG"
  sh "$NAS_ROOT/scripts/oneclick-compose.sh" config --quiet
  sh "$NAS_ROOT/scripts/start.sh"
  sh "$NAS_ROOT/scripts/mount-access.sh"
  grep -Fxq "default base $expected config --quiet" "$FIXTURE_LOG"
  grep -Fxq "default base $expected up -d --no-build --pull never" "$FIXTURE_LOG"
  grep -Fxq probe-default "$FIXTURE_LOG"
  candidate="registry.example.com/dsh@sha256:$(printf '%064d' 4)"
  sh "$NAS_ROOT/scripts/runtime-upgrade.sh" "$temp/bundle" "$candidate"
  grep -Fxq "default updated $expected up -d --wait --wait-timeout 120 --no-build --pull never platform agent" "$FIXTURE_LOG"
  printf '%s\n' "$AURIGA_DATA_ROOT/config/materials" >"$AURIGA_DATA_ROOT/runtime.path"
  : >"$FIXTURE_LOG"
  printf 'AURIGA_RESOURCE_DOWNGRADE_KID=test-only-downgrade\n' >>"$stack"
  if sh "$NAS_ROOT/scripts/mount-access.sh" >"$temp/missing.log" 2>&1; then exit 1; fi
  grep -q 'mount-access FAILED service=platform' "$temp/missing.log"
  mkdir -p "$AURIGA_DATA_ROOT/secrets/resource-downgrade"
  printf 'fixture-only\n' >"$AURIGA_DATA_ROOT/secrets/resource-downgrade/downgrade.pk8"
  sh "$NAS_ROOT/scripts/start.sh"
  sh "$NAS_ROOT/scripts/mount-access.sh"
  sh "$NAS_ROOT/scripts/runtime-upgrade.sh" "$temp/bundle" "$candidate"
  grep -Fxq "enabled base $expected up -d --no-build --pull never" "$FIXTURE_LOG"
  grep -Fxq probe-key "$FIXTURE_LOG"
  grep -Fxq stage "$FIXTURE_LOG"
  grep -Fxq "enabled updated $expected up -d --wait --wait-timeout 120 --no-build --pull never platform agent" "$FIXTURE_LOG"
  grep -Fxq commit "$FIXTURE_LOG"
  printf 'AURIGA_RESOURCE_DOWNGRADE_KID=\n' >>"$stack"
  sh "$NAS_ROOT/scripts/oneclick-compose.sh" config --quiet
  grep -Fxq "default updated $expected config --quiet" "$FIXTURE_LOG"
  printf 'AURIGA_RESOURCE_DOWNGRADE_KID=invalid kid\n' >>"$stack"
  if sh "$NAS_ROOT/scripts/oneclick-compose.sh" config --quiet >"$temp/invalid.log" 2>&1; then exit 1; fi
  grep -q 'invalid resource downgrade signing kid' "$temp/invalid.log"
  cmp "$temp/trust.json" "$AURIGA_DATA_ROOT/data/agent/agent/resource-trust.json"
  echo "PASS resource Compose projection: $mode, default, start, runtime update and disable"
done
