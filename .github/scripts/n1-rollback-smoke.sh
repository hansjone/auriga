#!/usr/bin/env bash
# P1：被测提交（HEAD）迁移后的库上，分别用父提交与 schemaCompatFloor 提交构建的 platform 镜像
# 做健康冒烟。platform 的 combined 入口会跑主库 common/各服务与 audit 的 Flyway 迁移，旧镜像
# 启动时对“未来迁移”的容忍与对已改旧迁移的拒绝都在这里被真实验证。
# 环境变量：PARENT（必填，40 位提交号）、FLOOR（可空）。工作流传入 HEAD^1（合并队列下即真实父提交）。
# 只起 platform 及其依赖（postgres、nats、audit-db-prepare），不构建整套栈。
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"
PARENT="${PARENT:?需要 PARENT}"
FLOOR="${FLOOR:-}"
for c in "$PARENT" ${FLOOR:+"$FLOOR"}; do
  printf '%s' "$c" | grep -Eqx '[0-9a-f]{40}' || { echo "提交号格式非法：$c" >&2; exit 1; }
done
HEAD_COMMIT="$(git rev-parse HEAD)"
TMP="$(mktemp -d)"
cleanup() {
  for wt in "$TMP"/wt-*; do
    [ -d "$wt" ] && git worktree remove --force "$wt" 2>/dev/null || true
  done
  rm -rf "$TMP"
}
trap cleanup EXIT

dc() {
  docker compose --env-file var/compose.env "$@"
}

wait_platform() {
  local label="$1"
  for _ in $(seq 1 90); do
    if sh var/iam-healthcheck.sh >/dev/null 2>&1; then
      echo "$label：platform 健康"
      return 0
    fi
    sleep 5
  done
  echo "$label：platform 未健康" >&2
  dc ps || true
  dc logs --tail=300 platform || true
  return 1
}

echo "== HEAD（$HEAD_COMMIT）：准备环境并应用本次迁移 =="
cd deploy/compose
chmod +x scripts/*.sh
./scripts/prepare.sh
for name in admin_password member_password encryption_key pg_password pg_migration_password; do
  [ -f "var/bifrost/$name" ] && printf '::add-mask::%s\n' "$(cat "var/bifrost/$name")"
done
dc build platform
dc up -d platform
wait_platform "HEAD"
dc stop platform

smoke_commit() {
  local commit="$1" label="$2"
  if [ "$commit" = "$HEAD_COMMIT" ]; then
    echo "跳过 $label：与 HEAD 相同"
    return 0
  fi
  if ! git cat-file -e "${commit}^{commit}" 2>/dev/null; then
    echo "$label 提交 $commit 不在仓库里（检出须 fetch-depth: 0）" >&2
    return 1
  fi
  echo "== $label（$commit）：已迁移库 + 旧 platform 镜像 =="
  local wt="$TMP/wt-$label"
  git worktree add --detach "$wt" "$commit" >/dev/null
  if [ ! -f "$wt/deploy/compose/Dockerfile.platform" ]; then
    echo "$label 提交没有 deploy/compose/Dockerfile.platform，无法构建旧镜像" >&2
    return 1
  fi
  docker build -t "auriga-n1-$label:local" -f "$wt/deploy/compose/Dockerfile.platform" "$wt"
  git worktree remove --force "$wt"
  cat >"$TMP/override-$label.yml" <<EOF
services:
  platform:
    image: auriga-n1-$label:local
    pull_policy: never
EOF
  dc -f docker-compose.yml -f "$TMP/override-$label.yml" up -d --no-build --force-recreate platform
  wait_platform "$label"
  dc -f docker-compose.yml -f "$TMP/override-$label.yml" stop platform
}

smoke_commit "$PARENT" "parent"
if [ -z "$FLOOR" ]; then
  echo "schemaCompatFloor 为空（设计合入起点），只测父提交"
elif [ "$FLOOR" = "$PARENT" ]; then
  echo "schemaCompatFloor 与父提交相同，不重复冒烟"
else
  smoke_commit "$FLOOR" "floor"
fi
echo "N-1 回退冒烟完成"
