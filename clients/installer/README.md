# Auriga Desktop installer (Windows test build)

设计见 `docs/design/dsh-installer.zh.md`。包一层不 fork：上游 `@deepseek-ai/dsh`、自带 Node 与 pnpm 是可替换组件，我们的插件、首启配置与安装核心单独存放。

```powershell
cd clients/installer
node scripts/build.mjs --exe                 # 默认上游版本见 upstream.json；--exe 同时出单文件安装器
node scripts/build.mjs --upstream 0.2.1-rc.4 # 换上游版本重新出包
node scripts/build.mjs --platform-origin <https 地址> --without-libreoffice   # 首启默认平台地址；精简版
pnpm test                                    # 单测；设置 INSTALLER_STAGE=<包目录> 额外校验成品包
node scripts/e2e.mjs --old <包目录|setup.exe> [--new <包>] [--broken <包>] --sandbox <目录>
```

本包是 pnpm 工作区成员（`@auriga/installer`），无第三方依赖，脚本只用 Node 内置模块。输出默认在 `target/`（已被 `.gitignore`）。
