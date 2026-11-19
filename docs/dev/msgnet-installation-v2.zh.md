# Msgnet v2 安装材料工具

本地工具准备安装材料、调用既有离线根签名器并输出 NAS 安装清单。它不开监听端口，不启动 Spring，不连接 PG，不投影或部署，不重置旧状态。自动续期仍由生产 `MsgnetRenewalConfiguration.Maintenance` 单一循环负责。

源码入口为 `auriga.collab.network.keys.MsgnetInstallationOperator`。先沿正式源码和开发套件构建包含本入口的 platform executable jar，独立核对其来源与 SHA-256。不能用尚未包含该类的旧 jar。根私钥仅供原离线根设施上的 `sign` 阶段读取，不能进入 NAS、源码、镜像、prepare/finalize 目录或日志。

## 固定启动器

```sh
node scripts/network/msgnet-installation.mjs prepare /private/launcher.json /private/prepare.json
node scripts/network/msgnet-installation.mjs sign /private/launcher.json /private/sign.json
node scripts/network/msgnet-installation.mjs finalize /private/launcher.json /private/finalize.json
```

`launcher.json` 精确为：

```json
{
  "version": 1,
  "java": {
    "path": "/opt/approved-jdk/bin/java",
    "sha256": "<已独立核准的64位小写hex>"
  },
  "jar": {
    "path": "/private/platform-exec.jar",
    "sha256": "<已独立核准的64位小写hex>"
  }
}
```

路径须为规范绝对本地路径，文件无链接且不可被组/他人写。使用 JDK 实际二进制路径，不能使用 `/usr/bin/java` 等符号链接。启动器只启动固定主类、固定空 loader 配置和一个已核 jar，预算固定为 heap512MiB、metaspace256MiB、CPU2；拒绝 Java/loader/classpath 环境覆盖，不继承服务凭据环境。Windows 使用同一 Node 命令，JSON 路径按 Windows 规则转义；计划和私钥仍由 Java 的原 Windows ACL 检查器核验。启动器不安装工具或依赖。

所有计划/源审批记录采用 owner-only 权限（POSIX0600），所在私有目录0700。JSON 必须无重复字段、未知字段或类型变化。`{path,sha256}` 引用中的摘要为**实际文件字节**的64位小写hex，不是 JSON 语义摘要；引用文件必须本地、非链接、大小有界。输出目录必须不存在；失败后保留 partial，换全新目录调查，不能覆盖重跑。

## 新 realm：prepare

`prepare.json` 精确字段如下；`nodeControlPins` 可省略，其余必填：

```json
{
  "version": 1,
  "mode": "fresh",
  "tenantId": "<原受信tenant UUID>",
  "realm": "<正数十进制字符串>",
  "rootKid": "<原根kid>",
  "rootFingerprint": "sha256:<原独立确认的根SPKI摘要>",
  "rootPublic": {
    "path": "/private/original-root.der",
    "sha256": "<文件摘要>"
  },
  "approvedSource": {
    "path": "/private/source-approval.json",
    "sha256": "<文件摘要>"
  },
  "identities": {
    "path": "/private/enrolled-identities.json",
    "sha256": "<文件摘要>"
  },
  "nodeControlPins": {
    "path": "/private/enrolled-node-pins.json",
    "sha256": "<文件摘要>"
  },
  "authorityId": "<唯一运行授权ID>",
  "authorityRevision": "1",
  "runtimeKid": "<唯一运行kid>",
  "kekId": "<独立KEK标识>",
  "controlRevision": "1",
  "minimumGeneration": "1",
  "minimumKeysetVersion": "<原批准的起始版本>",
  "notBefore": "2026-11-19T00:00:00Z",
  "limits": {
    "keyset_seconds": 86400,
    "business_key_seconds": 691200,
    "restart_key_seconds": 604800,
    "next_maturity_seconds": 86400,
    "overlap_seconds": 360,
    "archive_grace_seconds": 360
  },
  "transportScope": [
    {
      "component": "network",
      "identity_id": "<已登记identity>",
      "sans": ["network"],
      "ekus": ["serverAuth", "clientAuth"]
    }
  ],
  "projectorIdentityId": "<已登记的network identity>",
  "outputDirectory": "/private/new-prepared"
}
```

