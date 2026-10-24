# 托管实例的可信门户配置

`portalOrigin` 将安装者提供的 `deployment access.portalOrigin` 传到托管实例的受认证
`GET /auriga/bootstrap`。本轮只提供后续账户操作导航所需的可信配置，尚未接入退出按钮、
门户确认页面或真实 IAM/WS 联调。

## 配置链路与契约

agent 已通过 `deploymentconfig.Load` 校验安装配置。renderer 将 `Access.PortalOrigin()`
完整复制到 `auriga-dsh` Loader row 的 `config.portalOrigin`，不使用每实例 hostname、
issuer、profile patch、请求头或前端 query 推导门户。`AURIGA_PLATFORM_URL` 继续仅供
Node 侧 self/heartbeat/清单读取使用，内部平台地址不进入新的客户端字段。

`contracts/plugin/managed-config.schema.json` 的可选字段 `portalOrigin` 直接引用既有
`deployment/access.schema.json#/$defs/Origin`。插件的 `ManagedConfig` 与 `Origin`
由同一契约生成，不新增手写类型或第二份 schema 规则。

插件在启动时复用既有严格 Origin 校验并保存冻结副本：只接受完整的
`{scheme, host, port}`，scheme 为 `http` 或 `https`，host 为规范 IPv4 或小写 ASCII
DNS/A-label，port 为 1–65535 整数。拒绝 null、数组、URL 字符串、缺失子字段、
额外字段、userinfo、路径、查询、片段、尾点、编码 host、通配符与歧义 IP。
该字段不含凭据；运行时读取配置后修改原对象不会改变 bootstrap 的目标。

新 renderer 总是写入完整字段。兼容旧 Loader row 时允许整个字段缺失，bootstrap
省略 `portalOrigin`；后续客户端必须据此禁用门户入口。缺失不触发任何默认门户或
`AURIGA_PLATFORM_URL` 回退。显式 null 或坏配置会拒绝启动。

## 域名与纯 IP

域名配置示例的 bootstrap 片段为：

```json
{
  "portalOrigin": {
    "scheme": "https",
    "host": "portal.example.com",
    "port": 8443
  }
}
```

纯 IP 配置保留 HTTP 和实际门户端口：

```json
{
  "portalOrigin": { "scheme": "http", "host": "192.0.2.10", "port": 8080 }
}
```

bootstrap 始终返回结构化 Origin，包含默认端口或非默认端口。后续导航代码应按同一
Origin 语义序列化，默认端口可省略；当前不在客户端拼接跳转或执行注销请求。

## 配置所有权与认证边界

renderer 保留 `plugins.auriga-dsh.portalOrigin` 的整个子树。profile 对字段本身、
scheme/host/port、未知子字段、深层子字段的 patch 均拒绝；整行替换、斜杠与括号形式
不能绕过 patch 路径验证。其他托管配置仍遵循既有规则。

`portalOrigin` 不改变 `browserOrigin`：后者继续决定实例的浏览器 Origin fence，
域名模式是实例子域，纯 IP 模式是既有门户 Origin。门户导航配置不能授予浏览器身份、
跨源 IAM fetch、平台 API 代理权限或绕过原生 Host/Origin 检查。

bootstrap 仍位于实例签名门锁之后。缺少签名、伪造 request 身份属性、错误主人/实例/
请求路径、过期签名、坏签名和重放拒绝；合法签名仍须通过既有 browserOrigin fence。
拒绝请求不会读取清单或返回门户地址；成功响应继续使用 `cache-control: no-store`，
保留原有 `platformUrl: null`。清单仍须通过原有签名验证才能得到成功 bootstrap。

## 聚焦验证与边界

- Go renderer：域名/纯 IP × HTTP/HTTPS × 默认/非默认端口，确认门户配置来自
  deployment access、browserOrigin/原生白名单保持原边界、内部 alias 不进入 row；
  对门户整个字段与所有子字段覆盖形式做负测。
- 插件：`managed-portal-origin.test.js` 经生产 `createManaged.apply` 安装门锁、
  宿主认证和 bootstrap handler，以内存 EventEmitter 发出请求，不启动项目服务或监听端口。
  使用临时 Ed25519 身份与面板签名，覆盖认证条件、Origin 拒绝、域名/纯 IP 完整端口、
  缺失配置、伪造头/query、原配置修改及额外字段拒绝。
- 契约：既有 `ip-mode.test.mjs` 验证同一 Origin 引用、可选兼容、正例和结构负例；
  按既有 json-schema-to-typescript 命令重生成并检查类型。

这些验证不代表真实部署已完成门户注销、IAM 会话撤销、真实浏览器 cookie、
WebSocket 关闭或实例再次进入验证。本轮没有 UI 入口，也没有新增 logout POST、
跨源 IAM 请求、代理路由或部署变更。涉及 contracts 与 infra/agent 的后续 PR 必须标为
【待审查】，由独立会话审查并留下记录；作者不得自行合并。
