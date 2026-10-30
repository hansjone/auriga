# 设置页的 DSH UI Origin

设置接口先调用公开 `connection.requestRejection`（宿主认证及 Host 准入），再检查单一 Host/Origin、Fetch Metadata、JSON 与 `X-Auriga-Settings: 1`。用户保存的平台地址是连接偏好，不能决定 DSH UI 的信任源。

- managed：复用受信节点 renderer 注入的结构化 `config.browserOrigin`（已有 managed 配置契约）。此值优先于进程环境配置。
- 本机 HTTPS 反向代理：安装者在启动 DSH 进程前设置 `AURIGA_SETTINGS_UI_ORIGIN=https://instance.example.com:8443`。必须是规范 HTTP(S) origin，无尾斜杠、路径、凭据、query 或 hash；默认端口应省略。不从浏览器表单/profile 偏好读取此值。变更需要重新加载插件配置或重启进程，插件不会主动重启用户 DSH。
- 无上述配置：按 DSH 公开的 `node:http` 直连语义，要求 Origin 精确匹配 `http://Host`。HTTPS 代理必须显式提供可信外部源；不会从 `Forwarded` 或 `X-Forwarded-*` 猜测。

代理可以改写上游 Host，但该 Host 仍须先通过宿主认证/准入。外部 Origin 必须与受信配置的协议、主机和有效端口完全一致。安装者仍需正确配置 DSH 自身的公开 trustedHosts/认证入口；此设置不会绕过宿主拒绝。

公开 API 依据：DSH 基线 `packages/host/webserver/src/index.ts` 的 `register` 与 `node:http.createServer`，以及 `packages/client/connection/src/rpc.ts` 的 `requestRejection`。这些源码仅用于核对接口，插件不导入其内部文件。managed Origin 校验复用 Auriga 自有 `managed/host-auth.js`。
