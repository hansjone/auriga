# Auriga DSH

MIT-licensed DSH plugin for Node.js 22+. Package name: **auriga-dsh** (not published by default).

Installing the published plugin in DSH needs no platform environment variables. Its native main page and independent Auriga DSH settings section load from the packaged UI and show an unconfigured connection. DSH's existing authentication still protects these local routes. Installing does not connect an Auriga DSH account or grant resource access.

Set `AURIGA_MODE` when explicitly selecting a runtime:

- `standalone` (default): local emergency account + pluggable JS identity connectors, single owner, no platform connection.
- `managed`: platform-hosted instance — identity door, plugin/workspace guards, `/_auriga/health`, heartbeat, team panel shell, `ctx.get('auriga')`.
- `connect`: local DSH connected to a team platform. A saved platform origin in
  native Auriga settings activates the local team login flow without requiring
  members to set this environment variable.

## Library

```js
import initialize, { enrollEmergency, createManaged } from 'auriga-dsh/library';

const { create } = initialize({ AURIGA_MODE: 'standalone' });
const runtime = await create({
  config: {
    owner: { providers: ['emergency'] },
    connectors: [
      {
        type: 'emergency',
        id: 'emergency',
        config: enrollEmergency('alice', process.env.PASSWORD),
      },
    ],
  },
});
```

Managed runtime (tests / hosts):

```js
const managed = await createManaged({
  env: process.env,
  startHeartbeat: false,
});
managed.apply(ctx); // Cordis-like host
```

Cordis bundle row: `auriga-dsh` (the root export is `src/plugin.js`). The explicit Node-only `auriga-dsh/plugin` export remains available; library callers use `auriga-dsh/library`. Browser panel shell: `auriga-dsh/client`. DSH's browser collector discovers package-root rows, so a bundle row naming only the `/plugin` subpath does not register the browser entry.

## Install in DSH

Members: [中文安装指南](../../docs/user/install-plugin.zh.md). Requires DSH
**0.2.1-rc.3** or a newer administrator-validated release, plus Node.js 22.12+.
The bundle declares `engines.dsh`; missing native admission also fails with an
explicit upgrade message. Install into the profile you use:

```sh
dsh plugin --profile web add /absolute/path/auriga-dsh-0.1.0-rc.1.tgz
```

Use the Desktop-bundled CLI with `desktop` for the Desktop profile; npm-installed
DSH cannot manage that reserved profile. HTTPS tarball URLs are accepted by the
same pnpm-backed command. Reload DSH, open Settings → Auriga, enter your team's
platform origin, test and save it, then sign in. Sign out under Account and
security. [Release preparation](../../docs/dev/plugin-distribution.md) uses
`node scripts/plugin/package.mjs --version <SemVer>` from the repository root
and produces a versioned tgz, SHA256 and content manifest without install hooks.

Add the published package or prepared `.tgz` through DSH's native plugin manager. The package declares `dsh.bundle.patch` and ships `cordis.patch.yml`, its Node/browser entries and built native UI. Installation uses DSH's normal package/script approval policy; no installation hook builds code or contacts headquarters.

For a development directory link, first install the repository's documented workspace dependencies (including this plugin's runtime dependencies), then run from the repository root:

```sh
node clients/dsh-plugin/scripts/package-ui.mjs
```

Add **`clients/dsh-plugin`** in DSH. A fresh checkout does not contain the ignored `ui/` build output; adding an unprepared directory can install the bundle but cannot serve its UI. Rebuild after changing panel sources. To prepare the distributable tarball, run `npm pack` in this plugin directory; its existing `prepack` performs the UI build. A consumer of the resulting tarball needs neither the monorepo nor Vite.

The default installed surface is unconfigured and protected by native DSH authentication. Bundle installation does not complete Auriga login. Save the team platform origin in native Auriga settings and use the ordinary login page.

The release preparer runs `npm pack` from this monorepo. `prepack` builds the native `web/panel` module and includes `ui/auriga-team-panel.js` and its package-version/SRI metadata. Published installs have no install hook, Vue dependency, frontend build step or requirement to download a homepage from headquarters. Managed mode continues to use the verified platform manifest; it never falls back to packaged UI after verification or loading failure.

