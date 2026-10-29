# 双隔离原生 DSH 互发演示

此入口复用 `scripts/beta-ui/plugin-dsh.mjs` 的锁定 rc.3 原生宿主、成品插件安装、正常登录和隔离目录管理。它不启动平台、PG、Broker、N1 collab，也不生成用户、设备或成功回包。所有命令从当前仓库根运行；真实启动和验收由主控一次一条执行。

## 先辨明正在验证什么

- **已合 SEND4 隔离全链**：[dual-dsh.zh.md](../../e2e/network/dual-dsh.zh.md) 使用真实双 DSH/PG/生产传输与测试桥、测试身份和根；有故障/反证、公开原生 API 的持久历史及精确 prompt 计数。它不是现有 Beta 后台或公开部署，不证明模型往返。
- **本入口**：给配置完整的所选真实部署供应两个全新隔离客户端；登录、会话和工具执行走现有原生界面。离线预检/计划通过只说明配置和目录满足静态约束，部署、READY 和收发仍未验证。
- **默认本机 Beta**：门户 `http://127.0.0.1:18080` 的 combined 后台仍缺完整 N1 standalone collab/key-operations、可信外部 HTTPS/WSS、离线根签发布与安装 pin 供应。已有 submit mTLS listener 的 401 不代表业务已通。不能直接把它填进本入口凑成功。

安装公开配置导出、transport Origin/握手及所选部署的 N1 接线必须各自完成 CI、独审和合入，并由主控核精确源码后再真实验收。本入口不替代这些依赖，不因收到一个合法 JSON 宣告 L55 关闭。Compose/NAS/Windows Beta 的后台接线分别验收，不能互相推导。

## 准备受信公开配置

需 Windows 原生开发环境、Node 22.12+、仓库锁定依赖和现有 launcher 所需的 pnpm/构建工具。脚本不会升级宿主、读取外部私钥或替用户配置 API key。

运维先确认所选部署确实具备真实 IAM、N1 Mailbox/key-operations、批准的协议签名钥、根签 keyset/announcement、对应租户 realm、edge 路由及可信 HTTPS/WSS。浏览器与 Node 必须已信任 TLS 链、证书主机名匹配；不能关验证、改系统信任、用 TLS CA 冒协议根、从首次网络响应下载根或猜 realm。外部 WSS 可以和平台不同 origin，仍由安装配置固定。

使用安装导出流程审核后的**直接 connect 对象**作为本地 UTF-8 JSON（不是 `{ "connect": ... }` 包装），仅含现有 `config.connect` 合同字段。例如以下是形状说明，根占位符不能执行：

```json
{
  "platformOrigin": "https://portal.example.com",
  "issuer": "https://portal.example.com",
  "clientId": "auriga-dsh",
  "providerId": "team",
  "network": {
    "realm": "1",
    "wssUrl": "wss://network.example.com/net/v1/ws",
    "roots": [
      {
        "kid": "root-1",
        "public_key_spki": "<受信规范Ed25519公开SPKI-DER-base64url>"
      }
    ]
  }
}
```

platform/issuer 必须属于同一受信 HTTPS origin；realm 是真实租户分配的规范正 uint64 字符串（真实值也可能是 `1`，不能按数值识别 fixture）。根为规范无填充 base64url Ed25519 公钥 DER，kid 唯一；不接收 PEM、URL、私钥、token、TOFU、skipVerify 或未知字段。预检输出根 DER SHA-256 供与运维既有独立记录比对，但本工具不能证明该记录或未签 register 响应的来源。

若需要先用 register 查询 realm，按安装导出手册使用同租户**独立准备设备**；不要提前登记本次 A/B 目标设备，否则可能消耗首次 `created=true` 而没有完整账本。首次注册后缺账本会失败封闭，重复登录不保证新节点。保留已有目标的完整 profile/ledger，不能删账本、清 keyset cache、重置 revision 或 seed 凭据凑通。

## 离线预检与计划（不启动进程）

```powershell
$demoConnect = 'D:\auriga-beta-state\approved-connect.json' # 运维审核的公开文件
$demoParent = 'D:\auriga-beta-state\network-demo'
[void][IO.Directory]::CreateDirectory($demoParent)
$demoRun = [guid]::NewGuid().ToString('N')
$demoA = Join-Path $demoParent ('A-' + $demoRun)
$demoB = Join-Path $demoParent ('B-' + $demoRun)
node scripts/beta-ui/demo-network-send.mjs preflight --connect-config $demoConnect
node scripts/beta-ui/demo-network-send.mjs plan --connect-config $demoConnect --state-a $demoA --state-b $demoB
```

