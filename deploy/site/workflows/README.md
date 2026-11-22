# 现场工作流运行器与示例工作流

对应设计 `docs/dev/site-deploy.zh.md` **D12**（工作流触发与提权边界）、**D11**（日志/收据/退出码/锁）。CLI 侧 sudo 规则见 `deploy/offline/README-OFFLINE.md`「经 sudo 调用」；sudoers 片段见 `deploy/site/sudoers/auriga-runner`。

本目录只提供**可复制的示例与安装说明**。不要在本机或 NAS 上按本文真实安装运行器或写入 sudoers（属实施第 11 步验收）。

## 边界（先读）

能触发 `auriga deploy` 的人，**等价于能以 root 改这个站点的人**：部署会以 root 执行源码里的构建与启动脚本，这一点无法靠参数白名单消除。sudoers / CLI 白名单只限制「能跑哪些子命令、传哪些参数」；真正限制「部署哪些源码」靠 `site.json` 的 `source.git.allowedRefs`、可选 `trustedSigners`，以及经 sudo 的快进限制（降级走 `rollback` 或 root 人工 `--allow-downgrade`）。

签名包 `bundle` **不进**工作流 sudo 白名单：在独立构建机上运行，签名钥放构建机或作为该机构建工作流的秘密变量传给非 sudo 进程。同机 `deploy` 不需要签名钥。

## 示例工作流

| 文件 | 用途 |
|---|---|
| `site-deploy.yml` | 手动触发 `deploy <提交号>`（可选 `full` / `services` / `dry_run`）；推送触发示例默认注释掉 |
| `site-rollback.yml` | 手动触发 `rollback [--to <序号>]` |

复制到现场 Forgejo / GitHub 仓库的 `.forgejo/workflows/` 或 `.github/workflows/`，按现场改 `runs-on` 标签。GitHub 与 Forgejo 的差异见各 YAML 文件头注释。

**日志上传选定**：只上传 `--json` 标准输出的脱敏收据。`logs.dir`（默认 `/var/log/auriga`）为 root 所有，运行器读不到；**不要**放宽该目录权限，也**不要**在 sudoers 加 `cat`/`tee` 去读日志。

**取消与超时**（结果取决于运行器怎样结束 job，第 11 步验收时实测一次并把结论补到这里）：

- 运行器以普通用户运行，无权直接向 root 进程发信号；只有 `sudo` 本身收到 SIGTERM / SIGINT / SIGHUP 时，才会把信号转发给 root 的 `auriga`。
- `auriga deploy` 捕获 SIGINT / SIGTERM 并取消上下文：构建阶段的子进程（依赖检查、构建容器等走上下文的命令）连同其进程组被终止，部署按“未切换就失败”结束（退出码 1），照常写收据并在 `--json` 时打印。切换阶段的停启服务与健康检查不随上下文中断，会继续执行到切换成功或进入失败处理（自动回滚 / 交人工），然后写收据。
- `auriga rollback` 不捕获这些信号，收到即退出：不写收据，`receipt.json` 为空或缺失；已启动的启动脚本、`docker compose` 等子进程不一定随之结束。
- 两种情况下站点 `flock` 都由内核在 `auriga` 进程退出时释放（锁文件描述符不会被子进程继承），所以残留子进程可能在锁释放后继续运行；取消后先确认没有残留的构建或启动进程，再触发下一次部署或回滚。
- `sudo` 被 SIGKILL（部分运行器取消或超时直接 kill）时无法转发：root 的 `auriga` 继续跑完、照常写收据到 `logs.dir`，期间一直持锁，后续任务会因锁失败（退出码 1），工作流只是看不到结果。
- 杀在切换中（`phase` 为 `switch` 或 `rollback`），现场可能是混合版本：由管理员 `sudo auriga status`、`sudo auriga history` 看当前发行与最后一条记录，查看 `logs.dir` 里的日志；必要时 `rollback --to <上一成功序号>`，或人工处理。不要靠再次取消或重复触发来“修复”。

**并发**：YAML `concurrency` 组减少排队；CLI 对站点锁文件的 `flock` 是最终保证，第二个任务直接失败。

退出码：`0` 成功；`1` 未切换就失败；`2` 切换失败已自动回滚；`3` 需人工。非 0 时工作流步骤失败（标红），收据仍在 `if: always()` 步骤上传。

## 运行器安装说明（步骤与注意事项，不下载不执行）

1. **建普通用户**（示例名 `auriga-runner`，须与 sudoers、`site.json` 的 `server.runner` 一致）：
   - `useradd --system --create-home --shell /bin/bash auriga-runner`
   - **不要**把该用户加入 `docker` 组，也不要给它写数据目录、发行目录、`logs.dir`、`site.json` 的权限。
2. **安装 Forgejo runner**（或 GitHub 自托管 runner）：只按上游文档在该用户下注册到现场仓库/组织，标签至少包含示例里的 `self-hosted` 与 `auriga-site`（可改，须与 YAML `runs-on` 一致）。Forgejo runner 的标签必须映射为宿主机执行（如 `auriga-site:host`），映射到 docker 镜像时 job 跑在容器里，调不到宿主机的 `sudo` 与 `auriga`。上传收据的动作需要宿主机有 Node；Forgejo 不支持 `upload-artifact@v4` 时按 YAML 头注释换成 v3。本说明不下载、不启动任何安装程序。
3. **安装 CLI**：以 root 把已核对来源与 SHA-256 摘要的 `auriga` 二进制装到 `/usr/local/bin/auriga`，属主 root、模式 `0755`。来源可以是签名发行附件或 root 用受信提交在本机构建；`deploy` 不会替换该文件。
4. **登记运行器**：在 `/etc/auriga/site.json` 的 `server` 段加 `"runner": "auriga-runner"`（即 `server.runner`，例如 `"server": {"data": "/srv/auriga/data", "runner": "auriga-runner"}`）。先有登记再放行 sudoers，否则 CLI 不会把该账户识别为运行器。
5. **放 sudoers**：复制 `deploy/site/sudoers/auriga-runner` 到 `/etc/sudoers.d/auriga-runner`，`chmod 0440`，`visudo -cf /etc/sudoers.d/auriga-runner`。片段使用 `Defaults` 的 `env_reset` 与 `secure_path`，**不加** `SETENV`，只放行 `deploy *`、`rollback *`、`status`、`history`。
6. **首次手工验证**（root）：
   ```sh
   sudo -u auriga-runner sudo -n /usr/local/bin/auriga status
   sudo -u auriga-runner sudo -n /usr/local/bin/auriga history
   sudo -u auriga-runner sudo -n /usr/local/bin/auriga deploy --config /tmp/x   # 应被 CLI 白名单拒绝
   sudo -u auriga-runner sudo -n /usr/local/bin/auriga bundle                   # 应被 sudo 拒绝
   ```
   前两条应成功打印状态/历史，后两条应失败。确认无误后再把示例工作流接到现场仓库。
