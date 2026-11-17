# 开发环境的独立降级根与云端更新

统一资源的自动回落使用独立 `purpose=downgrade` 根。发行、撤销、降级和 runtime 镜像发行各有独立信任用途。本指南用于已有开发栈的增量配置，执行者为部署主控。开始前记录源码与配方版本、当前模板及序号高水位、目标实例和资源规则，并备份两端配置。现场收据与正常页面结果由主控提供。

## 准备新根

在可信机器或无网络的一次性密钥准备容器中运行生成器。父目录须预先存在且受保护，输出目录自身须不存在，祖先路径须逐层可检查并避开符号链接。Windows 路径还须使用真实规范路径的大小写。

```sh
node scripts/resource-keys/generate.mjs \
  --out /srv/auriga-secrets/new-development-roots \
  --kid-prefix development-resource-unique-batch
```

生成器创建三把彼此独立的 Ed25519 根。已有环境本次引入 `offline/downgrade.pk8` 和 `resource-roots.json` 中的 `downgrade` 公钥。原 release/revocation 配置与密钥继续承担原用途。私钥仅存放于受保护秘密目录，报告记录 kid 和公钥指纹。目录前置条件与保护实现见[密钥生成器](../../scripts/resource-keys/generate.mjs)。

## 先部署节点公钥和受信下载入口

### 信任文件与有效窗

节点读取 `<AURIGA_AGENT_DATA_ROOT>/agent/resource-trust.json`。NAS 配方将 `AURIGA_AGENT_DATA_ROOT` 设为 `${AURIGA_DATA_ROOT}/data/agent`，因此宿主文件为 `${AURIGA_DATA_ROOT}/data/agent/agent/resource-trust.json`。

保留已有 `schemaVersion=1`、经核准的 `origin` 和 `keys`，追加本批降级公钥。以下日期仅演示 UTC 格式，实际有效窗由主控批准。

```json
{
  "kid": "development-resource-unique-batch-downgrade",
  "purpose": "downgrade",
  "publicKey": "<Ed25519 raw32 的标准 base64>",
  "notBefore": "2026-11-18T00:00:00Z",
  "notAfter": "2027-11-18T00:00:00Z"
}
```

生成器提供 SPKI DER，节点字段要求 raw32。使用 Node 标准密码 API 转换并核对长度。

```js
import { createPublicKey } from 'node:crypto';

const spki = Buffer.from(roots.downgrade.publicKeySpkiBase64, 'base64');
const x = createPublicKey({ key: spki, format: 'der', type: 'spki' }).export({
  format: 'jwk',
}).x;
const raw = Buffer.from(x, 'base64url');
if (raw.length !== 32) throw new Error('Invalid Ed25519 public key');
const publicKey = raw.toString('base64');
```

节点仅在 `notBefore ≤ 当前时间 < notAfter` 时接受该根。记录 kid、raw32 公钥 SHA-256、SPKI SHA-256、有效窗和最终信任文件 SHA-256。原来没有信任文件时，须通过独立受信部署材料补齐现用 release/revocation 公钥，并核对不同用途的密钥独立性。[节点加载器](../../infra/agent/internal/runtime/unified_wiring.go)、[签名和时间验证](../../infra/agent/internal/runtime/unified_protocol.go)实现了这些边界。

公开 NAS `init.sh`、`render.sh` 当前未生成此信任文件。主控须为受保护文件提供经审查的安装过程，记录安装脚本及输入的摘要、旧文件是否存在、备份位置与原属主权限。在同目录写临时普通文件、核验内容和权限后原子替换，再重建或重启节点代理。NAS 节点以 root 运行，可采用 root 所有的 `0600` 信任文件和受保护父目录。代理启动时间及文件摘要须对应本次安装，加载成功还须通过下面的实际下载与应用验证。

### mTLS 地址、端口和实际下载

