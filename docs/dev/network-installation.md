# 本地交付 network 安装配置

安装者沿现有 [本机登录契约](../../contracts/plugin/local-login.md) 向 loader row
交付 `config.connect.network={realm,wssUrl,roots}`。默认安装仍无 network，通信保持
unconfigured。本工具只生成公开配置，不登录、不请求远端、不修改 DSH 或安装信任。
工具通过不代表 compose/NAS/Beta 已接好 collab，也不代表真实设备已完成签名 READY。

## 先确认来源

所有输入必须来自本地受信交付目录，目录及祖先不得允许非受信主体修改。材料不提交仓库。
安装者先核对已验证 Access 的平台 HTTPS origin、issuer、已登记 native 设备 clientId、
准确的 team-account/form providerId 和外部完整 `wss://…/net/v1/ws` 入口。
WSS 可用独立主机、端口、自定义路径或 HTTPS IP 部署；必须真实验证其 TLS 身份。
不得关证书验证、手补 Origin，或把 HTTP 部署当成 WSS 已启用。

可信运维通过**真实 HTTPS**，用已验证的 native 设备凭据调用已有
`POST /api/iam/v1/network/register`（JSON body `{}`），将成功响应原样保存为
`register.json`。调用工具的认证须来自受保护文件/凭据存储，不放 argv、URL、shell 历史或日志。
按既有 JWT 验证要求核 tenant/device，再与 IAM 响应 `tenant/device` 核对；把这两个公开
标识填写进 manifest 的 `expectedIdentity`。保存非敏感来源记录：实例、时间、核对人、租户、
设备和 realm，凭据另存。realm 是 IAM 持久分配的规范正 uint64，首 realm 可能就是 `1`。
禁止从 fixture、README、SQL 示例、随机数或数值大小推断 realm。

本地工具只能检查 JSON 形状和内容一致性，**不能密码学证明未签名 register JSON 来自真实
IAM**；`expectedIdentity` 也依赖可信运维核对。结果不得称为“服务器签名注册证明”。已有
权威 tenant→realm 运维记录可用于人工交付；本 helper 则显式要求上述真实响应及身份核对。

准备设备应是同租户的独立设备，不能预先 register 尚无 ledger 的目标新 DSH：预注册会
消耗该目标的 `created=true`，随后默认 provider 收到 `created=false` 而无完整账本会失败关闭。
目标 DSH 自己正常首次注册；已有设备须使用原完整 ledger。不要删账本、重登录或伪造
created 来绕过恢复检查。缺失原账本的恢复仍需另行验收。

离线根运营者交付该 realm 的 Ed25519 **public SPKI DER**、kid、根签完整 keyset 与配套
announcement，按 [密钥运维契约](../../contracts/network/key-operations.md) 保留离线审批。
指纹必须经独立受信渠道确认，再保存为单行 `sha256:` 加 64 位小写十六进制的文件；不能从
首次 keys 响应或同一未核材料中抄一个摘要就视为独立确认。TLS CA、业务签名钥、identity/
runtime 根均不能代替 network 离线根；仅靠公钥字节无法判断它们的运维角色。
根私钥留在离线设施，不提供给导出工具。根更新仍须显式受信安装交付。

gateway 运维另交付当前 `protocol_response` 公钥 SPKI DER/kid，必须匹配同 realm 根签清单
中的 active 条目及实际网关签名配置。工具不读取网关私钥，不能证明部署已装载那把私钥。
根签版本下限 `minimumVersion` 必须取受信部署检查点/外部高水位，不能为通过工具而降低。

## 导出

Node 22.12+，在准备好锁定依赖的仓库根运行。所有 CLI 参数均为文件路径：

```powershell
node scripts/network/export-installation.mjs C:/installation/manifest.json C:/installation/connect.json
node --test scripts/network/export-installation.test.mjs
```

省略第二个参数时输出 JSON 到 stdout；失败 stdout 为空，退出 1，stderr 仅给通用提示，
不回显材料。输出路径必须不存在，已有文件拒绝覆盖。不要用 shell 重定向覆盖既有可信配置。
helper 不联网、不安装根、不生成私钥；trust cache 仅存在于本次调用内存，不修改真实
DSH cache/ledger 或历史高水位。各 JSON 上限 64 KiB，公钥只接规范 44 字节 Ed25519 DER，
拒绝 PEM/PKCS#8；相对材料路径以 manifest 目录解析。空缺/未知字段和重复 JSON 键拒绝。

manifest 形状如下，**全部示例值都必须换成已核真实材料**，不能直接作为现场配置：

