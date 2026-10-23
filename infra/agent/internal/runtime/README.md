# runtime

Instance drivers: `Driver` interface, in-memory `Fake` (tests), and `Docker` (Engine API / Podman).

`Preparing` wraps a driver to fetch artifacts, ensure instance directories, and render config before `Ensure`.

Docker 的所有 Ensure/Start 都要求 `ReleaseActivation` 提供的内部同步授权上下文。
reconciler 统筹一次连续 Ensure → 独立 actual execution 落盘 → Start；不能裸调用或跨方法保存 lease。
完整 spec 在入队和准备时深拷贝固定，生产 Preparing 缺快照/布局/归档库/UID/执行库即拒绝。
内存 Fake 保留开发用途，真实 Docker 中的 fake-dsh 镜像同样必须正式验签。

授权从实际 Store 读取当前 ring，以真实 GOOS/GOARCH 和可信安装策略 Verify；每次重新读取公开原始 M/C
并 VerifyOCI，创建前、真正启动前、恢复 Ready 前再次实时检查。非空归档也经实际 artifacts.Store.Ensure
核字节；旧 execution 的归档纳入 holder/GC keep，确认实际删除后才释放。

本地 image inspect 必须精确匹配原 repo@digest、已核 config ID 和 OS/Architecture；pull 后再核。
创建使用 config ID 与显式 platform，保存返回的真实 container ID；记录落盘失败禁止 Start。
启动、恢复网络、探活和端点验证绑定该 ID、Image、受管标签及实际 generation，不能重新按名字认领。
清理不依赖发行签名仍有效，但删除失败保留执行记录；观察授权失败只撤 Ready/端点，不强杀运行进程。

生产通过原连续分区锁和同步持久 intent 防止旧 Engine 请求越过更高水位；取消/断线/崩溃不构成动作排空。
没有受支持的 Docker 服务端 fencing/drain 证明，因此未决分区保持阻塞，不提供盲清恢复命令。
测试含生产 EngineClient/Docker + 真 bbolt 的 A7/B8 交错，以及原调用子测试进程被终止后旧 HTTP Engine
动作延迟完成的反例。该 HTTP 模型不等于真实 daemon 故障注入或现场验收。