NAS 节点使用宿主网络。平台资源下载入口位于 HTTPS `8081` 监听器，宿主发布端口为 `AURIGA_HOST_PUBLISH_PLATFORM_HTTP`。NodeControl 使用独立的 `7443` 监听器和 `AURIGA_HOST_PUBLISH_NODECONTROL` 发布端口。平台 HTTPS 证书的 DNS SAN 为 `platform`。主控须核实实际证书链、监听端口和发布映射。[NAS 配方](../../deploy/nas/docker-compose.yml)、[平台 TLS 配置](../../services/platform-app/src/main/resources/application-combined.yaml)、[PKI 生成](../../deploy/nas/bootstrap/main.go)包含对应事实。

在这套证书与宿主回环发布方式下，下载 `origin` 的候选形式为 `https://platform:<AURIGA_HOST_PUBLISH_PLATFORM_HTTP>`。实际值须经核准。节点容器中的 `platform` 必须稳定解析到该宿主发布入口。在这个 host-network 布局中，主控可在受审固定配方的 `agent.extra_hosts` 接入 `platform:127.0.0.1`，重建后核对节点容器内解析结果和下载端口。宿主网络节点的可达性须从节点自身验证。当前公开配方缺少下载专用的 `platform` 解析接线，主控须补齐并在重建后复验。

节点使用自身已登记的 `node.crt`、`node.key` 和 `ca.crt` 建立 TLS 1.3 mTLS，服务端验证节点身份、租户、调度绑定及下载授权。TLS 名称验证必须开启。节点拒绝重定向，只接受授权 locator 的 HTTP `200` 完整响应、可解析的 `Date` 以及预算内字节。[下载控制器](../../services/resource/impl/src/main/java/auriga/resource/web/CatalogNodeController.java)、[节点下载实现](../../infra/agent/internal/runtime/unified_manager.go)定义了该入口。

主控先在原安装实例的实际 agent 容器中确认 `openssl version`，使用已有 OpenSSL 做传输探测。下面的命令也在该容器内执行，使用已挂载的节点 CA、证书和私钥。宿主先核对 `AURIGA_HOST_PUBLISH_PLATFORM_HTTP`，将实际值填入 `DOWNLOAD_PORT`。示例 `18081` 来自公开配方的默认值。命令以 `platform` 发送 SNI 并校验服务名，TCP 直接连接已核准的宿主回环发布入口。

```sh
AGENT_DIR="${AURIGA_AGENT_DATA_ROOT}/agent"
DOWNLOAD_PORT=18081
PROBE_PATH=/internal/resource/v1/catalog/downloads/00000000-0000-7000-8000-000000000000/manifest
printf 'GET %s HTTP/1.1\r\nHost: platform:%s\r\nConnection: close\r\n\r\n' \
  "$PROBE_PATH" "$DOWNLOAD_PORT" |
  openssl s_client -quiet -tls1_3 \
    -connect "127.0.0.1:$DOWNLOAD_PORT" -servername platform \
    -verify_hostname platform -verify_return_error \
    -CAfile "$AGENT_DIR/ca.crt" \
    -cert "$AGENT_DIR/node.crt" -key "$AGENT_DIR/node.key"
```

这是合成无效 grant 探测。主控记录 OpenSSL 版本、退出码、证书链与服务名校验结果及 HTTP 响应。校验通过且返回 `404` 只证明该路径的受信传输可达。此命令直接指定 TCP 地址，实际 Go 客户端仍按 `origin` 的主机名解析，主控须另行确认重建后的 agent 能解析 `platform`。执行环境与节点客户端见[agent 镜像配方](../../deploy/nas/docker/Dockerfile.agent)、[节点 mTLS 接线](../../infra/agent/internal/runtime/unified_wiring.go)。

真实下载另作验收。启用降级签发前，使用已有发行/撤销根发布一个全新测试版本，向隔离实例建立真实分发，取得该节点当前规格中实际授权的 `manifestDownloadPath` 和各制品 `downloadPath`，由实际节点执行完整下载。通过条件包括清单及全部制品均为 `200`、响应 `Date` 可解析、无 `Content-Range`、原字节 SHA-256 与已验签引用一致，以及节点成功验签并应用这个未缓存的新版本。保留节点来源的精确安装回报和宿主实际加载证据。代理存活、容器 healthy、TLS 握手或合成 grant 的 `404` 均不足以满足完整下载判据。下载失败时先修复接线，再进入签发启用步骤。

