# Dedicated Node 资源装配与观察

【待审查】本批接入 Node 的专用资源准备、placement 周期协调与 Host 回执门禁。完整专用启动器、可写 profile/bundle 与受控重启须配套装配后才可部署；本工位未运行真实 DSH、PG 或 NAS，不以下载成功作为安装完成。

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
