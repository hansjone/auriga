# SEND-4 产品入口阻塞：同平台 Origin 无法提交消息

在基线 `58296f2` 核实：默认生产插件始终将 SubmitRequest POST 到
`config.platformOrigin + /api/ai/v1/network/submit`。生产 edge 未选择该路径，
直接返回 `404 common.not_found`，请求没有到达 gateway。域名和纯 IP 入口均复现。
这是路由前置条件失败，尚未执行双 DSH、普通登录或 PG 消息验收。

## 依据与影响

- `clients/dsh-plugin/src/network/device-provider.js` 的 `submit()` 使用上述固定路径；
  `request()` 同时用于 IAM register/tickets 和 collab keys，固定在同一 platformOrigin。
- `clients/dsh-plugin/src/local/platform.js` 要求 issuer 与 platformOrigin 严格同源，
  普通登录的设备令牌还要经过真实验签；不能把单独的提交端口当作另一平台绕过。
- `infra/edge/device_proxy.go` 的 `isDevicePath()` / `deviceMethod()` 不包含网络提交；
  `gateway.go` 的 `matchUpstream()` 只把 AI admin 路由给 ai-admin，剩余 `/api/ai/**`
  返回 `AI API not configured or allowed`。配置现有全部 AI upstream 仍不能转发。
- SEND-3 的 `support/assembly-plugin.mjs` 将提交交给单独的生产 device-provider，
  IAM 和 submit 分别使用两个真实 HTTPS Origin。该夹具明确未覆盖普通登录和完整客户端；
  不能直接复用它宣称默认插件已经完成正常认证后的双端闭环。

## 最小复现

从干净工位根目录运行，需 Go 1.27 工具链和现有锁定模块依赖：

```powershell
$head = (git rev-parse HEAD).Trim()
.\e2e\network\verify-dual-dsh-preflight.ps1 -ExpectedHead $head -Run
```

脚本解包精确 HEAD，用 `go test -overlay` 仅添加
`support/send4-platform-origin_test.go` 到原 edge 测试包；不改任何生产文件，
复用原 `accessFixtureConfigured` 的真实随机端口 listener/authority 处理与清理。
两个模式都先核 `/ai/v1/models` 正控确实到达已配置 AI upstream，再核精确提交路径。
上游仅返回 HTTP 418 传输哨兵，无登录、JWT 验签或业务成功替身；输入令牌为合成探针。
请求采用设备 Bearer、JSON、无 Cookie/Origin，并保留 Node fetch 的 `Sec-Fetch-Mode: cors`。
本测试只判断路由是否到达上游，提交的 `{}` 不代表合法业务请求。

当前首失为 `SEND-4 platform Origin must route network submit`：两个子例均为
`status=404 submitUpstreamCalls=0`，测试退出码 1、零 skip。
红测是未修复产品缺口，不能计为坏实现反证或 E2E 通过。
归档 SHA256、测试和相关生产源码 SHA256、命令、退出码及完整日志保存在
`target/verify-dual-dsh-preflight-*/manifest.json` / `edge-routing.log`。

## 主控接线与未覆盖

需由主控另派允许修改 `infra/edge` 的实现：为该精确 POST 路径接入 gateway 的
生产网络 submit HTTPS listener，同时保留当前设备凭据及来源连接校验、普通登录
Origin/issuer 边界，避免套用模型 AI audience 或浏览器 admin 会话换票。
应覆盖域名/IP、Node 原生请求头、非法方法/编码路径、浏览器凭据及上游未配置负例。

任务明确“禁止改产品代码；若发现产品缺口，停下写清”，因此本线停在此处。
尚未交付双 DSH 往返驱动、正常认证/join/source Get、接收开关控制、PG/宿主关联证据、
三项故障矩阵和单生产 overlay 业务反证；该 preflight 也不是完整 SEND-4 verify。
既有 T3b/T4/SEND-2/SEND-3 断言及代码均未改变。
