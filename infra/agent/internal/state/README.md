# state

`state.db` 使用 bbolt 保存节点 meta、desired/actual 和 `release_trust_v1`。

发行信任初始化由独立 `release-trust.guard` 保护：先独占创建并同步 guard，
再在一笔事务中写 pending meta 和完整 ring，身份四文件保存成功后才标为 active。
guard 绑定节点公钥、节点 ID、CA、sealing 公钥、初始证书摘要和固定根摘要。
pending、缺 guard、身份不完整、绑定不符或损坏记录都不能返回可用发行信任；
缺失 state.db 不代表首次入网，Join 在 Open 创建文件之前检查历史痕迹。
`CompleteReleaseInitialization` 只可在完整本地身份匹配 pending 绑定时完成，
不联系 workspace、不补根；本版本没有自动修复入口。

Linux 首次初始化在 guard 文件 Sync 成功后，从实际身份目录逐层 fsync 到路径根（含根），
持久化 guard 目录项和完整父链接链；这也覆盖 CLI ensureLayout 在调用前刚建立的目录。
相关文件系统必须支持文件和目录 fsync 语义，不能把“目录已存在”视为其父链接已经耐久。
符号链接先按文件系统解析，再确定实际同步链；任一文件/目录 open、sync、close 错误都向上传播，
保留已写 guard，不能继续创建 state.db 或自动重试 TOFU。
Windows 保留实际可写 guard 文件的 File.Sync（Go 调用 FlushFileBuffers）及真实错误传播；
没有复制 Linux 目录链 fsync、申请额外权限或执行卷级 flush，也不宣称任意 Windows 文件系统具有相同目录链掉电保证。
测试覆盖 IO 顺序与失败保痕迹，未进行真实断电实验。
如果字面 symlink/.. 与清理后路径指向不同目标，首次初始化在 RPC 和任何写入前拒绝，
要求传入已解析的绝对路径；普通 symlink 别名及括号等合法目录字符继续支持。

`MergeReleaseKeys` 在 bbolt Update 中重读最新 ring、完整验图、一次提交，
并发 RPC 不能用旧快照覆盖已接纳 key；失败不部分提交，历史 kid 永久保留。
读取重验持久图、固定截止、guard 和身份绑定并返回深拷贝。
调用方缓存 ring 时仍须通过 `Lookup(kid, now)` 在每次使用时核截止。
普通明文开发入网只写显式 untrusted 状态，不能经 Renew 晋升为可信根。

证书文件与 meta 的续证持久阶段由 enroll 的同目录锁协调，RPC 可以并发；
证书合法而 key 批次失败时保留新证书，返回明确的信任更新错误。
此包不实现 RT2 的发行启用、高水位或驱动互斥。