`preflight` 返回 `configuration-valid/readiness=unverified`；不读取远端根、不联网、不检查 N1。缺 `--connect-config` 返回 `unconfigured/readiness=blocked`、退出码 2；参数/文件/配置无效退出码 1。退出码 0 不代表 TLS、登录或 READY 已通过。

`plan` 返回 `prepared-plan/readiness=unverified`、两条启动参数数组及各自 profile/HOME/cwd；不会创建 A/B。目录必须是本机绝对普通路径，互不包含，与用户 DSH 和源码隔离；任何已存在的 A/B（包括空目录）都拒绝覆盖。禁止把真实 HOME/DSH_HOME 指向演示目录来绕路径守卫。

## 主控顺序启动、正常登录

确认部署与依赖齐全后，再依次执行；作者局部验证不执行这一步：

```powershell
node scripts/beta-ui/demo-network-send.mjs launch --state $demoA --connect-config $demoConnect
node scripts/beta-ui/demo-network-send.mjs launch --state $demoB --connect-config $demoConnect
node scripts/beta-ui/demo-network-send.mjs status --state $demoA
node scripts/beta-ui/demo-network-send.mjs status --state $demoB
```

每端沿原 launcher 独占创建目录、ACL、HOME、web profile、workspace、独立账本并安装锁定 rc.3。启动前从所选 HTTPS 读取公开身份源列表，要求唯一 team-account/form 且 ID 与受信文件一致；TLS、重定向或列表失败就停止，不自动换平台。`launch` 可能安装锁定 npm 依赖并构建成品插件；不会启动后台服务。

终端打印各自本机随机端口入口。入口 URL 含宿主启动票据，只在私下浏览器打开，别录屏、转录或粘贴共享日志；票据不写入文件。两端分别在 Auriga 普通登录页输入正常团队账号口令，按原生流程完成 TOTP；不添加第二套登录、不预填 vault/session/device。两端可用同一授权团队账号的不同设备，但权限和组织策略必须正常允许本次通讯。显式 `NODE_TLS_REJECT_UNAUTHORIZED=0` 会在所选部署发现阶段被拒绝。

核每端设置的当前平台仍等于受信 `platformOrigin`，登录状态确实成功，network 达到真实 connected/READY 且 messaging ready。切换平台会剥离原平台 pin，不能靠更改 UI 地址继续复用旧安装根。连接不就绪或新工具缺失时保留失败，不能只凭登录页自检、`status.running=true` 或提交工具文本称收发成功。

## 原生会话 A→B、B→A

1. 在两个 DSH 内分别创建一个普通目标会话 A-target/B-target，记录会话 ID 与工作目录。可各开一个不登记/不接收的对照会话；不要把 shell 工具调用或测试 observer 当成真实会话。
2. 在各自目标会话通过宿主真正的工具执行调用 `auriga_network_join`，参数 `{}`。若通过模型对话触发，必须看到真实工具调用与返回 `status=registered`/地址，模型写出的 JSON 不算。脚本不配模型；若当前宿主没有人工工具执行入口且模型未配置，这一步是明确阻塞，不能改用测试桥替代。
3. 打开每端 DSH 原生设置中的 Auriga“接收消息”，找到相应 sessionId/地址，显式勾选接收。要求设置确认、无 pending；只复制真实返回的目标地址，不猜 node/local。记录 A-address/B-address。
4. 在 A-target 真正调用 `auriga_send_message`，参数示例为 `{"target":"<B-address>","body":{"subtype":"text","text":"beta-A-to-B-<本轮唯一标记>"}}`。只发一次；`request_submitted` 是裁剪的提交结果，不是接收/模型执行回执。
5. 等 B-target 原生历史出现带 `[Auriga external message <message_id>]` 的该输入并持久可读，核其只有一次。再在 B-target 用同样工具向 A-address 发送另一个唯一标记，核 A-target 同样一次；不要因模型忙或没有回复就另造新幂等键重发。
6. 如需关闭接收负例：在 B 设置中关闭并等确认，再由 A 发全新唯一正文；应拒绝且 B 无额外输入，对照会话也无输入。完整四表零新增/准确拒绝仍由主控服务器只读证据核对，不只看 UI。

