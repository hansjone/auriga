# Dedicated Node 资源装配与观察

【待审查】Node 已接专用资源物化、同镜像一次性 profile 准备、常驻启动与受控重启 caller。本工位只运行合成 Engine/权威及真实 rc.3 SDK 的纯组合，未启动真实 DSH、PG 或 NAS。部署仍须主控串行核完整镜像、Engine 挂载能力与正常市场页面。

只有 W 当前唯一完整 `dedicated` binding 可消费本人 optional plugin/skill；同一物理池不混装 shared 或第二个成员。Node 每周期从原 `GetSharedDshResourceSnapshot` 重新读取各 placement，binding 和 authorization digest 不变也会协调选装变化。源不可用立即撤正常入口，不能沿旧缓存发新的 ready。

资源仍经既有 Node mTLS GET 下载桥、原签名/撤销/租约/版本高水位与安装修订校验。共享仍只接受受控企业 skill，不能借此接 optional 插件或私有 skill。DED 验证实际 manifest/payload SHA 和字节数；skill 文本名称及描述须与签名 manifest 一致。已验缓存按摘要只读挂到 `/run/auriga/dedicated-resources`，不把 Node 私有 state 或服务凭据挂给 Host。

Node 先耐久写入防回滚状态与 pending，再原子写入 `<pool>/control/host-relay/dedicated-resources.json` 并读回，最后提交事务。该目录映射为 Host 的 `<pool>/control`；意图精确为 `{schemaVersion,view,resources}`，完整 view 使用原公开安全整数/模式，资源列表只含稳定接口的五个字段。摘要按 Host 的 canonical 规范计算。失败保留 pending/原意图和高水位，不覆盖变坏的已暴露缓存；未完成事务不提供 ready 依据。

下载/解包不等于加载。Node 对同一签名 Engine execution、container ID、PID/启动时间与固定实际端点做前后回验，再 GET `/_auriga/dedicated-resources`。只有完整 binding/view/intentDigest、有效 Host 进程 UUID、`ready/verified` 和真实 `/_auriga/dedicated-profile` 四 cap 全部匹配，才报告当前 view 的 `SharedDshResourceReport ready/verified` 并发布入口。`applying/failed/restart-required`、旧回执、错绑定、额外 capability 或失联均保持不就绪；必要重启仍须完成专用受控排空/启动闭环，不直接假报成功。

W 经 Edge 的专用 profile GET 仍沿原 Edge mTLS 信任。Node 前后查 W 当前 mode，仅代理对应固定 shared/dedicated profile 路径，DED 回包严格核四外键和四能力。正常原生 dedicated HTTP 使用当前 E/W 与单次 JTI；Cookie/Authorization 不转给 Host，`/api`、`/connect`、转义别名和 Upgrade 均拒绝。不能从旧 owner 票或共享 profile 推断 dedicated 能力。

纯物化与回执测试可在 `infra/agent` 运行：

```sh
GOTOOLCHAIN=go1.27.3 GOMAXPROCS=2 GOMEMLIMIT=512MiB go test -p 1 ./internal/runtime -run '^TestDedicated|^TestPersistentDedicated' -count=1
go test -p 1 ./internal/ingress -run 'Dedicated|SharedBrowser|SharedInitialProfile' -count=1
```

跨语言纯夹具先令 `AURIGA_DEDICATED_FIXTURE_OUTPUT` 为本工位一个新绝对输出目录，运行 `TestDedicatedMaterializerAcceptsSignedOptionalSkillAndPlugin`，再从仓库根执行：

```sh
node --max-old-space-size=512 infra/agent/internal/runtime/testdata/dedicated-intent.mjs '<批准rc.3 SDK>/package.json' '<输出目录>/skill.json' '<输出目录>/plugin.json'
```

夹具消费真实 Go 产物及原 Host `readIntent/loadResources`，只用批准 SDK 内已有的声明依赖 `yaml@2.9.1`；测试脚本的有限依赖 hook 不进入生产 launcher/config。普通 Go CI 不依赖完整 SDK。两例只证明字节、协议、解包与原消费者一致，未启动 PluginManager、DSH 或供应商进程，不能代替正常市场页验收。

## 固定同镜像准备与运行

