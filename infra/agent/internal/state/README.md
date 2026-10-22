# state

`state.db` 使用 bbolt 保存节点 meta、desired/actual 和 `release_trust_v1`。

发行信任初始化由独立 `release-trust.guard` 保护：先独占创建并同步 guard，
再在一笔事务中写 pending meta 和完整 ring，身份四文件保存成功后才标为 active。
guard 绑定节点公钥、节点 ID、CA、sealing 公钥、初始证书摘要和固定根摘要。
pending、缺 guard、身份不完整、绑定不符或损坏记录都不能返回可用发行信任；
缺失 state.db 不代表首次入网，Join 在 Open 创建文件之前检查历史痕迹。
`CompleteReleaseInitialization` 只可在完整本地身份匹配 pending 绑定时完成，
不联系 workspace、不补根；本版本没有自动修复入口。

`MergeReleaseKeys` 在 bbolt Update 中重读最新 ring、完整验图、一次提交，
并发 RPC 不能用旧快照覆盖已接纳 key；失败不部分提交，历史 kid 永久保留。
读取重验持久图、固定截止、guard 和身份绑定并返回深拷贝。
调用方缓存 ring 时仍须通过 `Lookup(kid, now)` 在每次使用时核截止。
普通明文开发入网只写显式 untrusted 状态，不能经 Renew 晋升为可信根。

证书文件与 meta 的续证持久阶段由 enroll 的同目录锁协调，RPC 可以并发；
证书合法而 key 批次失败时保留新证书，返回明确的信任更新错误。
此包不实现 RT2 的发行启用、高水位或驱动互斥。
