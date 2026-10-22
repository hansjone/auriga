# Auriga NAS isolated test pack

Public deployment templates for a separate NAS test stack (`auriga-nas-test`).

- Operator guide: [`docs/dev/nas.md`](../../docs/dev/nas.md)
- Do not put real domains, LAN IPs, or credentials in this tree
- Build images via `.github/workflows/nas-bundle.yml` (`workflow_dispatch` + exact commit)
- Never deploy from a worker session; main controller runs site instantiation from private notes

## 下载工件后的准备

GitHub Actions 工件不会保留文件执行位（见 [Permission Loss](https://github.com/actions/upload-artifact#permission-loss)）。
在解包后的工件根目录，先核对 `images/manifest.json` 的提交与 `images/SHA256SUMS`，再核对工件根 `SHA256SUMS` 中的 bootstrap/manifest 摘要并恢复预构建二进制的执行位：

```sh
sha256sum -c SHA256SUMS
(cd images && sha256sum -c SHA256SUMS)
chmod 0755 deploy-nas/bin/auriga-nas-bootstrap
```

源代码 checkout 使用对应路径 `deploy/nas/bin/auriga-nas-bootstrap`。操作脚本使用 `sh deploy-nas/scripts/<操作>.sh`；
脚本不要求执行位。bootstrap 准备好后再按操作指南配置数据根并初始化，NAS 不需要安装 Go。
