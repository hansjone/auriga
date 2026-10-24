# 内部 tar.gz 校验组件（R1ARCH1）

`auriga.resource.archive.TarGzipArchiveValidator`是资源上传前置的内部组件，尚未接线HTTP、Spring装配、版本登记或签名。现有对外契约只写gzip，本批不修改该契约，也不把内部支持子集宣传为公开格式或全部npm tgz兼容。

## 支持子集

本轮只支持单个gzip member中的POSIX ustar，条目只允许普通文件或目录（普通文件typeflag为0或NUL，目录为5）。POSIX magic/version、经典八进制数值、checksum、数据及padding均核验；拒绝PAX/global、GNU longname/longlink、sparse、binary数值、STAR等扩展以及链接、设备、FIFO、未知类型。不作格式猜测，不解压到宿主目录，不执行脚本/npm/install。

使用Commons Compress的受保护`readRecord()`扩展点，在库自动处理PAX/GNU/sparse**之前**检查每个物理header并计数；header、数值、UTF-8字段和checksum的解析计算仍调用库，不另写tar解析器。只使用512字节的库block，避免库把EOF后的整块数据盲跳。须有两个完整零EOF record；条目数据的对齐padding和EOF后的padding都必须为零，完整展开长度必须是512的整数倍。空ustar（两个零块）合法，不增加manifest文件或目录布局要求。

## 预算与完整消费

- 原始gzip字节最大67,108,864（64 MiB）。重新计数并计算SHA-256，必须同时匹配已暂存ObjectInfo的digest/size与调用方expectedDigest，不能仅信声明长度。摘要严格为`sha256:`加64个小写hex。
- 物理条目最多4096，包含目录；扩展在第一条记录就拒绝，不允许被库透明隐藏后绕过计数。校验结果不返回条目列表，内部路径登记至多4096项。
- 完整展开流最大268,435,456（256 MiB），包含header、文件数据、条目padding、EOF和后续零padding。read/skip都经过计数；padding检查放在实际读取层，不能假定第三方`skip`一定调用InputStream.skip。
- 完整展开量U与实际原压缩量C满足`U <= 100*C`，精确等号允许。流式上界同时受256 MiB及暂存size构造的ratio预算限制，返回前再次核原size/digest与实际C，使用long乘法且C已限64 MiB，不溢出。不接受通过追加原始尾随数据放大C。
- gzip解析完整消费到CRC32/ISIZE及EOF；单member结束后底层不得再有任何字节，包括零或第二个member。截断、错误校验和和尾随材料均拒绝，不能在tar EOF出现时直接当全部校验结束。

gzip头部在两次标准库处理时都限64 KiB，限制可选文件名/注释等metadata的内存。本组件没有公开调小上限的测试开关。Commons Compress 1.28.0跳过可选FHCRC验证，因此先以JDK GZIPInputStream校验同一有界标记头部，再reset并交给Commons Compress做单member解码；不自行解释gzip头或实现CRC。原始计数/摘要位于有界mark/reset缓冲之下，不重复计算重读的头部字节。

## 路径比较规则

严格UTF-8，拒绝无效编码、名字字段NUL终止后的隐藏非零后缀、控制字符、绝对/驱动/UNC路径、反斜杠和任何`..`段。允许正常`./`、中间冗余`/`和目录结尾`/`，比较键剔除空段/`.`并逐段NFC规范化。原始归档字节不重写；普通文件不能以`/`结尾或归一化成空路径，`.`/`./`可作为单个显式根目录条目。

NFC键重复、同路径文件/目录重复、文件作为祖先或以文件覆盖已有后代都拒绝。隐式父目录不单独扩张登记表，因此深层路径不会制造无界metadata。linux目标保持大小写区别；windows/any目标另外拒绝ADS/非法Windows字符、尾点/尾空格、设备名（含带扩展名、COM/LPT及其上标数字别名），以NFC加Locale.ROOT大写键保守拒绝大小写碰撞。目标os使用已有UploadMetadataManifest.OsEnum，不根据验证服务器的宿主OS猜测。

这些规则仅用于校验，不把规范键作为新的文件名写回归档。未来安装端仍须对自身落地路径、根及链接风险执行安全解压和隔离规则，本组件不是安装授权。

## 调用与所有权

内部调用为`validate(tenant,StageId,ObjectInfo,expectedDigest,targetOs)`，只通过ObjectStore.openStaged取得流并关闭自己取得的流；不调用put/commit/abort/cleanup，也不关闭ObjectStore。参数校验失败且未取得流时不接管任何资源。输入流、包装流和库decoder各自生命周期有界，原始取得的流只关闭一次；关闭失败不能报告校验成功。

成功结果只含原ObjectInfo、目标os和条目/文件/目录/完整展开字节/文件数据字节统计。不验证包名、SemVer、DSH版本范围语义、manifest布局或包内某个JSON文件，不授予ACL或发布/安装权限。调用方仍负责鉴权、配额、实际manifest规则、之后的commit与数据库登记，并在自己的finally中决定abort。

参数、格式、类型、路径、预算、CRC/截断等失败使用`common.validation_failed`；原摘要或size不符为`resource.digest_mismatch`；底层读取/关闭IO失败为`resource.storage_unavailable`。ObjectStore已有错误保持；不新增archive_invalid。错误参数不带路径或输入，库原始异常文本不透传。没有把故障流包装成模拟成功接口。

## 依赖准入与验证

本轮经主控明确准入`org.apache.commons:commons-compress:1.28.0`：根pom仅新增版本pin及dependencyManagement，resource-impl直接compile依赖，未借用test-support传递库。实际resource-impl compile解析为Compress 1.28.0、Codec 1.21.0、IO 2.20.0、Lang3 3.20.0；Codec/Lang3保留Boot BOM更高版本，IO达到候选2.20.0要求。未加入可选ASM、JNI、XZ、Brotli或Zstd。

官方依据：[坐标](https://commons.apache.org/proper/commons-compress/dependency-info.html)、[依赖](https://commons.apache.org/proper/commons-compress/dependencies.html)、[安全公告](https://commons.apache.org/proper/commons-compress/security.html)、[tar实现](https://commons.apache.org/proper/commons-compress/xref/org/apache/commons/compress/archivers/tar/TarArchiveInputStream.html)、[gzip实现](https://commons.apache.org/proper/commons-compress/xref/org/apache/commons/compress/compressors/gzip/GzipCompressorInputStream.html)。所列tar PAX CPU/内存问题已在更早版本修复；本轮依然禁用扩展并执行预算，不把版本号或当前公告视为独立安全审查结论。

集中验证命令：

```powershell
.\mvnw.cmd -B -q -pl services/resource/impl -am test '-Dtest=TarGzipArchiveValidatorTest,LocalObjectStoreTest,ResourceCursorTest,ResourceSignatureVectorsTest' '-Dsurefire.failIfNoSpecifiedTests=false'
.\mvnw.cmd -B -q -pl services/resource/impl spotless:check
```

测试有真实ObjectStore暂存/复读/调用方提交、实际64 MiB压缩边界、256 MiB展开边界、4096条目、ratio100等号、CRC/可选头CRC/尾随/截断、库自动处理前的扩展拒绝、路径规范化与各目标OS别名、真实流的故障注入及关闭检查。Linux在已有JDK镜像内对候选源码重新编译，非root、只读根、无网络/端口、独立tmpfs运行；数量、具名平台skip、首失及最终证据以工位报告为准。