`source-approval.json` 是受信安装操作人的运维事实记录，**不是新信任根或替代根签名**。精确字段为 `version:1`、`mode:"fresh"`、相同 `tenantId/realm/rootKid/rootFingerprint`、`identitiesSha256`、`approvedBy`、`approval`；有节点 pins 时另含 `nodeControlPinsSha256`，与输入文件摘要相同。操作人从独立安装记录核实这些事实，工具不能从公开 root.der 推造根私钥、从不可信响应自选根，或把旧 v1 记录改称 fresh。

`identities` 原样采用契约 `InstallationPins`：`schema_version:2, tenant_id, realm, pins`。每个 pin 仅原七字段 `target_id,identity_id,signing_spki,component,gateway_id,sealing_spki,subject`，分别绑定规范 Ed25519/X25519 SPKI、组件、网关和 RFC2253 TLS subject。身份只能来自受信登记通道；工具不代注册或生成组件私钥。可选 `nodeControlPins` 使用原 `NodeControlPins` schema，独立于投影身份，不能复用 identity/target。transportScope 必须对应实际登记身份/组件，不能为陌生身份扩权。

prepare 使用现 `MsgnetSchema`、`MsgnetControlLedger.pins`、`MsgnetNodePins.read`、默认生产 `RenewalConfig` 和 `MsgnetPolicy.authorizedConfig` 检查，而非另立验签策略。运行授权固定已有六能力，`no_hard_expiry`、365天复核和原 limits/transport_scope 契约不改。

输出包括 `runtime-private.der`（新独立 Ed25519 PKCS#8）、`kek.bin`（32字节随机KEK）、原公开 pins/根、`request.json` 和只供本机后续使用的 `prepared.json`。所有文件0600、目录0700。**只把公开 request.json 交给原离线根设施**；不用把整个 prepared 目录或运行私钥/KEK送去签名。stdout 只给状态、收据路径和摘要。

## 离线根签与 finalize

`sign.json` 精确为：

```json
{
  "version": 1,
  "request": {
    "path": "/offline/public-request/request.json",
    "sha256": "<准确文件摘要>"
  },
  "rootPrivateFile": "/offline-root/original-root-private.der",
  "outputDirectory": "/offline/new-signed"
}
```

根设施先独立核准 request 的来源、原根指纹、tenant/realm、授权与 scope。工具仅薄调用现 `MsgnetOfflineRoot.sign`：先签 authority，再把**已签 authority 的真实 JCS digest**写入原 RootControl 的 allowed_authorities 并签 control；最后用原 `MsgnetV2Verifier.verifyAuthority` 证明根私钥匹配独立公钥。输出仅 `authority.json`、`control.json`；根私钥必须在 request 目录和新输出目录之外。

将两个已签公开文件带回准备端，再运行 finalize。其计划精确为：

```json
{
  "version": 1,
  "prepared": {
    "path": "/private/new-prepared/prepared.json",
    "sha256": "<准确文件摘要>"
  },
  "authority": {
    "path": "/private/signed/authority.json",
    "sha256": "<准确文件摘要>"
  },
  "control": {
    "path": "/private/signed/control.json",
    "sha256": "<准确文件摘要>"
  },
  "outputDirectory": "/private/new-installation"
}
```

finalize 重核源审批/原根/pins/全部引用摘要、签名、payload与 request 的精确对应、runtime公私配对、32字节KEK及原配置政策。只输出固定相邻材料：`authority.json, control.json, runtime-private.der, kek.bin, identities.json`，有节点pins再加 `node-control-pins.json`；另有 `installation.json` 和 `installation.sha256`。

