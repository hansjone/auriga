# 插件发行包

成员入口：[中文安装说明](../user/install-plugin.zh.md)。插件为 MIT，平台部署与插件分发分开。

## 本地构建

仓库根，Node 22.19.0（npm 10.9.3）、pnpm 11.28.6，独立 node_modules：

```sh
pnpm install --frozen-lockfile --store-dir "$(pnpm store path)"
node --test scripts/plugin/package.test.mjs
node scripts/plugin/package.mjs --version 0.1.0-rc.1 --out dist/plugin
```

PowerShell 的 store 参数可用 `$env:AURIGA_PNPM_STORE`（未设置时用 `$(pnpm store path)`）。脚本构建当前 `web/panel`，强制关闭构建 mock，不复用旧 UI。显式 SemVer 注入临时 staging，不改源码版本或锁文件。产物：

- `auriga-dsh-<版本>.tgz`：Node/browser 入口、Cordis bundle、UI/SRI 元数据、运行依赖声明；无开发依赖、测试及生命周期脚本。
- 同名 `.tgz.sha256`：标准 SHA256 校验行。
- `auriga-dsh-<版本>.json`：版本、摘要、字节数、engines、文件清单。

同一源码字节、锁文件和工具版本产生同字节 tgz（npm 固定 tar 元数据），源文件时间变化不影响摘要。跨系统复现需相同换行字节，推荐 `core.autocrlf=false` 的干净 checkout。依赖仍由 DSH/pnpm 按声明安装，tgz 不含 node_modules，不声称完整依赖安装离线或字节固定。已有版本资产拒绝覆盖。

## CI 与发布

`.github/workflows/plugin-release.yml` 在相关 PR 测试并上传 artifact；只有推送 `plugin-v<SemVer>` tag 才创建 GitHub Release 并附三份资产，预发布版标为 prerelease。package job 构建两次并比较 tgz、校验摘要；release job 单独持 contents:write。无 npm publish 或部署。本任务不创建或推送 tag。

维护者先完成正常审查和精确 HEAD CI 门禁，再创建对应版本 tag。成员用 Release 包，不用仓库根目录的 GitHub 安装 spec。

## 验收层次

打包测试证明内容、版本、摘要和重复构建一致，不等于完整客户端通过。维护者在隔离 HOME/USERPROFILE/DSH_HOME、独立 npm/pnpm cache 的 **DSH 0.2.1-rc.3** 执行：

1. 按成员文档本地 tgz 与 URL 安装；`dsh --profile web --dump-config` 出现 `auriga-dsh` bundle。
2. 随机端口启动临时 DSH，真实浏览器看到 Auriga 侧栏与原生设置。
3. 在设置连接自己的本机 Compose 平台，到普通登录页；分别记录登录、Team 和模型授权是否通过。
4. 退出后验证登录态失效；清理本次临时进程，不重启用户 DSH。

本机真实 PG/DSH 全链只由 root 串行占唯一槽；工人提供包、命令和边界供主控复验，config dump 或原生模块测试不能代替页面与登录验收。实际结果见工位 `.agent-report.md`，报告不随包发布。

## 2026-10-30 本机安装实测

Windows、Node 22.19.0、npm 10.9.3、pnpm 11.28.6；DSH 在工位 target 目录用 `npm install --prefix <隔离目录> @deepseek-ai/dsh@0.2.1-rc.3` 安装，不改全局 rc.2。每轮独立 HOME、USERPROFILE、DSH_HOME 和 npm/pnpm 缓存：

1. 通过该隔离安装的 `dsh --version` 确认 `0.2.1-rc.3`；`dsh --profile web --dump-config` 初始化 profile，profile 的 packageManager 固定 pnpm 11.28.6。
2. `dsh plugin --profile web add <产物tgz绝对路径> --store-dir <隔离缓存>` 返回 0；产物版本、Node/browser 入口、UI 与 metadata 均在安装目录存在。
3. 新建另一隔离 HOME，以随机回环端口提供同一 tgz；`dsh plugin --profile web add http://127.0.0.1:<随机端口>/auriga-dsh-0.1.0-rc.1.tgz --store-dir <另一隔离缓存>` 返回 0。该测试只验证 URL spec；未发布 GitHub Release，也未实测公网 HTTPS 资产 URL。
4. 两个 profile 的 `dsh --profile web --dump-config` 都返回 0 且包含 auriga-dsh。官方 app-boot 公开兼容性检查器接受 rc.3、拒绝 rc.2。

**未验证**：真实浏览器入口显示、Compose 平台连接、首次登录与退出。未启动 DSH 宿主/Compose，遵守 root 唯一串行槽；不能据此声称第5项全链验收完成。回环包服务已关闭，完整安装命令、退出码与日志保存在工位 target/plugin-dist，提交后的最终产物路径和摘要见工位报告。