```json
{
  "connect": {
    "platformOrigin": "https://portal.example.com",
    "issuer": "https://portal.example.com",
    "clientId": "auriga-dsh",
    "providerId": "team"
  },
  "expectedIdentity": {
    "tenant": "019f0140-0000-7000-8000-000000000001",
    "device": "019f0140-0000-7000-8000-000000000002"
  },
  "registrationFile": "register.json",
  "minimumVersion": "7",
  "roots": [
    {
      "kid": "offline-1",
      "publicKeyFile": "root-public.der",
      "fingerprintFile": "root-fingerprint.txt"
    }
  ],
  "signedKeysetFile": "signed.json",
  "announcementFile": "announcement.json",
  "gateway": { "kid": "gateway-1", "publicKeyFile": "gateway-public.der" },
  "wssUrl": "wss://portal.example.com/net/v1/ws"
}
```

`connect` 沿现有配置允许可选 `deviceName`；这里明确只接受 HTTPS，不接受 allowLocalHttp
或内嵌 network。issuer 必须与平台同 origin。roots 支持 1–16 个唯一 kid，每个指纹独立
核实。实际 realm 从 register 读取，必须与根签清单相同。工具复用插件
`platformConfig/networkInstallation`、strict JSON/SPKI 校验和 `openTrustStore`，验证根签
清单/公告完整一致、24 小时清单上限、当前有效期、最低版本及协议钥用途/材料/active 状态。
导出不延长发布或密钥寿命，不重置客户端历史；离线根发布设施仍须按契约及时续签交付。

输出只有 `{ "connect": { …, "network": { … } } }`，无 tenant/device、令牌、私钥或签名发布。
安装者复核来源记录和导出内容后，才通过既有受信安装渠道交给目标 DSH 管理者。

## 写入准确 loader 位置

先在**全新隔离** `DSH_HOME` 中按 [插件安装说明](dsh-plugin.md) 用原生管理器安装准备好的
目录或 tgz；不要重启用户正在运行的 DSH 或写其真实 profile。DSH 公开 CLI 的
`--profile web --dump-config` 可初始化/检查该隔离 profile。编辑
`<隔离 DSH_HOME>/profiles/web/cordis.patch.yml`，找到包根 `id: auriga-dsh` 行（不是 `/plugin`
子路径），在这一行的 `config.connect` 放入导出的 `connect` 对象。可用下面命令显示
准确的完整行补丁，再人工合入对应行（JSON flow mapping 是有效 YAML）：

```powershell
$installation = Get-Content -Raw -LiteralPath C:/installation/connect.json | ConvertFrom-Json
"- id: auriga-dsh`n  disabled: false`n  config: " + ($installation | ConvertTo-Json -Depth 8 -Compress)
```

保留该行其他已批准设置；不要把文件写进 npm 包里的通用 `cordis.patch.yml`，也不要
把外层 `{connect:…}` 再塞入 `config.connect`。重新 dump 检查实际生效配置，再启动隔离
profile；helper 本身不写这些文件。保存的平台设置优先于 loader 默认值：平台与 loader
同 origin 才沿用其 issuer/provider/network；换平台不沿用旧 pin，也不从远端下载新根。

## 真实验收与失败语义

由主控安排串行槽，在已实际接好独立 collab/keys、gateway、edge TLS/Origin 的目标部署：

1. 保留来源摘要、根指纹、版本/有效期、实际部署与 DSH 版本、隔离 profile 标识；不留凭据。
2. 通过真实 loader 启用默认生产 provider，不注入测试 provider。原生设备登录后，provider
   依次调用已有 register/ticket、固定 HTTPS keys/announcement，再连固定 WSS/callout。
3. 核 IAM 返回与当前 JWT 的 tenant/device、安装 realm 一致；核真实根签 READY。新节点须
   完成首次 C1 bootstrap、完整 ledger 耐久提交、关闭 C1、取新 ticket 后正常 C2 READY，
   只有最终 `connected=true` 才算基础通信就绪。断开后用新 ticket 重连并保持身份/账本。
4. 无 network 保持 unconfigured；错误 realm、错根/签名、过期/不匹配清单不得进入 READY；
   换平台不携旧 pin；TLS CA 不可替代安装根。不能靠跳 TLS 验证或修改 Origin 凑通。

导出失败不应安装任何新配置；原生登录成功与通信就绪分别记录。清单失效或发布不可达时
失败关闭，不伪造 READY、不重置账本。连通仅为 connection-only，不等于双 DSH SEND/收发、
所有部署模式上线或公开设备验收已经完成。