NAS-only spec 为安装文件格式 version1：`version,tenantId,realm,rootKid,rootFingerprint,kekId,projectorIdentityId`，以及 `authority/control/runtimePrivate/kek/identities` 五个 `{path,sha256}`，可选 `nodeControlPins` 同形。各 path 是上述固定相邻叶名；只按 spec 目录解析，不含绝对路径、子目录或 `..`。整份 spec 另核独立 SHA。它不含密码/token/私钥正文或 READY 证明。

NAS 唯一安装器必须从**原 checked collab 安装源**再次比 tenant/realm/rootKid/fingerprint，不能从 spec/authority 自选新根。全新 NAS realm 若尚未登记该根 DER 和原安装事实，上游受信根登记仍是独立前置；本工具不生成 v1 假签包过渡，根登记缺失要明确 required。向实际 collab UID10002 投影 secret 时使用其独占可读0600，不能以0640组读规避原 `ProtectedKeyFiles` 检查。

工具 `configured` 仅表示文件闭包校验通过。首次生产调用仍由原 `MsgnetRenewal.install → KEK入库 → reconcile` 判定；PG已有v1 row会拒绝普通首装，不能用 fresh 清单绕过迁移。

## 已有 v1：显式迁移计划包装

```sh
node scripts/network/msgnet-installation.mjs migration-plan /private/launcher.json /private/legacy-wrapper.json
```

本批**没有自动 queue/material commitment producer**。不能假称 legacy 迁移闭包已经完成；下列原根已批准的完整材料必须由原运维通道提供：

- wrapper 精确字段 `version:1, tenantId, realm, rootKid, rootFingerprint, configFile, rootPublic, permit, ancestor, queueCommitment, members, outputDirectory`。除成员数组与普通字符串外，configFile/rootPublic/permit/ancestor/queueCommitment 均为绝对 `{path,sha256}` 引用。
- permit 是原 `SignedMigrationPermit`；ancestor 是原公开 `{signed_keyset,announcement}` 数组；queueCommitment 是已批准、严格按版本升序的 `{version,pair_digest,material_digest}` 数组，摘要对应 permit。工具不从假PG事实生成它。
- 每个 member 精确 `signed,announcement,privateFiles`，前两者为引用，privateFiles 为 **全部 kid**→引用映射，包括历史/预签候选要求的全部材料；只给三个当前 active 不够。配置文件须为原 operator 完整受保护 properties（含真实PG配置、原根、authority/control/KEK及runtime文件引用），不会输出其内容。

包装复用原归档根验签、`verifyMigrationPermit`、schema、私钥匹配，核精确成员/版本/pair摘要/队列最大版与v2最小代际；只生成 `MsgnetMigrationOperator migrate` 原精确五字段计划 `config_file,permit_file,runtime_private_file,ancestor_file,members`。**material commitment 重算、真实PG current pair、6h剩余、≤600秒freeze、first_published归档及 legacy_disabled 屏障全部留在原 `MsgnetMigration.migrate`，包装成功不代替这些门禁。** 原根 signer 也已有 migration 固定域入口，需签新的许可时沿原 `MsgnetOfflineRoot` 操作，不复制迁移算法。

已过期 v1 不能直接迁入。先按 [原独立到期恢复流程](nas-network-renewal.md) prepare-recovery、原根签名、recover-expired/install，保原PG/根/realm/客户端高水位；恢复入口还要求旧清单及所有历史钥均过期满360秒、PG/app时差≤60秒，条件不满足则走原正常队列/next成熟恢复路径。恢复后的真实当前版和仍可能生效的原预签队列须重新纳入许可；v2首版高于全部承诺v1版本。不得重置PG或另建realm规避旧记录。

首次真实恢复/迁移与12h续期观察仍由主控串行执行，并核正常页面、原N projector和真实reload/控制回执。本文工具本身不会停机、提交PG或自动签实际部署材料。
