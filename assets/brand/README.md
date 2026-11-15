# Auriga DSH 品牌资源

原创标志由御夫座五点轮廓与内部字母 A 构成；深蓝底、雾蓝连线、白色主星。无外部素材或字体下载。

- `auriga-dsh-wordmark.svg`：横版主标（深色背景适用），文字采用 Segoe UI / Arial 回退。
- `auriga-dsh-icon.svg`：方形主标，64 × 64 画布。
- `favicon.svg`、`favicon-32.png`、`apple-touch-icon.png`（180 × 180）：浏览器图标。
- `auriga-dsh.ico`：Windows 图标，包含 16、24、32、48、64、128、256 像素七帧，32 位 PNG 编码。

门户与管理台的 `public/brand/` 保存浏览器资源副本；共用 Vue 标志是 `web/shared/src/BrandMark.vue`，修改标志时须同步。容器构建会包含 public 资源。

PNG / ICO 由同一 SVG 经 Edge 无头渲染生成：运行 `node assets/brand/generate.mjs`。需要本机 Edge 及 `playwright@1.56.1` 工具；后者不进入项目运行依赖。若工具安装在仓库外，可将环境变量 `AURIGA_PLAYWRIGHT_MODULE` 设置为其 `index.mjs` 的绝对 file URL。脚本只生成品牌资源并同步两个 public/brand 目录，不启动项目服务。

截图等过程证据存放在主控本地 runs 目录，不进入仓库。
