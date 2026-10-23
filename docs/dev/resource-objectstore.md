# 本地资源对象存储（R1OBJ1）

本组件只存储不可信归档的**原始字节**，供后续版本上传服务调用。没有HTTP入口、Spring装配、数据库迁移或新增依赖；不解压、执行、扫描或判断插件安全性，不作ACL、审核、签名、分发或安装决策。64 MiB是存储字节上限，gzip/条目/展开大小/manifest等正式归档限制须由后续验证器实现，不能以存储成功代替归档验证。

## 内部端口与生命周期

唯一内部端口为`auriga.resource.objectstore.ObjectStore`，JDK实现为`LocalObjectStore`。所有对象和stage操作都显式传入租户UUID；业务结果只有受控`StageId`和`ObjectInfo(digest,sizeBytes)`，不暴露宿主绝对路径。

- `putStaged(tenant,input,maxBytes)`：从调用起接管并关闭输入流，包括参数、读取、写入和close失败。分块计数并计算SHA-256，不使用`available()`或声明长度推断大小，不整包入内存。maxBytes必须为0–64 MiB，最多多读一个字节判断超限；失败删除自身部分payload并释放lease，不返回句柄。
- 成功返回`Staged(id,object)`前，payload已经flush、`FileChannel.force(true)`并关闭写流和写通道，输入流也已成功关闭。stage是随机UUIDv4标识，只在创建它的ObjectStore实例中有效；不能跨租户、跨实例或在重启后恢复为可提交句柄。
- `openStaged(tenant,id)`：完整核验普通文件、大小和摘要后交付已打开的只读流，供未来归档验证器读取。调用方负责关闭，并应在commit/abort前关流。
- `commitIfAbsent(tenant,id,expected)`：调用方必须提供预期摘要及大小；校验与真实暂存结果完全一致，再重新流式核验磁盘内容。预期摘要必须为`sha256:`加64个小写十六进制字符，大小0–64 MiB。错误大小/摘要、磁盘篡改均不发布对象。有效本地句柄的commit尝试无论成功失败都消耗句柄并清理stage；无权或未知句柄不能消耗别人的stage。
- `abort(tenant,id)`：只处理本实例本租户的活跃stage；对已消费、未知或其他租户句柄是幂等空操作。没有通过旧stage继续写入、删除或覆盖已提交对象的操作。
- `stat(tenant,digest)`：读取真实普通文件并完整流式核验SHA-256及大小后返回信息，不以目录项存在代替核验。`open(tenant,expected)`同样先核验，随后将**同一个已经打开的通道**归零交付，不重新按路径打开另一个文件。返回流由调用方关闭；后续读/close的JDK IOException由使用方适配为既有存储错误。
- `close()`：等待当前存储操作退出，释放并清理本实例stage；可重复调用。它不接管已交付的读取流。关闭后调用是内部生命周期错误`IllegalStateException`；调用方不得把原始异常文本当跨进程协议，服务适配器按既有`common.internal`处理此类编程错误。

本端口允许0字节用于明确的原始存储边界；这不意味着空文件是合法gzip归档。上传服务仍须应用正式版本/归档规格，并把客户端expectedDigest与真实暂存结果比较。取消或超时应关闭输入流使stage失败清理，或在取得句柄后finally调用abort；字节上限不替代请求超时、成员权限或配额。

## 不覆盖的原子发布

物理根由可信部署者提前创建并提供，示例为`/var/lib/auriga/resource-objects`或`C:\auriga\data\resource-objects`，不是请求参数。组件使用自己的`.auriga-resource-objects-v1/<tenant>/`子树：`staging/stage-<随机32hex>/`保存payload和lease，`objects/<摘要hex>`保存已提交字节。所有路径只由固定名字、UUID和验证后的摘要推导。

发布使用`Files.createLink(target,stagedPayload)`的原子“不存在才创建”语义：完整、已关闭写通道的payload一次成为可见对象。**不使用可能替换已有目标的ATOMIC_MOVE，也没有复制覆盖回退。**同租户同摘要并发提交收敛到一个对象；目标已存在时必须重新核大小和摘要，正确对象保持原文件身份，损坏对象拒绝并保留现场，不能用新上传覆盖或删除。

stage与已提交对象短暂共享inode；stage清理只删除stage名字，已提交名字保留。提交后的旧句柄、abort、过期清理都不能获得写通道或删除已提交名字。跨租户存放独立文件，即使摘要相同也不共享句柄、目录授权或物理文件。元数据事务、权限检查和引用登记均在后续服务完成；DB登记失败留下的已提交孤儿按正式至少24小时保留与引用释放规则另行处理，本组件**没有DeleteUnreferenced或任何已提交内容GC**。

