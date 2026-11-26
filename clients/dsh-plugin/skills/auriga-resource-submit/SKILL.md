---
name: auriga-resource-submit
description: 用户说“发布到 Auriga”“帮我把这个技能送审”“把这版 DSH 插件提交到 Auriga”时使用。预检桌面会话工作目录中的 DSH 插件或技能，按用户要求提交成员资源审核。Use when the user asks to publish or submit a local DSH plugin or skill to Auriga for review.
---

# 桌面 DSH 资源送审

用户说「把这个技能发布到 Auriga」「帮我把 plugins/example 的新版本提交送审」时，使用下面的预检和送审流程。告诉用户成功后平台状态为「待审核」。

1. 确认资源种类、当前会话工作目录中的相对路径、作者、许可证和实际所需权限。已有资源的新版本可复用已知的 `packageId`。若同名资源已存在且 ID 未知，使用可用的当前成员目录查询入口按种类和目录资源名定位用户拥有的目标。若无法确定目标，请用户指定此次追加的资源。其他必填资料不足时也请用户补充。
2. 调用 `auriga_resource_prepare`。如果用户只要求预检，展示返回的种类、目录资源名、版本和归档大小，然后结束。如果用户已明确要求提交到 Auriga，且资料齐全，直接用同一会话中的句柄调用 `auriga_resource_submit`。目录文件有改动时重新预检。
3. 当返回 `state: "pending_review"` 时，告知用户资源已进入审核，并提供 `packageId` 和 `versionId` 供跟踪。流程到此结束。

## 技能目录示例

```text
<会话工作目录>/
  skills/example/SKILL.md
```

`SKILL.md` 需要 YAML front matter，包含 `name` 和 `description`，其余内容使用纯文本技能格式。

```json
{
  "kind": "skill",
  "directory": "skills/example",
  "metadata": {
    "version": "1.0.0",
    "author": "Example",
    "license": "MIT",
    "permissions": { "tools": [], "outboundHosts": [], "dataAccess": [] }
  }
}
```

## 插件目录示例

```text
<会话工作目录>/
  plugins/example/package.json
  plugins/example/cordis.patch.yml
  plugins/example/src/index.js
```

```json
{
  "kind": "plugin",
  "directory": "plugins/example",
  "metadata": {
    "author": "Example",
    "license": "MIT",
    "permissions": { "tools": [], "outboundHosts": [], "dataAccess": [] }
  }
}
```

`package.json` 声明 npm 名、版本、简介和 `dsh.bundle.patch`。将所声明的 patch 和运行时依赖一并放在目录内，交由工具核对归档。插件目录须符合统一资源的归档要求，包括移除包脚本与原生构建声明。插件版本取自 `package.json`，提供 `metadata.version` 时两者必须一致。两个种类都支持可选 `metadata.name`、`metadata.description` 和 `metadata.dshVersionRange`。技能的名称和简介须与 `SKILL.md` 一致。插件已有 `engines.dsh` 时，它须与显式 DSH 版本范围一致。

实际声明中完整填写工具名、外连主机，以及适用的 `workspace_read`、`workspace_write` 或 `user_selected_files`。保留运行所需依赖，并如实说明权限。

## 提交参数与修正

插件预检可用 `metadata.tags` 填最多五个功能标签，例如 `["data-analysis", "operations"]`。标签由小写字母、数字和短横线组成，每个最多 32 个字符。优先沿用市场已有标签。作者在市场上的姓名和部门由平台根据当前成员身份保存。

`auriga_resource_submit` 接受 `handle` 和 1 至 1024 字符的非空 `reason`。向已有资源追加版本时，提供该资源的 `packageId`。句柄在同一登录、设备和会话中有效 15 分钟。

- `common.validation_failed` 时，检查相对目录、种类、清单字段、版本、权限和归档内容。修正后重新预检。
- `common.unauthenticated` 时，请用户确认 Auriga 已登录，再重新预检。遇到 `common.forbidden` 时，检查当前是否为有效桌面主会话、当前身份是否仍是租户内部成员以及目标资源是否属于当前用户。
- `common.not_found` 时，在当前会话重新预检。遇到 `common.conflict` 且需要更改送审理由或目标资源时，也要重新预检。
- `common.unavailable` 时，待连接恢复后使用同一组提交参数重试。遇到 `common.rate_limited` 时，等待当前预检完成或旧句柄到期。
