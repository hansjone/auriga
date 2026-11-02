# N1 签名密钥运维接线

此文件补充既有 `keyset.schema.json`、`protocol.schema.json`，HTTP 见
`../openapi/network-keys.yaml`。不改变业务消息、READY 或邮箱核心。

## 安装信任与离线根

安装/组织接入必须通过受信渠道交付 realm、tenant、离线根 Ed25519 SPKI DER、
`sha256:<SPKI DER 的 64 位小写十六进制 SHA-256>` 指纹，以及固定 HTTPS
`/api/collab/v1/network/keys` URL。根文件及指纹不能取自该 HTTP 响应或消息。
每 realm 使用独立根；根更换通过受信安装流程显式完成，不能由业务密钥解除吊销。
客户端用已安装 roots 调用现有 trust store 的 update(SignedKeyset)。
此端点只发公钥清单，不负责自动下载/替换客户端 roots。

SignedKeyset 域沿用 `UTF-8("Auriga.NetworkKeyset.v1") || 0x00 || JCS(keyset)`。
KeysetAnnouncement 的签名输入固定为
`UTF-8("Auriga.NetworkKeysetAnnouncement.v1") || 0x00 || JCS(announcement 去掉 root_signature)`；
保留 root_kid。manifest_digest = `sha256:` + SHA-256(JCS(KeysetPayload)) 的小写十六进制。
公告沿安装根与所属 realm 校验，不把可能泄露的业务签名当根证明。

在线进程没有根私钥配置。离线执行器入口类为
`auriga.collab.network.keys.OfflineKeySigning`（使用 collab-impl 及其运行依赖 classpath）：

```text
java -cp <runtime-classpath> auriga.collab.network.keys.OfflineKeySigning sign payload.json root-2 root-private.der signed.json announcement.json
java -cp <runtime-classpath> auriga.collab.network.keys.OfflineKeySigning install signed.json announcement.json root-2 root-public.der sha256:<fingerprint> key-state.json
```

payload.json 直接遵守 KeysetPayload，私钥在测试外不能生成/写入源码仓库。
`sign` 的输出必须是新文件；只传密钥文件路径，不在 argv 或普通配置传私钥字节。
`install` 验根签名/期限、持独占 state 锁，再原子替换仅含公钥的业务签名状态；
已有文件版本必须更高，业务签名器运行时拒绝替换。
它是停机安装工具，不能代替 PG 防回退记录或运维审查。

## collab 配置

配置前缀为 `auriga.collab.network.key-operations.`，`enabled` 默认 false。
生产启用后以下全部必填（示例值皆为占位符）：

```properties
auriga.collab.network.key-operations.enabled=true
auriga.collab.network.key-operations.tenant-id=019f0140-0000-7000-8000-000000000001
auriga.collab.network.key-operations.realm=2
auriga.collab.network.key-operations.root-kid=root-2
auriga.collab.network.key-operations.root-public-file=/run/auriga-trust/root-public.der
auriga.collab.network.key-operations.root-fingerprint=sha256:<fingerprint>
auriga.collab.network.key-operations.signed-file=/run/auriga-trust/signed.json
auriga.collab.network.key-operations.announcement-file=/run/auriga-trust/announcement.json
auriga.collab.network.key-operations.minimum-version=1
auriga.collab.network.key-state-file=/var/lib/auriga/keys/key-state.json
auriga.collab.network.key-operations.private-files[business-2]=/run/secrets/business-2.der
auriga.collab.network.key-operations.private-files[protocol-2]=/run/secrets/protocol-2.der
auriga.collab.network.key-operations.private-files[management-2]=/run/secrets/management-2.der
```

独立 collab schema 及 V11 必须已迁移，运行角色非 owner/非 BYPASSRLS。
最低版本须来自受信部署检查点；灾难恢复若连 PG 也回滚，必须按外部已知高水位提高它。
所有 active/next 各有独立 DER PKCS#8 私钥；三个用途都必须有公钥条目；
retired/revoked 不加载私钥。协议响应公钥（含网关 ERROR 使用的公钥）须在同一根签清单里，
网关加载的私钥/用途/kid/有效期须与该条目一致，部署材料由 J7 交付。
某用途全部撤销时可继续发布撤销清单，但该用途没有合法 active 私钥就拒绝签发。
当前业务签名器版本上限为有符号 long 最大值；超出版本拒绝启动，不截断。

