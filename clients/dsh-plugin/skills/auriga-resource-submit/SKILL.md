---
name: auriga-resource-submit
description: 用户说“发布到 Auriga”“帮我把这个技能送审”“把这版 DSH 插件提交到 Auriga”“帮我升级这个插件/技能”时使用。支持首次发布与已有资源版本升级，预检桌面会话工作目录中的 DSH 插件或技能并提交审核。Use when the user asks to publish, submit, or upgrade a local DSH plugin or skill on Auriga.
---

# 桌面 DSH 资源发布与升级

用户说「把这个技能发布到 Auriga」「帮我把 plugins/example 提交送审」「帮我升级这个插件到新版本」时，按以下流程执行。提交成功后平台状态为「待审核」。

> **身份说明**：提交人姓名与部门由 Auriga 平台根据当前登录认证信息自动记录，Agent **不需要也不允许**填写作者（`author`）字段。

## 一、标准流程（首次发布 / 版本升级）

1. **先查已有资源（尤其是升级场景）**：
   - 调用 `auriga_resource_list`（可按 `kind` 过滤），查看当前用户已拥有的资源、最新版本号与状态，获取目标 `packageId`。
   - 如果是**升级已有资源**：
     - 确认新版本号严格不同于且高于现有版本（插件修改 `package.json` 的 `version`，技能更新 `metadata.version`）；
     - 整理本次升级的**变更说明（`changeNotes`）**，说明新增功能、修复内容及权限变化，方便审核员审查。
2. **核对必填信息与简介质量**：
   - 确保 `package.json`（插件）或 `SKILL.md`（技能）中的 `description` 准确描述了资源的用途和使用场景，不要保留空值或模板占位文案。
   - 如实声明实际所需权限 `permissions`（`tools`、`outboundHosts`、`dataAccess`），插件如 `package.json` 已有 `license` 可省略 `metadata.license`。
   - **不要把 npm 带 `@scope/` 的包名填进 `metadata.name`**；留空 `metadata.name` 即可自动派生合法的目录名（小写字母、数字和短横线）。
3. **预检（`auriga_resource_prepare`）**：
   - 调用 `auriga_resource_prepare`，工具会自动检测是 `create`（首次发布）还是 `upgrade`（命中当前用户同名资源，返回 `existingPackage`）。
   - 若返回的 `warnings` 提示版本号冲突，请先递增版本号再重新预检。
   - 若用户只要求预检，或资料尚待确认，向用户展示预检摘要（名称、版本、模式、简介、权限、变更说明、文件数、预估风险等级）后再决定是否送审。目录文件有改动时须重新预检。
4. **提交送审（`auriga_resource_submit`）**：
   - 用户明确要求提交/升级且资料齐全时，用同一会话的句柄调用 `auriga_resource_submit`，填写清晰的 `reason`（送审理由）与 `changeNotes`（变更说明，升级强烈建议提供）。
   - 升级已有资源时，可显式传入 `packageId`（预检命中同名已有资源时也会自动关联该 `packageId`）。
   - 返回 `state: "pending_review"` 后，告知用户已进入审核队列，并提供 `packageId` 与 `versionId` 供跟踪。

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
    "license": "MIT",
    "changeNotes": "首次发布：提供标准排查步骤与检查清单",
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
    "tags": ["operations"],
    "changeNotes": "v1.1.0：新增导出能力，无新增外连域名或文件权限",
    "permissions": { "tools": [], "outboundHosts": [], "dataAccess": [] }
  }
}
```

`package.json` 声明 npm 名、版本、简介、许可证和 `dsh.bundle.patch`。将所声明的 patch 和运行时依赖一并放在目录内，交由工具核对归档。插件目录须符合统一资源的归档要求，包括移除包脚本与原生构建声明。插件版本取自 `package.json`，提供 `metadata.version` 时两者必须一致。技能的名称和简介须与 `SKILL.md` 一致。插件已有 `engines.dsh` 时，它须与显式 DSH 版本范围一致。

实际声明中完整填写工具名、外连主机，以及适用的 `workspace_read`、`workspace_write` 或 `user_selected_files`。保留运行所需依赖，并如实说明权限。

## 提交参数与错误处理

插件预检可用 `metadata.tags` 填最多五个功能标签，例如 `["data-analysis", "operations"]`。标签由小写字母、数字和短横线组成，每个最多 32 个字符。

`auriga_resource_submit` 接受 `handle`、1 至 1024 字符的非空 `reason`，以及可选的 `packageId` 与 `changeNotes`。句柄在同一登录、设备和会话中有效 15 分钟。

- `common.validation_failed` 时，按错误中指明的具体字段检查相对目录、种类、清单字段、版本、权限和归档内容，修正后重新预检。
- `common.unauthenticated` 时，请用户确认 Auriga 已登录，再重新预检。遇到 `common.forbidden` 时，检查当前是否为有效桌面主会话、当前身份是否仍是租户内部成员以及目标资源是否属于当前用户。
- `common.not_found` 时，在当前会话重新预检。遇到 `common.conflict`（例如版本已存在或送审参数变化）时，递增版本号或重新预检后再提交。
- `common.unavailable` 时，待连接恢复后使用同一组提交参数重试。遇到 `common.rate_limited` 时，等待当前预检完成或旧句柄到期。