使用 `dedicated-runtime-agent.example.json` 作为现有 agent 配置的运行部分。保持原 `shared-runtime-compose.yml` 的材料目录只读挂载；把原已批准 runtime spec、shared profile、公开 Host 模板和 services 放到其既有路径，把冻结 `dedicated-runtime-profile.example.yml` 放为 `dedicated-profile.yml`。省略 `dedicatedProfileFile` 时仍只能启动 shared；配置该文件也不替代 W 的显式唯一 dedicated placement。公开 Host 模板沿原 render 命令生成，仅含 SPKI、模型 ID 和企业策略；真实凭据始终不进入文件或准备环境。

运行镜像必须配套冻结的固定 tooling 配方：Linux Node22.19.0、rc.3 SDK `/opt/dsh/package.json`、Auriga `/opt/auriga-dsh`，以及 pnpm11.28.5 `/opt/auriga-tools/pnpm/node_modules/pnpm/bin/pnpm.cjs`。没有工具时明确失败，不借 PATH/Corepack 补下载。`deploy/compose/real-dsh` 与 offline 的镜像输入、原始 TGZ、source lock 和签名门禁仍由对应已批准配方及主控负责。

Node 在一次连续签名 release activation 内，用同一已验 image config ID 创建固定 `/usr/local/bin/node /run/auriga/dedicated-prepare.mjs` helper。整个准备和启动共享 30 秒控制面预算，使用与常驻实例相同的资源限额和已分配 UID/GID；旧实例、helper、新实例不并行。helper 只读镜像、无网络、无 capabilities、无主 socket/Host token/服务凭据，仅当前 quota home 和新 Agent 专用输出可写。

**ext4 named volume 的 helper 仅挂 `.host` 子目录，需要 Engine API ≥1.45 并实际回读 `VolumeOptions.Subpath=.host`。** 版本不足或 Engine 忽略子目录时，Node 在启动 helper 前拒绝；不会把整池改成可写。旧 NAS Engine 必须由主控确认受支持的卷子目录能力，或另定能证明同一 quota home 的 Engine 可见 bind 路径，不能据此候选宣告旧现场可部署。原 bind quota 后端直接只挂该 home，仍需原目录配额读回。

helper 不 boot、不启动 Shell/模型/后台服务。它沿公开 `initProfile`、`loadProfileDirectory`、`readProfilePatches`、`composeEntries`、专用 normalizer 与 `createRuntimeResolution` 读取持久 profile。已选包的每个文件与当前已验完整 TGZ 对照，资源行批准来自包内 literal patch；用户/home 层不能改变封存基线或引入未知模块。pnpm 的固定 offline/ignore-scripts/store/cache 参数放在真实可写 `profileContext.packageManager`，由运行中的公共 PluginManager 消费。

最终 config 与完整 binding/view/intent/attempt/image、模板及实际文件摘要原子输出到 Agent 私有 `control/dedicated-preparations/<UUID>/output`。Node 核精确两文件、无链接/越根、完整 SHA、基线及当前意图，确认 helper 精确 CID 已退出且移除后撤掉写所有权。常驻实例只读挂到 `/run/auriga/dedicated-prepared`，cache 也只读；quota home/profile/bundle 继续持久可写。真正 launcher 只 boot 该生成文件，patches 参数为空，公开 PluginPackages 仍使用同 SDK 两锚 resolution。

## 排空、重启与失败围栏

`restart-required` 先耐久关闭新 ordinary 准入，等待既有 ordinary/真实 Job 的原精确 Finish 排空；不清未知 slot。旧容器确认停后才准备，新 CID/PID/start proof 和不同 Host UUID 的实际四 cap/current receipt 才能重新 ready。每个当前 intent 最多自动尝试一次受控重启，仍失败就保留证据并关闭入口。

prepare→启动和 guardian Stop/Remove/整树停止证明共用可取消的进程边界。helper/main Create/Start 有未知结果时，原持久 release unresolved intent 优先于超时或“眼下未运行”观察，不能放槽或重放启动；沿既有显式 protected recovery 处理。最终 probe、当前 binding、入口发布与 ready report 同步围栏，晚回不能覆盖 Stop/撤销。

必要定向运行与纯 SDK 脚本见 `infra/agent/internal/renderer/testdata/README.md`；实际 Linux package 安装、只读输出、ext4 子目录挂载、原生页面及供应商调用仍属于主控的真实部署门禁。