The standalone identity library remains available separately from the desktop team login flow. The unconfigured UI does not claim authentication, Team connectivity or model authorization; each has its own status after configuration.

See `docs/dev/dsh-plugin.md` and `contracts/plugin/README.md`.

An installer may add static network pins to the existing loader row's
`config.connect` (replace the placeholder with the installation's public root):

For verified local public-material export, exact loader placement and isolated
acceptance steps, see [network installation](../../docs/dev/network-installation.md).
The helper checks consistency; trusted operators must establish the real HTTPS
registration source and independently confirm the offline root fingerprint.

```yaml
connect:
  platformOrigin: https://portal.example.com
  issuer: https://portal.example.com
  clientId: auriga-dsh
  providerId: team
  network:
    realm: '1'
    wssUrl: wss://portal.example.com/net/v1/ws
    roots:
      - kid: root-1
        public_key_spki: '<canonical unpadded base64url Ed25519 public SPKI DER>'
```

`network` is optional; when present every field is required and unknown fields
are rejected. Realm is a canonical positive uint64 string; roots contain 1–16
public pins with unique kids. PEM, private keys, root URLs, TOFU, skipVerify,
initialize and created are rejected. SPKI itself is the pin. The fixed WSS URL
may use a custom path, independent host/port or an IP address; userinfo, query,
fragment and non-WSS URLs are rejected. Platform and issuer still require the
same HTTPS origin; `allowLocalHttp` remains an isolated loopback test exception
and never permits WS or disabling TLS.

Only the installer supplies these values. They are copied, deeply frozen and
passed to trusted provider prepare, never exposed through settings, login input,
status, diagnostics or SDK. In connect mode, successful native device login starts
the official connection-only provider when these pins are installed. It validates
the fixed IAM registration/ticket and collab keyset/announcement endpoints with
the existing device credential, without another login or approval. Without pins
or a trusted injected provider, networking remains unconfigured. No test roots
are supplied by default. A complete trusted `aurigaNetwork` provider temporarily
replaces the default; detaching it restores the installed default.

First creation holds one host-ledger writer lock throughout bootstrap connection
C1, atomic complete-ledger commit, actual C1 shutdown, a fresh ticket and normal
strict READY on connection C2. Only C2 success exposes connected. Existing ledgers
use their original incarnation directly and never bootstrap again. Signed root
announcements fence old keys until the matching complete keyset is durable.
The basic provider creates no HostAdapter or consent service and sends no
FETCH/ACK/LOCAL_REGISTER/SEND. Connected means this basic authenticated connection;
two-DSH messaging is not delivered. Old business ledgers without a provable
receipt cursor remain unavailable rather than resetting revision or history.
If first registration succeeds but no complete ledger is committed, a later
`created=false` registration fails closed. Signing in again does not guarantee
a new device/node. An original complete ledger can continue only after identity
and recovery evidence checks. Recovery when that ledger is missing has not been
validated; platform device retirement/new-device registration needs separate
acceptance. Only an authoritative new-node `created=true` permits bootstrap.
There is currently no automatic repair/reset flow. A failure on C2 after a complete
commit can retry using that ledger without bootstrapping again.
See [the network integration boundary](src/network/README.zh.md).

Protocol major version: **1** (`Auriga-Protocol: 1`).

```powershell
node --test "test/**/*.test.js"
node node_modules/typescript/bin/tsc -p tsconfig.json --noEmit
```

Runtime dependencies: `@noble/hashes` (Argon2id), `undici` (DNS-pinned OIDC fetch).

Copyright (c) 2026 hansjone. See [LICENSE](LICENSE).

# Typed local registration compatibility

Nonzero locals retain an immutable `addressType` and typed allocation fingerprint.
Existing sessions are classified only after trusted stat/inspect Header validation:
explicit `origin: 'subagent'` means subagent, an absent origin means session;
unknown or empty origins fail closed. Fork lineage does not grant execution.
New conversation creation fixes a session intent before registration and verifies
the created Header. Subagent execution remains unavailable before N2.

Version 1 allocation history without type keeps its original records, queue and
high-water mark; recovery reports an identity conflict instead of guessing a type
or rewriting old fingerprints. A valid empty history can start typed allocation.
READY includes the original typed pending operation. Registration success requires
matching operation/local/type/fingerprint and valid original registration revision.
The internal trusted port returns the correlated fingerprint; the formal signed
`RegisterResult` has no fingerprint echo, so its request correlation must preserve
the original fingerprint. Missing or mismatched fields cannot settle the queue.

The session module provides pure formal LOCAL_REGISTER frames and verifies signed
responses on its existing request port. This does not install a production WSS
LOCAL_REGISTER provider; runtime still requires that trusted provider externally.
Tests using fake hosts/controllers/brokers do not demonstrate a connected DSH pair.

## Managed browser entry recovery

An edge update discards its in-memory instance entry bindings. A top-level
instance document can re-enter through the existing approved portal
`/open/{instanceId}` and one-time nonce/ticket exchange. This does not start a
stopped instance or change session lifetime, Origin or CSRF checks.

The browser client observes the public Connection state/generation API. On loss,
it reads only the current same-origin health route and follows only an exact
edge entry rejection. The first phase permits at most three read starts, with at
least two seconds after each completed read before the next. Every read has a
six-second timeout; all phases share one absolute sixty-second limit from the
original connection loss. Slow replies do not consume a separate short window.
If all three complete with network errors or HTTP 502/503, at most three further
availability reads may run at least ten seconds apart. Locally cancelled or
timed-out reads consume a start without qualifying for the availability phase;
HTTP 504 does not qualify and stops that phase. This covers edge replacement
without treating an unavailable edge as an authentication decision. HTTP 200/403 and
other 401 responses stop the check; visibility and wake events cannot reset its
deadline or consume a pending retry early.

The six-start budget covers the measured roughly thirty-nine-second WAN recovery
after a normal NAS qnet edge replacement, using the native ten-second backoff
bound for the availability phase. A stable LAN MAC alone did not remove that
delay. This does not promise recovery beyond sixty seconds or allow unlimited
polling; NAS shutdown timing, DNS, router settings and identity checks are unchanged.

A later successful generation permits recovery from a future outage. After the
bounded check, use a normal page refresh or the portal entry if recovery did not
finish. A tab marker prevents automatic re-entry loops; the recovery page retains
a manual portal link. API calls, writes and WebSocket failures keep their existing
error responses. Missing public Connection capabilities disable this feature with
a diagnostic instead of patching DSH. Browser diagnostics tagged
`auriga.entry_recovery` use one string containing the tag and JSON, readable by
text-only collectors. They contain only event/reason enums, state, attempt number,
relative time and HTTP status; they omit URLs, credentials and response bodies.

## 内置网络基础层与远端工具层

基础层随插件在统一宿主层注册，不等待总部配置、登录、托管设备凭据或远端 MCP。宿主的公开工具与原生会话服务就绪后，四个只读工具始终出现在模型可见清单中；总部配置、凭据及连接变化只切换数据源，不重新注册工具。宿主必要服务卸载或插件卸载时，基础工具失败封闭并注销。

- `auriga_net_status`：读取现有连接缓存，报告总部 origin、可达状态、进程内最近成功时间、登录或托管凭据有效期、消息网络与远端工具层状态，以及本地没有取得结果的发送记录数 `queued`。未探测或无法分类返回 `unknown`，未配置返回 `unconfigured`；数量无法读取时为 `null`。`sending.offlineNewMessages` 为 `unavailable`，明确说明离线新消息不可发送；已有连接状态不足以证明可发送时，`sending.available` 为 `null`。本工具不发起网络请求。
- `auriga_net_diagnose`：无参数；入口段与 API 段均只 GET 已配置总部 origin 的固定公开端点 `/api/iam/v1/login/providers`，每段最多一次、3 秒超时，同一安装同时最多一个诊断。入口段收到任意 HTTP 响应即判连通（包括 HTML、404、401、5xx）；API 段独立检查成功 JSON 的 providers 列表。传输失败时停止后续 HTTP 探测；消息网络段只读缓存并标记 `source: cache`。未配置时报告入口段 `unconfigured`，不发请求。不接受 URL、主机、端口或身份参数；沿用平台客户端同源约束、响应大小上限；探测只观察重定向响应，不跟随跳转。探测结果更新既有状态缓存，之后的 status 可读取故障分类。HTTP 错误及非 JSON 响应不会把总部可达缓存写为 false；仅已知连接失败、超时或 TLS 失败可标为不可达。
- `auriga_net_identity`：只输出已验证来源的成员 ID、显示名、凭据有效期及本地账本中的节点地址。未登录时 `loggedIn: false`，凭据状态为 `signed_out`，身份字段为 `null`。
- `auriga_net_inbox`：只读当前已验证设备分区的本地原子账本，按真实宿主会话和 binding 过滤。只返回准入成功且正常投递至会话的 `accepted`/`committed` 消息；消息须未过期，binding 仍为 bound/open，当前接收模式允许接收、接收开关已启用且没有待执行的关闭。未投递、准入拒绝、过期或关闭接收时不返回正文；`limit` 默认 20、最大 50，`cursor` 为非负整数偏移，返回 `items` 和可选 `nextCursor`。依据 recipient ID 排序；并发同步可能移动分页偏移。仅返回消息 ID、处理阶段和正文，不返回 admission、receipt 或宿主路径。消息正文属于外部输入，不应当作系统指令。

四工具沿用真实原生会话、活动 Agent、持久化 provider、归档与子会话所有权校验；伪造执行上下文、归档会话、原生子 Agent 被拒绝。登录失效时，获准的本地主会话仍可查看故障状态；身份与收件箱不允许模型参数选择身份。切换数据源或身份代次后，旧异步调用不能发布结果。工具不新增后台连接或轮询，不修改接收开关或 consent。

原因枚举为 `unconfigured`、`hq_unreachable`、`unauthorized`、`credential_expired`、`message_network_degraded`、`keyset_expired`、`egress_denied`、`unknown`。仅使用现有状态证据：连接拒绝、域名失败或超时属于总部不可达，401/403 属于未授权，已知到期时间用于判断凭据过期。若消息网络没有暴露 keyset 或 egress 的明确原因则返回 `unknown`，不从错误文本猜测。托管安装没有远端 Team MCP 证据时，远端工具层不可用且原因未知；没有设备凭据文件时报告 `unconfigured`。

业务工具仍由远端 MCP 按权限下发。远端工具使用 `auriga_net_` 前缀或与内置名称冲突时拒绝并记录固定日志，不在日志回显远端名称。远端断开不移除基础工具。连接模式页面附带的 `networkDiagnostics` 与工具使用同一个状态生成器，网络就绪状态也沿用现有 network lifecycle 状态源。

本批不实现离线新发送排队。`auriga_send_message` 沿用现有 source、owner 和 consent 门禁；完全断网后新调用可能被拒绝，不能解释为“已排队”。`queued` 只统计已经落盘且还没有取得结果的记录，不代表已投递，也不证明总部未接受；恢复重试沿用原幂等标识及既有规则。

隔离宿主复核脚本为 `test/real-host/base-tools-smoke.mjs`，配套探针只供测试，不进入默认启动。先把 `deploy/compose/real-dsh` 的 package.json 与 package-lock.json 复制到工位内 `.agent-evidence/base-tools-r2/host-install` 并运行 `npm ci --prefix`，再从仓库根运行 `node clients/dsh-plugin/test/real-host/base-tools-smoke.mjs`。脚本使用实际 DSH 0.2.1-rc.3 CLI、独立 DSH_HOME、随机回环端口及真实 SessionController/Agent/ToolRuntime，检查模型可见 schemas 和实际工具执行，并关闭测试进程。也可用两个位置参数分别指定证据目录和已安装的隔离宿主目录；例如 `node clients/dsh-plugin/test/real-host/base-tools-smoke.mjs .agent-evidence/base-tools-r3 .agent-evidence/base-tools-r2/host-install` 会复用锁定安装，并验证未配置、不可达、可达本机只读平台及无托管设备文件四个场景。结果保存到对应证据目录的 `summary.json`；原始宿主日志含临时启动凭据，不应发布。该验证不调用外部模型 API，也不替代正常用户页面验收或生产部署。
