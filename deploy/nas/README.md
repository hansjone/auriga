# Auriga NAS isolated test pack

Public deployment templates for a separate NAS test stack (`auriga-nas-test`).

- Operator guide: [`docs/dev/nas.md`](../../docs/dev/nas.md)
- Runtime release guide: [`docs/dev/runtime-release.md`](../../docs/dev/runtime-release.md)
- Do not put real domains, LAN IPs, or credentials in this tree
- Build images via `.github/workflows/nas-bundle.yml` (`workflow_dispatch` + exact commit)
- Never deploy from a worker session; main controller runs site instantiation from private notes

## 下载工件后的准备

GitHub Actions 工件不会保留文件执行位（见 [Permission Loss](https://github.com/actions/upload-artifact#permission-loss)）。
在解包后的工件根目录，先确认 `images/manifest.json` 的 `commit`、`source_tree` 对应独立核对的精确源码提交，
并核对 `bootstrap`、`runtime_release` 的包内相对路径及 SHA-256（后者目标为 `linux/amd64`）。
再验证工件根 `SHA256SUMS` 中的两个二进制、manifest、versions 摘要，以及 `images/SHA256SUMS` 中的镜像归档摘要，恢复两个工具的执行位：

```sh
sha256sum -c SHA256SUMS
(cd images && sha256sum -c SHA256SUMS)
chmod 0755 deploy-nas/bin/auriga-nas-bootstrap deploy-nas/bin/runtime-release
```

两个工具均由 workflow 从同一精确 checkout 使用 Go 1.27.3、`CGO_ENABLED=0` 预构建为 Linux/amd64 二进制。
源代码 checkout 的对应路径为 `deploy/nas/bin/auriga-nas-bootstrap` 和 `deploy/nas/bin/runtime-release`。
操作脚本使用 `sh deploy-nas/scripts/<操作>.sh`，脚本不要求执行位。bootstrap 准备好后再按操作指南配置数据根并初始化。
在 NAS 上可直接使用 `deploy-nas/bin/runtime-release`，按 runtime release 指南运行 `verify`、`stage-layout` 等命令，无须现场安装 Go 或编译。

本工件是准备包，**不是完整离线安装包**：它不提供官方可信根、已签名的真实 DSH 发行材料、DSH 全部镜像层或第三方基础镜像。
工具不会自动生成或信任发布根；operator 必须从可信渠道独立审核并固定包外公开根、正式材料和精确允许镜像。
仅本机匿名 HTTP import profile 的运输流程、经 operator 独立审核的 ORAS 工具及其使用路径仍需另行准备，
遵照 runtime release 指南按原始 manifest 摘要运输与核验，并按对应精确版本的显式命令执行导入；
工具不自动启动 registry、不管理 registry 认证，也不自动执行现场部署。
完成摘要检查、材料验证或基础栈初始化不代表已能登录、真实实例已就绪或现场验收通过。
