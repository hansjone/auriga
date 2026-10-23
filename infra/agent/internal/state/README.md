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

RT2S 的 `WithReleaseActivation` 保持 `(purpose,name,os,arch)` 连续锁，从锁内重新授权、
持久高水位提交直至同步驱动动作全部返回；`Close` 拒绝新资格并等待原协调域退出。
RT2R 的 `WithReleaseExecution` 在同一资格内添加 `release_intent_v1`：真实 Engine 副作用前
事务写入并显式 Sync 未决 intent。仅原调用在所有动作有明确成功回执、上下文未取消且仍持相同资格时，
比较并删除自身 intent。请求丢响应、HTTP 错误、取消、panic 和进程死亡留下持久阻塞；
同序重试和更高序都被拒绝。后续一次成功回执不能抹掉早先失联动作；没有强制清标记 API。
健康失败与执行结果不明分开：确定执行已完成后仅探活超时可由原协调域清 intent。
这一选择保安全序，不保证异常退出后自动恢复可用性。

独立 `release_execution_v1` 记录原实际完整 DshRuntime/envelope、实际 generation、candidate、
真实 container ID、原 C 的 ConfigID、已固定 EngineImageID、公开 proof 摘要定位与 created/started 阶段。
记录 Version=2，沿用原 bucket 以保证旧记录不会消失；Version1、缺字段或集合外 EngineImageID 均拒绝读写和重开，
没有自动推断迁移。EngineImageID 只可为 ConfigID 或 ProofDigest；运行时仍重验原 M→C 和容器的那个确定 ID。
desired/actual 的状态写入和删除
不能覆盖它；读取返回独立副本。内部 JSON 形状、绑定、规范编码或任一记录损坏均拒绝读写。
只有确认对应真实容器删除成功才可比较 container ID 删除 execution；分区高水位永久保留。
测试使用真实 bbolt，覆盖重开、损坏、关闭、深拷贝、未决结果和 panic；没有真实掉电耐久实验。