## 再启用平台签发

### 当前 NAS 的持久接线缺口

Java 平台已读取 `auriga.resource.downgrade-kid` 与 `auriga.resource.downgrade-key-file`。基础 NAS Compose 当前缺少 `/run/secrets/auriga-resource` 挂载和这两个属性的透传。一键路径的固定 `oneclick-compose.yml` 将 `${AURIGA_DATA_ROOT}/resource-keys/platform` 只读挂载到 `/run/auriga-resource`，其中仅绑定 release/revocation 的 kid 和文件属性。`render.sh` 会重新生成 `config/platform.env`，`run_compose` 显式选择固定配方与受控覆盖层。

因此，通用公开 NAS 配方目前需要主控补齐持久接线。最小接线涉及固定 Compose 的秘密目录挂载与配置入口，以及所有渲染/启动/升级路径的一致消费。主控须提交受审配方或提供已有开发配方的精确版本和收据，确认 `start.sh`、`runtime-upgrade.sh` 后仍生效，再继续本节。直接修改生成的 `config/platform.env` 会被后续渲染覆盖。[固定 Compose 选择](../../deploy/nas/scripts/lib.sh)、[平台环境生成](../../deploy/nas/scripts/render.sh)、[一键覆盖层](../../deploy/nas/oneclick-compose.yml)可用于核对缺口。

### 配方接线完成后的启用与验证

确认节点公钥、mTLS 下载和实际应用通过后，由主控将新私钥安装到平台专用秘密目录。NAS 平台运行身份为 `10001:10001`，密钥的直接父目录须归该用户所有且精确为 `0700`。私钥也须归该用户所有，采用 `0600` 或 `0400`。路径与祖先使用普通真实路径，加载器逐层拒绝符号链接，并核对规范路径。目录整体只读挂载，保持容器内直接父目录的权限。[平台配置装配](../../services/resource/impl/src/main/java/auriga/resource/config/ResourceRuntimeConfiguration.java)、[密钥加载器](../../services/resource/impl/src/main/java/auriga/resource/app/ResourceSigning.java)执行这些检查。

若主控核准的固定配方使用 `/run/secrets/auriga-resource`，其平台配置应包含以下属性。

```properties
auriga.resource.downgrade-kid=development-resource-unique-batch-downgrade
auriga.resource.downgrade-key-file=/run/secrets/auriga-resource/downgrade.pk8
```

这两项由现有 Spring 配置机制读取。采用独立 `resource.properties` 时，固定配方须只读挂载其目录，并将该文件追加到既有 `SPRING_CONFIG_IMPORT` 列表，同时保留原有安装、网络及 UAC 配置导入。主控也可在固定 Compose 的平台 `environment` 中显式设置 `AURIGA_RESOURCE_DOWNGRADE_KID`、`AURIGA_RESOURCE_DOWNGRADE_KEY_FILE`，文件路径须与真实只读挂载一致。以上是配方接线要求，当前通用脚本尚未提供对应的完整部署入口。

### 属性变更后的平台显式重建

固定只读挂载中的 `resource.properties` 内容改变后，Compose 声明可能保持相同。普通 `start.sh` 执行 `up -d`，仅显式重启 `agent`，平台旧进程可能继续保留原属性。启用、轮换、恢复和无降级根负例都须由主控在属性落盘后显式重建 `platform`，再确认新进程加载结果。

主控持有 NAS 部署锁，预先将 `NAS_ROOT` 设为原安装实例实际采用的受审配方目录，将 `AURIGA_DATA_ROOT` 设为原数据根。下面使用该配方已有的 `lib.sh` 和 `run_compose`，保留固定项目、原安装模式及受控覆盖层的选择。

