# Auriga DSH installer (Windows prototype)

设计见 `docs/design/dsh-installer.zh.md`。包一层不 fork：上游 `@deepseek-ai/dsh` 与自带 Node 是可替换组件，我们的插件、首启配置与安装核心单独存放。

```powershell
cd clients/installer
node scripts/build.mjs                       # 默认上游版本见 upstream.json
node scripts/build.mjs --upstream 0.2.1-rc.4 # 换上游版本重新出包
node --test "test/**/*.test.mjs"             # 单测；设置 INSTALLER_STAGE=<包目录> 额外校验成品包
node scripts/e2e.mjs --old <包> [--new <包>] --sandbox <目录>   # 隔离环境安装/启动/升级/回滚/卸载
```

没有 `package.json`：脚本只用 Node 内置模块，避免改动 pnpm 工作区锁文件。输出默认在 `target/`（已被 `.gitignore`）。
