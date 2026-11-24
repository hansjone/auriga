#!/bin/sh
# Approved source installation; start.sh owns live replay preparation/startup.
# Explicit rotations:
#   --set-model ID --expected-base-sha256 SHA
#   --set-profile FILE --profile-sha256 SHA --expected-base-sha256 SHA
set -eu
. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"
load_stack_env
_shared_profile=; _shared_profile_sha=; _shared_model=; _shared_base_sha=
_shared_adapter=; _shared_adapter_sha=; _shared_team=false
_shared_dedicated=; _shared_dedicated_sha=
_shared_backend=; _shared_quota_mount=; _shared_quota_binary=
_shared_project_first=; _shared_project_last=
_shared_set_model=
_shared_set_profile=false
_usage='Usage: shared-install.sh --profile-file /approved/profile.yml --profile-sha256 SHA --model ID --expected-base-sha256 SHA [--dedicated-profile-file /approved/dedicated.yml --dedicated-profile-sha256 SHA] [--backend ext4-image|xfs --quota-mount /path [--quota-binary /usr/sbin/xfs_quota] [--project-first N --project-last N]] [--team-only | --adapter-file /approved/adapter.json --adapter-sha256 SHA]
   or: shared-install.sh --set-model ID --expected-base-sha256 SHA
   or: shared-install.sh --set-profile /approved/profile.yml --profile-sha256 SHA --expected-base-sha256 SHA'
while [ "$#" -gt 0 ]; do
  case "$1" in
    --team-only) [ "$_shared_team" = false ] || die 'duplicate team-only'; _shared_team=true; shift ;;
    --set-model|--set-profile|--profile-file|--profile-sha256|--dedicated-profile-file|--dedicated-profile-sha256|--model|--expected-base-sha256|--adapter-file|--adapter-sha256|--backend|--quota-mount|--quota-binary|--project-first|--project-last)
      [ "$#" -ge 2 ] && [ -n "$2" ] || die 'shared installation flag requires a value'
      case "$1" in
        --set-model) [ -z "$_shared_set_model" ] || die 'duplicate set-model'; _shared_set_model=$2 ;;
        --set-profile)
          [ "$_shared_set_profile" = false ] || die 'duplicate set-profile'
          [ -z "$_shared_profile" ] || die 'duplicate profile'
          _shared_set_profile=true
          _shared_profile=$2 ;;
        --profile-file) [ -z "$_shared_profile" ] || die 'duplicate profile'; _shared_profile=$2 ;;
        --profile-sha256) [ -z "$_shared_profile_sha" ] || die 'duplicate profile hash'; _shared_profile_sha=$2 ;;
        --dedicated-profile-file) [ -z "$_shared_dedicated" ] || die 'duplicate dedicated profile'; _shared_dedicated=$2 ;;
        --dedicated-profile-sha256) [ -z "$_shared_dedicated_sha" ] || die 'duplicate dedicated profile hash'; _shared_dedicated_sha=$2 ;;
        --model) [ -z "$_shared_model" ] || die 'duplicate model'; _shared_model=$2 ;;
        --expected-base-sha256) [ -z "$_shared_base_sha" ] || die 'duplicate base hash'; _shared_base_sha=$2 ;;
        --adapter-file) [ -z "$_shared_adapter" ] || die 'duplicate adapter'; _shared_adapter=$2 ;;
        --adapter-sha256) [ -z "$_shared_adapter_sha" ] || die 'duplicate adapter hash'; _shared_adapter_sha=$2 ;;
        --backend) [ -z "$_shared_backend" ] || die 'duplicate backend'; _shared_backend=$2 ;;
        --quota-mount) [ -z "$_shared_quota_mount" ] || die 'duplicate quota-mount'; _shared_quota_mount=$2 ;;
        --quota-binary) [ -z "$_shared_quota_binary" ] || die 'duplicate quota-binary'; _shared_quota_binary=$2 ;;
        --project-first) [ -z "$_shared_project_first" ] || die 'duplicate project-first'; _shared_project_first=$2 ;;
        --project-last) [ -z "$_shared_project_last" ] || die 'duplicate project-last'; _shared_project_last=$2 ;;
      esac
      shift 2 ;;
    *) die "$_usage" ;;
  esac
