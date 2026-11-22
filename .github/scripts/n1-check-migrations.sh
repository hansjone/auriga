#!/usr/bin/env bash
# N-1 兼容声明门禁（设计 D2）：相对真实父提交，校验
#   1. deploy/site 声明自洽（schema-compat.json 与 format-versions.json）；
#   2. schemaCompatFloor 与迁移内容一致：没有新增/改动带 -- auriga:destructive 的迁移时继承父提交
#      的 floor；有时必须前移（父提交本身或其祖先，且晚于父提交的 floor）；
#   3. 变更迁移命中破坏性关键字却未标记则失败（辅助检查，真实门禁是回退冒烟）。
# 用法：n1-check-migrations.sh <父提交>
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
PARENT="${1:?需要父提交}"
if ! printf '%s' "$PARENT" | grep -Eqx '[0-9a-f]{40}'; then
  echo "父提交须为 40 位小写十六进制：$PARENT" >&2
  exit 1
fi
cd "$ROOT/deploy/offline"
log="$(mktemp)"
trap 'rm -f "$log"' EXIT
set +e
AURIGA_N1_REPO="$ROOT" AURIGA_N1_PARENT="$PARENT" \
  go test -count=1 -run '^TestCIN1Gate$' -v . >"$log" 2>&1
rc=$?
set -e
cat "$log"
if [ "$rc" -ne 0 ]; then
  exit "$rc"
fi
# 防止测试因环境变量没传到而被静默跳过。
grep -q -- '--- PASS: TestCIN1Gate' "$log" || { echo "TestCIN1Gate 没有实际运行" >&2; exit 1; }