payload执行force，但不宣称跨所有文件系统的断电持久性或与数据库的原子事务。发布后清理失败会返回存储失败并保留可核验对象，而不是报告整个操作成功；调用方必须通过后续stat/重新暂存提交确认，不把失败推断为对象一定不存在。

## 物理目录和平台条件

只支持默认JDK物理文件系统，显式拒绝ZipFS等非物理provider。根必须是绝对且无`.`/`..`规范化歧义的既有目录，Windows只接受普通驱动绝对路径，拒绝UNC、设备路径和ADS。请求摘要中的绝对、驱动、UNC、穿越、反斜杠等都在字符串格式校验阶段被拒绝，不能成为路径。

每次访问逐级使用NOFOLLOW_LINKS检查根、namespace、租户和父目录；只接受非symbolic、非other的真实目录及普通文件。Windows JDK的`isOther()`覆盖junction/reparse，不能只检查`isSymbolicLink()`。文件打开也使用NOFOLLOW_LINKS；发现链接、reparse、设备或目录冒充对象均拒绝。POSIX新建私有目录700、文件600；Windows继承可信根既有ACL，本组件不修改宿主ACL或特权，部署者必须先限制根的其他主体写权限。

必要能力包括同文件系统硬链接、排他文件锁以及所用NOFOLLOW检查。Linux/Windows都走这些真实JDK原语；不支持或跨设备发布时明确返回`resource.storage_unavailable`，不回退非原子复制/覆盖。平台定向测试包含Windows junction与Linux symlink，不需要启用Windows符号链接特权。

威胁边界是可信部署根及其祖先不由其他同权限进程任意改写。组件可拒绝操作时观察到的链接和损坏，不能抵御拥有相同宿主写权限的攻击者在检查与使用之间不断换根、修改已交付流对应inode或伪造内部marker。Windows BasicFileAttributes的fileKey可能为空，不能用它声称消除此类竞争；测试用真实硬链接见证与Files.isSameFile验证“不替换”。根应由存储组件独占写入。

## 过期暂存清理

默认stage保留期24小时，可通过内部构造参数`Clock`和正`Duration`由受信装配方指定，无新环境变量或配置协议。`cleanupExpiredStaging(tenant)`仅枚举该租户staging的直接子目录，识别随机stage名字与版本化lease marker中的租户、stage UUID和创建Instant；不递归删除调用方目录，不触碰objects。

stage创建时先取得lease文件排他锁，再写marker和payload。锁一直持有到commit/abort/close，包括仍在复制、校验及等待归档验证的阶段。清理器使用tryLock；同JVM不同实例的OverlappingFileLock或另一JVM的锁均表示活跃，不能因超过TTL强删。进程退出由OS释放锁，留下的完整或部分stage才可成为孤儿。过期条件为`createdAt <= clock.instant()-ttl`，时钟倒退不会提前清理；未来时间不清理。

清理只处理标记完整、已过期、可获得锁、且仅含lease/payload这两个认识名字的stage。非法标记或额外材料保留，链接/类型异常或IO错误封闭失败；不存在的租户/staging为空。发生删除失败时报告错误并保留可诊断材料，不递归强删。它不负责回收仍活跃但业务方遗忘abort的句柄，调用方须落实finally和关闭生命周期。

## 错误与接线责任

使用既有`AurigaException/ErrorCode`：输入格式/上限/预期大小为`common.validation_failed`；未知句柄/缺失对象为`common.not_found`；摘要、磁盘内容或读取预期大小不符为`resource.digest_mismatch`；文件系统/链接/锁/读写/close失败为`resource.storage_unavailable`。没有新增跨进程错误码；绝对路径不作为业务结果或错误参数返回，底层cause仅供既有内部诊断。

后续上传服务需要先在线鉴权与配额，再暂存；使用openStaged进行正式归档/manifest验证，关闭验证流，以客户端expectedDigest和验证后大小提交，最后在resource事务登记版本/引用。授权、归档验证、数据库、审计/事件、发布签名、下载、引用释放和生产配置均不在R1OBJ1中接线。

## 定向验证

Windows先点源既有Auriga环境，再执行：

```powershell
.\mvnw.cmd -B -q -pl services/resource/impl -am test '-Dtest=LocalObjectStoreTest,ResourceCursorTest,ResourceSignatureVectorsTest' '-Dsurefire.failIfNoSpecifiedTests=false'
.\mvnw.cmd -B -q -pl services/resource/impl spotless:check
```

测试使用真实临时文件系统、64 MiB流式边界、实际硬链接/锁、两个独立ObjectStore、独立JVM进程退出、损坏与缺失、跨租户、输入/实际部分输出/close故障注入。故障注入仅组合真实NIO输出流，不增加生产测试模式。Linux同一字节码通过已有JDK镜像在无网络、无端口、只读根、非root的临时容器与独立tmpfs中执行，精确命令、平台具名跳过及结果见工位报告；无现场部署验证。