done
if [ -n "$_shared_set_model" ] && [ "$_shared_set_profile" = true ]; then
  die 'set-model and set-profile are mutually exclusive in one invocation; run them sequentially'
fi
_shared_replay_rebind() {
  # Model/profile rotation keeps the installation identity; move the replay
  # installer receipts onto it (keys, bucket and activation state unchanged).
  if [ "${AURIGA_INSTALL_MODE:-host}" = container ]; then
    sh "$NAS_ROOT/scripts/installer.sh" shared-replay-rebind
  else
    "$(bootstrap_binary)" shared-replay-prepare -base "$AURIGA_DATA_ROOT/config/materials" -action rebind
  fi
}
if [ -n "$_shared_set_model" ]; then
  [ -n "$_shared_base_sha" ] || die 'set-model requires --expected-base-sha256'
  [ -z "$_shared_profile$_shared_profile_sha$_shared_model$_shared_adapter$_shared_adapter_sha$_shared_dedicated$_shared_dedicated_sha$_shared_backend$_shared_quota_mount$_shared_quota_binary$_shared_project_first$_shared_project_last" ] && [ "$_shared_team" = false ] \
    || die 'set-model only accepts --set-model and --expected-base-sha256'
  if [ "${AURIGA_INSTALL_MODE:-host}" = container ]; then
    sh "$NAS_ROOT/scripts/installer.sh" shared-set-model "$_shared_base_sha" "$_shared_set_model"
    sh "$NAS_ROOT/scripts/installer.sh" shared-commit "$_shared_base_sha"
  else
    "$(bootstrap_binary)" shared-runtime -base "$AURIGA_DATA_ROOT/config/materials" -out "$AURIGA_DATA_ROOT/config/shared-source" \
      -action set-model -expected-base-sha256 "$_shared_base_sha" -model "$_shared_set_model"
    "$(bootstrap_binary)" shared-runtime -base "$AURIGA_DATA_ROOT/config/materials" -out "$AURIGA_DATA_ROOT/config/shared-source" \
      -action commit -expected-base-sha256 "$_shared_base_sha"
  fi
  _shared_replay_rebind
  sh "$NAS_ROOT/scripts/render.sh" --core-only
  info 'shared model committed and role files re-rendered; run start.sh so Agent reloads host.json (clear shared pool only if members still bind the old model)'
  exit 0
fi
if [ "$_shared_set_profile" = true ]; then
  [ -n "$_shared_profile" ] && [ -n "$_shared_profile_sha" ] && [ -n "$_shared_base_sha" ] || die 'set-profile requires --set-profile FILE --profile-sha256 SHA --expected-base-sha256 SHA'
  [ -z "$_shared_model$_shared_adapter$_shared_adapter_sha$_shared_dedicated$_shared_dedicated_sha$_shared_backend$_shared_quota_mount$_shared_quota_binary$_shared_project_first$_shared_project_last" ] && [ "$_shared_team" = false ] \
    || die 'set-profile only accepts profile file, profile hash and base approval'
  if [ "${AURIGA_INSTALL_MODE:-host}" = container ]; then
    sh "$NAS_ROOT/scripts/installer.sh" shared-set-profile "$_shared_base_sha" "$_shared_profile" "$_shared_profile_sha"
    sh "$NAS_ROOT/scripts/installer.sh" shared-commit "$_shared_base_sha"
  else
    "$(bootstrap_binary)" shared-runtime -base "$AURIGA_DATA_ROOT/config/materials" -out "$AURIGA_DATA_ROOT/config/shared-source" \
      -action set-profile -expected-base-sha256 "$_shared_base_sha" -profile-file "$_shared_profile" -profile-sha256 "$_shared_profile_sha"
    "$(bootstrap_binary)" shared-runtime -base "$AURIGA_DATA_ROOT/config/materials" -out "$AURIGA_DATA_ROOT/config/shared-source" \
      -action commit -expected-base-sha256 "$_shared_base_sha"
  fi
  _shared_replay_rebind
  sh "$NAS_ROOT/scripts/render.sh" --core-only
  info 'shared profile committed and role files re-rendered; run start.sh so Agent reloads the shared profile'
  exit 0