私钥只允许 owner 访问；Windows 也允许 SYSTEM/Administrators。文件权限不够、符号链接、
过大 DER、错配、根/业务公钥混用、缺状态或状态与根清单不符均拒绝启动。
公开根/清单/状态及状态父目录禁止非受信主体写入。挂载目录、祖先目录及宿主隔离由部署保证；
此处权限检查不声称阻止宿主管理员或具有同一 OS 身份的 agent。

## 发布、撤销与恢复

1. 先离线审查并根签名完整 KeysetPayload，保留全部历史 kid（含撤销项），不重用材料/用途/有效期。
   初次安装允许 active；其后新钥必须先 next，发布满 24 小时后才 active，
   老 active 进入 retired 时保留至少 5 分钟+60 秒验旧窗口。
2. 完整清单及公告作为一对文件原子交付。30 秒后台检查先验根，再同事务保存 PG
   高版本、首次公布时间及 pending_push。不同步/损坏文件不替换已知有效发布，不续期。
   HTTP 直接返回 PG 的完整根签清单/公告；同版本重取不增加期限。
3. NetworkKeyPush.broadcast(realm, signedKeyset, announcement) 是 J2/J3 的独立传输端口。
   返回表示已耐久接收 fanout；只收到了 Core PUB 不得确认成功。失败每轮重试，
   重启保留待推送；新完整清单可覆盖旧待推送，因为历史撤销不能消失。
   接线方按当前权威 node 路由广播，或附在 HEAD/READY_RESULT/FETCH_RESULT 公告中。
   未配置传输时告警并保留 pending，不能宣称已送达客户端。
4. 清单更新与业务签发锁同一 PG 行。撤销清单提交后，旧本地状态不能再签发；
   即使私钥文件仍在，也不能凭旧状态恢复。原 PG 消息继续 pending，无无签名降级。
   激活/撤销发布后，停机安装同一根签版本的 state 并重启，重新绑定当前业务私钥。
   该保守实现会暂停新签发，未实现业务私钥的无停机热切换。
5. 清单有效期最多 24 小时，离线根发布设施应提前生成/审批下一版本并可靠交付；
   本服务不会持根私钥自动续签。6 小时客户端抖动刷新及接收端硬截止由现有 trust 层/后续 provider 承接。
   更新链路全部失败时只能保证剩余缓存硬截止，不冒称全网 5 分钟撤销。

J7 需配置公开固定 HTTPS 路由、只读 roots/清单挂载、独立私钥 secret、可写受保护 state
及 PG 持久卷/外部防回退检查点。单签名器使用既有文件独占锁；多副本/配置分发 HA
仍需部署设计与真实故障验收。未接 J2/J3/J7 不等于真实 N1 网络已闭环。

## 独立离线过期恢复

普通发布的 next/24h 规则不变。运营者可明确审批一次独立离线恢复，使用原根签名的新完整清单；
不得通过在线 HTTP 开关、修改时钟或伪造首次公布时间实施。`NetworkKeyOperator` 的
`preflight-recovery`（事务回滚）及 `recover-expired`（提交）复用固定计划、原 state 绑定和 PG 独占门禁。

恢复要求 PG 已有完整历史；旧清单及每把旧钥均已过期至少 360 秒，PG/应用时钟误差不超过 60 秒。
新清单版本严格增加，issued_at 不早于全部旧截止加 360 秒。保留全部旧 kid 和原材料、用途、期限，
原 revoked 不能复活，所有旧钥仅为 retired/revoked；新增恰好三个用途各一把独立 active，
not_before 等于新 issued_at 且不晚于当前时刻，not_after 在当前之后且寿命最多 7 天。
新 kid 的 firstPublished 记录真实提交时钟，不回填 24 小时历史。

部署适配器须保留批准人、原因、准确 payload/signed/announcement 摘要和客户端重新获取完整 keyset 的明确确认，
追加恢复 receipt，再安装同版 state/私钥并重启。客户端继续沿原受信根、版本和不可变历史校验；
不删除账本、不重置信任、不放宽过期检查。未刷新客户端保持拒绝，真实刷新及签发验收由运营者安排。