```sh
: "${NAS_ROOT:?set the original installed recipe directory}"
: "${AURIGA_DATA_ROOT:?set the original instance data directory}"
. "$NAS_ROOT/scripts/lib.sh"
load_stack_env
before_id=$(run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" ps --all --quiet platform)
test -n "$before_id"
docker inspect --format '{{.Id}}|{{.State.StartedAt}}|{{.State.Status}}' "$before_id"
run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" up -d \
  --no-deps --force-recreate --no-build --pull never \
  --wait --wait-timeout 120 platform
after_id=$(run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" ps --all --quiet platform)
test -n "$after_id"
test "$after_id" != "$before_id"
docker inspect --format '{{.Id}}|{{.State.StartedAt}}|{{.State.Health.Status}}' "$after_id"
```

记录重建前后的容器 ID、`StartedAt`、退出码、健康状态及属性文件摘要。核对新容器实际配置导入、挂载目标及 `RW=false`、父目录与私钥的 UID/GID/权限、kid 与公钥对应关系。新进程健康后，通过下文的签名授权及节点消费核实新 kid 生效，停用签发时核实回落失败且安全停用继续执行。证据仅保留所需非秘密字段。随后执行 `start.sh` 或标准 runtime 升级时复核这些项目，属性再次变化则重复显式重建步骤。[普通启动流程](../../deploy/nas/scripts/start.sh)、[固定 Compose 入口](../../deploy/nas/scripts/lib.sh)、[启动时配置装配](../../services/resource/impl/src/main/java/auriga/resource/config/ResourceRuntimeConfiguration.java)支持上述操作。

平台签发端独立读取密钥，未读取节点根的有效窗。主控须在到期前复核窗口与节点时钟。在窗口失效或根撤回时，先停止降级签发并按上述步骤显式重建平台，再按受审信任更新流程处理节点文件。降级根的撤回通过受信配置移除该根，资源撤销清单的 `revokedKids` 按发行签名用途检查。获批轮换使用独立新密钥和新 kid，先分发有效公钥、验证节点加载与受信下载，再切换平台属性、显式重建并核实新进程加载，随后完成新 kid 授权消费实测。旧根退役须覆盖仍有效授权和全部目标节点，历史授权消费记录与高水位持续保留。

## 完整 runtime 与已有实例切换

使用 `deploy/compose/scripts/build-real-dsh.mjs` 和完整 real-dsh 配方，保留锁定版本及基础镜像摘要，完成隔离制品检查。按 runtime 发行流程取得真实 registry manifest/config，沿原受信 runtime 根签发递增序号，由主控运行 `deploy/nas/scripts/runtime-upgrade.sh` 激活。记录正式 manifest 摘要、ConfigID、发行序号、签名正文摘要和激活回执。[runtime 发行流程](runtime-release.md)、[NAS 激活脚本](../../deploy/nas/scripts/runtime-upgrade.sh)给出具体入口。

runtime 脚本提交模板后，管理员须在点启动或重启之前，将目标模板写入实例规格。先读取 `GET /api/workspace/v1/admin/runtimes` 确认目标版本已加载，再选择以下现有管理员操作。

- 批量更新跟随默认的实例，调用 `PUT /api/workspace/v1/admin/runtimes/default`，JSON 正文为 `{"version":"<已加载目标版本>"}`。服务对 `pinned_version IS NULL` 的已有实例应用该模板，有变化时递增 generation。即使版本字符串相同，也须确认模板中的正式镜像与发行材料已更新。
- 更新指定实例，先 `GET /api/workspace/v1/instances/{instanceId}` 取得当前 generation，再 `PATCH /api/workspace/v1/admin/instances/{instanceId}`。请求使用 `Content-Type: application/merge-patch+json` 和 `If-Match: "<当前generation>"`，正文为 `{"dshVersionPin":"<已加载目标版本>"}`。采用 `{"dshVersionPin":null}` 时按当前默认模板更新并转为跟随默认。核对返回的目标版本、generation 和待重启状态。