fi
[ -n "$_shared_profile" ] && [ -n "$_shared_profile_sha" ] && [ -n "$_shared_model" ] && [ -n "$_shared_base_sha" ] || die 'complete independently approved profile/model/base inputs required'
if [ -n "$_shared_dedicated$_shared_dedicated_sha" ]; then
  [ -n "$_shared_dedicated" ] && [ -n "$_shared_dedicated_sha" ] || die 'dedicated profile and independent SHA256 must be supplied together'
fi
case "$_shared_team" in
  true) [ -z "$_shared_adapter$_shared_adapter_sha" ] || die 'team-only and adapter inputs are mutually exclusive' ;;
  false) [ -n "$_shared_adapter" ] && [ -n "$_shared_adapter_sha" ] || die 'approve the existing company adapter or explicitly select new team-only installation' ;;
esac
if [ "$_shared_backend" = xfs ] && [ -n "${AURIGA_IMAGE_AGENT:-}" ] && command -v docker >/dev/null 2>&1; then
  if docker image inspect "$AURIGA_IMAGE_AGENT" >/dev/null 2>&1; then
    _xfs_probe_bin=${_shared_quota_binary:-/usr/sbin/xfs_quota}
    docker run --rm --pull never --network none --entrypoint sh "$AURIGA_IMAGE_AGENT" -c 'test -x "$1"' -- "$_xfs_probe_bin" \
      || die "agent image ($AURIGA_IMAGE_AGENT) is missing executable $_xfs_probe_bin required for --backend xfs; rebuild or upgrade agent image with xfsprogs"
  fi
fi
if [ "${AURIGA_INSTALL_MODE:-host}" = container ]; then
  NAS_SHARED_BACKEND="$_shared_backend" \
  NAS_SHARED_QUOTA_MOUNT="$_shared_quota_mount" \
  NAS_SHARED_QUOTA_BINARY="$_shared_quota_binary" \
  NAS_SHARED_PROJECT_FIRST="$_shared_project_first" \
  NAS_SHARED_PROJECT_LAST="$_shared_project_last" \
    sh "$NAS_ROOT/scripts/installer.sh" shared-stage "$_shared_base_sha" "$_shared_profile" "$_shared_profile_sha" "$_shared_model" "$_shared_adapter" "$_shared_adapter_sha" "$_shared_team" "$_shared_dedicated" "$_shared_dedicated_sha"
  sh "$NAS_ROOT/scripts/installer.sh" shared-commit "$_shared_base_sha"
else
  set -- -base "$AURIGA_DATA_ROOT/config/materials" -out "$AURIGA_DATA_ROOT/config/shared-source" \
    -action stage -expected-base-sha256 "$_shared_base_sha" -profile-file "$_shared_profile" -profile-sha256 "$_shared_profile_sha" -model "$_shared_model"
  if [ "$_shared_team" = true ]; then set -- "$@" -team-only
  else set -- "$@" -adapter-file "$_shared_adapter" -adapter-sha256 "$_shared_adapter_sha"; fi
  if [ -n "$_shared_dedicated" ]; then set -- "$@" -dedicated-profile-file "$_shared_dedicated" -dedicated-profile-sha256 "$_shared_dedicated_sha"; fi
  if [ -n "$_shared_backend" ]; then set -- "$@" -backend "$_shared_backend"; fi
  if [ -n "$_shared_quota_mount" ]; then set -- "$@" -quota-mount "$_shared_quota_mount"; fi
  if [ -n "$_shared_quota_binary" ]; then set -- "$@" -quota-binary "$_shared_quota_binary"; fi
  if [ -n "$_shared_project_first" ]; then set -- "$@" -project-first "$_shared_project_first"; fi
  if [ -n "$_shared_project_last" ]; then set -- "$@" -project-last "$_shared_project_last"; fi
  "$(bootstrap_binary)" shared-runtime "$@"
  "$(bootstrap_binary)" shared-runtime -base "$AURIGA_DATA_ROOT/config/materials" -out "$AURIGA_DATA_ROOT/config/shared-source" -action commit -expected-base-sha256 "$_shared_base_sha"
fi
sh "$NAS_ROOT/scripts/render.sh" --core-only
info 'shared source committed and checked role files rendered; run start.sh to prepare replay storage, confirm installer revocation, and start the shared services'