本流程只证明实际做到的会话收发。没有明确配置并真实观察模型回复，就不能称模型往返；已合 SEND4 也未把模型回复当判据。首次首联、递归子会话、跨租户、HA 等不在此入口验收范围。

## 提交、回执与输入次数的证据

```powershell
node scripts/beta-ui/demo-network-send.mjs evidence --state $demoA
node scripts/beta-ui/demo-network-send.mjs evidence --state $demoB
```

`evidence` 只读指定 launcher 目录的 `dsh/profiles/web/auriga-connect/network/host/ledger.json` 原子快照，不拿 writer 锁、不修改/修复旧账本，不读取 vault、设备凭据或 DSH 私有 journal。拒绝链接、错误 manifest 和超大文件；运行中快照可继续变化。

输出每笔原始提交的幂等 key/producerEpoch、规范原请求 SHA-256、原结果字节 SHA-256、内部结果 status/messageId，以及入站 recipientId/messageId/sessionId/stage/hostRequestId/原 receipt SHA-256。正文和任意额外字段不输出。完整原请求、原结果和回执仍留在原账本，摘要不能重建原字节，也不能代替独立签名验证。

主控每个方向都须关联：

- 发送端原始 submission 的原结果 `platform_stored` 与 messageId；未知结果必须保留原操作，不覆盖账本。
- 接收端相同 messageId 的 `stage=committed`、真实 receipt 内 hostRequestId/recipientId，及原生目标会话的持久输入。
- 在所选真实部署，由既有只读运维流程、真实租户上下文/RLS 核 network_submit/message/mail_item/recipient 各单份、状态 `host_accepted`、host_request_id 对应。凭据仅从受限文件供应；不使用超级用户绕 RLS，也不把 SQL 输出中的正文/身份贴入共享日志。
- 人工核目标会话同一个外部 messageId/正文恰好一条、对照会话零条，记录为“人工已核”或“人工待核”。自动摘要固定 `nativeInputCount=null`，绝不从 committed/invoking 或收件条数推导 prompt 次数。机器级精确调用次数只能引用已合 SEND4 的公开 SessionController.inspect/持久回读与观察证据；它不自动成为本次部署的计数证明。

只有主控在本次所选部署拿齐双向关联、真实会话历史和人工计数后，才可记录该部署演示成功。脚本从不输出 `complete=true`；仍未验证的项目不能打勾。

## 失败定位、停止与保留

先保留首次失败的命令、精确 git HEAD、退出码、所处阶段和脱敏证据。`configuration-valid` 之后的失败优先区分 TLS/provider discovery、普通登录、首次 register/账本初始化、根签公告/keyset、WSS Origin/握手、READY、join/source、receive、submit、receipt、原生持久输入；不把一种失败当另一种成功反证。

每端私有 StateDir 的 `dsh.log`、`profile-config.log`、`plugin-install.log`、`build.log`、`host-install.log` 与 `plugin-dsh.json` 可定位阶段。输出转发器会脱敏启动票据；其他私有日志仍可能含本地路径/公开配置，只摘白名单错误类别，不分享原 profile/vault/账本。TLS 不受信应交运维补齐真实链，不能开 insecure；旧 cache/账本拒绝继续保留，交独立恢复任务。

```powershell
node scripts/beta-ui/demo-network-send.mjs stop --state $demoB
node scripts/beta-ui/demo-network-send.mjs stop --state $demoA
node scripts/beta-ui/demo-network-send.mjs status --state $demoA
node scripts/beta-ui/demo-network-send.mjs status --state $demoB
```

`stop` 复用 launcher 的 PID/创建时间所有权校验，只停本端自有 DSH/转发器，保留所有 profile/ledger/日志；不停止所选部署或现有 Beta，不改数据库、不回收设备。已有目录不能再 `launch` 覆盖，当前脚手架没有恢复启动入口。需要重新演示时先保留原失败数据，由主控确认新目标设备/首次创建条件，再用全新 A/B 目录；不自动删除、不清理他人目录或链接目标。

局部守卫（不启动真实 DSH/PG/N1）：

```powershell
node --test scripts/beta-ui/demo-network-send.test.mjs e2e/beta-ui/plugin-dsh.test.mjs
```