这两项管理员动作负责重新选择模板。实例页“重启”及 `POST /api/workspace/v1/instances/{instanceId}/restart` 使用当前规格排队重启。规格确认后，对已停止实例点启动，对运行实例选择重启，并等待节点观察收敛。验收须同时核对实际实例容器的目标镜像 ConfigID、规格内正式镜像摘要/序号、`observedGeneration == generation` 和正常实例页面。[Workspace 管理逻辑](../../services/workspace/impl/src/main/java/auriga/workspace/app/WorkspaceAdmin.java)、[启停实现](../../services/workspace/impl/src/main/java/auriga/workspace/app/WorkspaceService.java)、[公开接口契约](../../contracts/openapi/workspace.yaml)支持上述顺序。

## 配置恢复与 runtime 提交边界

### 两端配置恢复

主控先通过同一持久配置入口停止降级签发，撤回 `downgrade-kid` 和 `downgrade-key-file` 两项，按[显式重建步骤](#属性变更后的平台显式重建)重建平台，核实新进程已停止降级签发且发行/撤销签发仍可用。等待已有短期降级授权结束，或明确接受受影响安装失败关闭后，再恢复节点信任备份、重启代理并复验。原来没有信任文件时，主控先停止该批共享分发并确认受影响内容已停用，再撤回本次新增信任文件、重启代理，核验文件缺失时共享激活关闭的影响。私钥备份按原秘密保护方式保存。

恢复节点信任时保留该节点之后已批准的其他根变更，核对旧根有效窗。节点 ledger、`UsedAuthorizations`、最高高水位以及已安装资源的授权绑定持续保留。先撤回签发再恢复节点信任，避免平台继续发出节点已经撤回信任的新授权。[节点信任加载](../../infra/agent/internal/runtime/unified_wiring.go)、[防重放协议](../../contracts/resource/unified-protocol.md)说明了恢复边界。

### runtime 提交前后

`runtime-upgrade.sh` 在 stage 后进入待提交状态。Compose 启动失败时，其退出处理尝试 rollback 到前指针并恢复控制消费者，发行序号高水位仍保留。主控须核对退出码、日志、`active.json`、原指针和控制服务实际状态。进程中断后的待提交候选须沿同一公开材料和精确镜像对账恢复。

提交完成的标志为 `Pending=false`，此时显式 rollback 会返回 `only pending activation can roll back`。如果提交后实例或页面验收失败，主控应保留故障证据，准备经批准的修复镜像与真实 proof，沿原根签发更高发行序号，再走标准激活、Workspace 模板切换和实例重启流程。模板和运行时恢复均保留各自的序号高水位、已消费授权 ID 及安装历史。[runtime 指针与序号实现](../../deploy/nas/bootstrap/runtime_upgrade_linux.go)定义了提交后边界。

## 自动回落的现场验收

由主控使用隔离测试实例和专用测试资源执行无根、有根两组用例，分别保存独立证据。两组用例均须在撤销前完成下面步骤1–3的共同前提。版本、摘要及序号均来自真实签名发行。

1. 审核并发布同一包的低版本 L 与高版本 H，确保 L 已发布且其清单和制品摘要均未被恶意撤销。安装并实际加载 H，取得节点来源的 `applied`。记录 H 的实际名称、版本、来源 entryId、manifest/artifact 摘要、releaseSequence、installationRevision、selectionRevision 和 generation，确认 observedGeneration 等于 generation，并保留最高高水位。
2. H 实际生效后，另行审核并发布第三个独立制品的默认版本 D，例如 L/H/D 分别采用 `1.0.0`、`1.2.0`、`1.3.0`。三个版本使用可明确区分的清单与制品摘要。发布 D 会将包的 `defaultVersionId` 设为 D 并保持 `enabled=true`。保留 D 的已发布、未撤销和默认版本回执，核实实例仍实际加载 H。
3. 在目标实例作用域设置指向 L 精确 identity 的有效 `required` 或 `default` 规则，核对 scope、targetId、规则修订和安装方式。撤销前再次确认 H 仍在实例实际生效、包默认仍为独立已发布的 D、包仍 `enabled=true`，以及 L 对该实例主人仍可见、可选并符合规则分发准入。`optional` 规则不满足自动回落条件。此顺序参照 `CatalogWorkflowTest.mandatoryRulesRecoverAfterUnavailableHighVersion`，其先安装高版本，再额外发布默认版本，设置低版本规则后执行撤销。
4. 对 H 执行“恶意撤销”，观察已签撤销事实及规则对账，复核包仍启用、默认仍为 D、L 仍满足候选条件。撤销当前默认版本会清空包默认并禁用整包，导致低版本失去可用性。任一默认保活前提失败时应修复测试准备后重做该组。普通下架 `withdrawn`、卸载和仅降低规则版本均不自动签发此类授权。恶意制品摘要复用会使相关版本一并被撤销，因此 D 和 L 都须与 H 的制品独立。
5. 无降级根的负例中，主控在该隔离环境的持久配置中同时停用两项降级签发配置，按显式重建步骤确认新平台进程已加载停用配置，再完成共同前提和撤销。通过条件为 H 实际停用并有节点报告，回落保持可重试失败，L 未被当作已授权安装。记录正常页面、宿主加载状态及失败原因，并确认包仍启用、默认 D 和候选 L 仍可用，排除整包禁用导致的失败。
6. 有降级根的用例中，主控先恢复经核准的签发属性、显式重建平台并核实新进程加载，再按相同的三版本共同前提让 H 实际生效并执行恶意撤销。确认 `UnifiedResourceRef.downgradeAuthorization` 包含 `purpose=downgrade`、`reason=revocation`、匹配的 ownerId、instanceId、L 的精确 identity、from/toReleaseSequence，以及不超过10分钟的有效期，签名 kid 为已分发的独立降级根。对负例中保留的合法回落重试时，也须重新核对包仍启用、默认 D 与候选 L 可用，并记录与原撤销的关联。
7. 成功判据为低版本已由真实宿主加载，节点安装回报四元组 identity/installationRevision/selectionRevision/generation 与当前期望完全匹配，observedGeneration 等于 generation，并确认来源为节点。高版本停用、低版本 `applied`、正常页面和实例运行状态须有一致证据。`required` 的关闭/卸载在统一目录继续被拒绝，`default` 可从资源市场卸载，原生入口对受管插件的关闭/卸载须拒绝且实例继续运行。
8. 对同一安装修订进行重试及节点重启，核对 `node-state.json` 中安装绑定的 `AuthorizationID`、`UsedAuthorizations` 与 `Watermarks`。同一已绑定安装可延续，已消费 ID 仅保留一份且最高高水位仍为原高版本。服务续签可刷新同一授权的时间和签名，并保留 authorizationId。通过受控验证检查已消费授权被用于新 installationRevision 时会被拒绝，或使用另一个经核验的新授权完成合法变更。保存逐次修订与消费记录，确认同一授权没有被用于新修订重装。

包默认与撤销行为见[目录状态机](../../services/resource/impl/src/main/java/auriga/resource/app/CatalogWorkflow.java)，准备顺序见[现有回落测试](../../services/resource/impl/src/test/java/auriga/resource/app/CatalogWorkflowTest.java)。自动签发条件见[资源分发实现](../../services/resource/impl/src/main/java/auriga/resource/app/CatalogDelivery.java)。节点绑定和防重放见[授权消费](../../infra/agent/internal/runtime/unified_history.go)、[状态持久化](../../infra/agent/internal/runtime/unified_manager.go)及[统一协议](../../contracts/resource/unified-protocol.md)。

完整验收还包括插件 required/default/optional 分发和真实 applied、技能文件夹上传/审核/发布/分发及云端与桌面可见，以及 `allowPersonalPlugins=false` 时启动前清理命令行安装的个人插件。每项记录实际操作、截图、接口回执、宿主观察与未完成项。现场证据补齐后，主控按精确部署版本确认该批次的用户可见结果。
