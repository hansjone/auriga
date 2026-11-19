#!/bin/sh
# Approved source installation; start.sh owns live replay preparation/startup.
set -eu
. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"
load_stack_env
_shared_profile=; _shared_profile_sha=; _shared_model=; _shared_base_sha=
_shared_adapter=; _shared_adapter_sha=; _shared_team=false
_shared_dedicated=; _shared_dedicated_sha=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --team-only) [ "$_shared_team" = false ] || die 'duplicate team-only'; _shared_team=true; shift ;;
    --profile-file|--profile-sha256|--dedicated-profile-file|--dedicated-profile-sha256|--model|--expected-base-sha256|--adapter-file|--adapter-sha256)
      [ "$#" -ge 2 ] && [ -n "$2" ] || die 'shared installation flag requires a value'
      case "$1" in
        --profile-file) [ -z "$_shared_profile" ] || die 'duplicate profile'; _shared_profile=$2 ;;
        --profile-sha256) [ -z "$_shared_profile_sha" ] || die 'duplicate profile hash'; _shared_profile_sha=$2 ;;
        --dedicated-profile-file) [ -z "$_shared_dedicated" ] || die 'duplicate dedicated profile'; _shared_dedicated=$2 ;;
        --dedicated-profile-sha256) [ -z "$_shared_dedicated_sha" ] || die 'duplicate dedicated profile hash'; _shared_dedicated_sha=$2 ;;
        --model) [ -z "$_shared_model" ] || die 'duplicate model'; _shared_model=$2 ;;
        --expected-base-sha256) [ -z "$_shared_base_sha" ] || die 'duplicate base hash'; _shared_base_sha=$2 ;;
        --adapter-file) [ -z "$_shared_adapter" ] || die 'duplicate adapter'; _shared_adapter=$2 ;;
        --adapter-sha256) [ -z "$_shared_adapter_sha" ] || die 'duplicate adapter hash'; _shared_adapter_sha=$2 ;;
      esac
      shift 2 ;;
    *) die 'Usage: shared-install.sh --profile-file /approved/profile.yml --profile-sha256 SHA --model ID --expected-base-sha256 SHA [--dedicated-profile-file /approved/dedicated.yml --dedicated-profile-sha256 SHA] [--team-only | --adapter-file /approved/adapter.json --adapter-sha256 SHA]' ;;
  esac
done
[ -n "$_shared_profile" ] && [ -n "$_shared_profile_sha" ] && [ -n "$_shared_model" ] && [ -n "$_shared_base_sha" ] || die 'complete independently approved profile/model/base inputs required'
if [ -n "$_shared_dedicated$_shared_dedicated_sha" ]; then
  [ -n "$_shared_dedicated" ] && [ -n "$_shared_dedicated_sha" ] || die 'dedicated profile and independent SHA256 must be supplied together'
fi
case "$_shared_team" in
  true) [ -z "$_shared_adapter$_shared_adapter_sha" ] || die 'team-only and adapter inputs are mutually exclusive' ;;
  false) [ -n "$_shared_adapter" ] && [ -n "$_shared_adapter_sha" ] || die 'approve the existing company adapter or explicitly select new team-only installation' ;;
esac
if [ "${AURIGA_INSTALL_MODE:-host}" = container ]; then
  sh "$NAS_ROOT/scripts/installer.sh" shared-stage "$_shared_base_sha" "$_shared_profile" "$_shared_profile_sha" "$_shared_model" "$_shared_adapter" "$_shared_adapter_sha" "$_shared_team" "$_shared_dedicated" "$_shared_dedicated_sha"
  sh "$NAS_ROOT/scripts/installer.sh" shared-commit "$_shared_base_sha"
else
  set -- -base "$AURIGA_DATA_ROOT/config/materials" -out "$AURIGA_DATA_ROOT/config/shared-source" \
    -action stage -expected-base-sha256 "$_shared_base_sha" -profile-file "$_shared_profile" -profile-sha256 "$_shared_profile_sha" -model "$_shared_model"
  if [ "$_shared_team" = true ]; then set -- "$@" -team-only
  else set -- "$@" -adapter-file "$_shared_adapter" -adapter-sha256 "$_shared_adapter_sha"; fi
  if [ -n "$_shared_dedicated" ]; then set -- "$@" -dedicated-profile-file "$_shared_dedicated" -dedicated-profile-sha256 "$_shared_dedicated_sha"; fi
  "$(bootstrap_binary)" shared-runtime "$@"
  "$(bootstrap_binary)" shared-runtime -base "$AURIGA_DATA_ROOT/config/materials" -out "$AURIGA_DATA_ROOT/config/shared-source" -action commit -expected-base-sha256 "$_shared_base_sha"
fi
sh "$NAS_ROOT/scripts/render.sh" --core-only
info 'shared source committed and checked role files rendered; run start.sh to prepare replay storage, confirm installer revocation, and start the shared services'
