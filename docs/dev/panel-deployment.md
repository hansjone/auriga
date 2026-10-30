# 云端插件面板的部署、版本与自动续签

`web/panel` 构建自包含的 `auriga-team-panel.js`，随 edge 镜像发布。
`/auriga-panel` 是短时、无网络 signer；普通 edge、platform、agent、实例及
宿主定时器均不读取面板私钥。私钥 `panel-signing.key` 保持原安装的 0600，
只读挂给 signer；独立公钥 `panel-keys.json` 经 agent renderer 写入实例只读材料，
通过 `AURIGA_PANEL_KEYS_FILE` 下发。两者纳入原安装收据，与身份头钥分开。

## 启动故障只影响面板

两套公开 Compose 的 edge 均不等待 `panel-prepare` 成功。
NAS `start.sh` 和开发栈 `prepare.sh` 尝试安装定时器并检查签名；失败记告警，
继续启动其他服务。启动用 signer 加 `-best-effort`：签名失败记录状态和日志，
退出 0；手动直接调用不带此参数的 signer 仍以非零报告失败。

edge 没有可读、结构合法、未过期且存在入口脚本的清单时，仅 `/panel/` 的
合法制品路径返回明确 503；其他门户、管理、登录路由照常工作。
恢复制品后无需重启 edge。此可用性检查不代替插件的 Ed25519/SRI 验证。

## 内容版本与受控回退

不使用手填 `AURIGA_PANEL_VERSION` 或镜像版本文件。signer 按构建字节的
SHA-256 摘要维护每个安装内的单调版本：首次 `0.0.1`，相同字节保持版本，
内容变化则 patch 加一。旧安装 `0.0.0` 可直接升级。

回退镜像或 revert 到历史内容时，同样分配**高于当前版本的新版本**，
不恢复旧版本号。比如 A/0.0.1 → B/0.0.2 → A/0.0.3。
显式发布低版本、同版本换字节、时间倒退、旧清单/历史签名异常仍拒绝。
替换前先保存 `.panel-history/<version>.json`，每次检查记录的签名与版本。

版本是安装内的发布顺序，不是跨安装源码提交号。首次见到的摘要无法表达
源码年代，发布者应选用正确可信镜像。完整备份/恢复安装收据、原钥、公钥和
全部输出目录；不要只恢复旧清单或删除历史。当前历史记录不能防御拥有输出
目录写权限的主体删除记录，插件也未保存持久版本高水位；不要把它描述为
防御恶意宿主回放的完整客户端防回滚方案。

## 启动检查与小时级任务

清单有效期 30 天，剩余 **不超过 7 天** 时以同版同字节更新签发时间和有效期。
新内容立即发布；过期后可用原钥恢复。制品/SRI 和清单最后原子替换，旧散列脚本
继续保留，供已取得旧清单的浏览器加载。

宿主有 `crontab` 和运行中的 cron/crond 时，公开 prepare/start
幂等安装每小时第 17 分钟的任务，保留其他 cron 条目。WSL 或精简开发宿主缺少
条件时明确告警并跳过安装，启动检查仍执行；应配置宿主 cron 或定期手动续签。
NAS container 模式使用与 start 相同的非 root operator 安装自己的 cron，
不要求给 operator 增加数据目录写权限或宿主 sudo。

有 `flock` 时使用文件锁；精简 NAS 缺少它时，使用私有任务目录中的原子
`run-lock` 目录锁，正常完成、命令失败或收到终止信号均移除。不会按 PID 自动
抢占旧锁。宿主掉电或进程被强杀后，先确认本安装没有续签进程或 signer 容器，
再由原调度用户核对该锁为本人所有的0700真实空目录，并用 `rmdir` 删除后重试。
未知所有者、软链等异常锁直接拒绝，不自动修复。signer 原有 OS 发布锁保持生效。
新任务目录显式使用0700，避免继承 ACL 使 `umask 077` 创建出可供其他用户访问的目录；
已有任务目录元数据错误仍拒绝，不修改共享上级 ACL。

定时器的 job、日志、锁和宿主失败状态放在数据根的直接父目录下
`.auriga-panel-<标识>/`（开发 Compose 放在 `deploy/compose/` 下）。
父目录必须规范、属于当前 operator 或 root、无 group/other 写位，并可供 operator
创建私有目录；任务目录属于当前用户且为 0700，绝不挂给 signer。
root 定时器拒绝复用其他 UID 的目录，不在 signer 可写输出中做 chmod/mv 或写日志。
容器模式原有的 env 导出临时目录要求不变，见 [NAS 配方](nas.md)。

`crontab -l` 只有退出 1 且准确返回英文 `no crontab for <当前用户>` 才视为空表；
权限、I/O 或其他错误均拒绝更新，不覆盖原表。日志 `renew.log` 超过 1 MiB 时
保留一份 `renew.log.1`。定时任务只检查正在运行的 edge，不拉起已停止的安装。

```sh
# 手动检查/续签；不重建、不拉取新镜像，沿用部署选定的短时 signer。
sh deploy/compose/scripts/renew-panel.sh
sh deploy/nas/scripts/renew-panel.sh
# 只安装或移除本安装的任务（用原安装任务的同一运维用户运行）。
sh deploy/compose/scripts/renew-panel.sh --install
sh deploy/compose/scripts/renew-panel.sh --remove
sh deploy/nas/scripts/renew-panel.sh --install
sh deploy/nas/scripts/renew-panel.sh --remove
```

NAS `stop.sh` 自动调用 remove；卸载时先执行 `stop.sh` 再删除部署材料。
开发 Compose 停栈/卸载或移动脚本前先执行 `--remove`，新位置再 `--install`。
移除只删本安装 marker 对应的条目，保留其他任务；日志留在私有目录便于诊断。

## 故障信号与恢复

signer 在 OS 发布锁内写入/清除 `.panel-renew-failed`，成功检查刷新
`.panel-renew-checked`。抢锁失败只记录“已有任务执行，跳过”，不写失败标记，
也不清除当前发布者的状态。宿主 Docker 命令失败在私有目录写 `failed`，
无需取得 uid10001 输出目录写权限。

edge Docker healthcheck 只读公开输出：签名失败标记、过期/缺失入口脚本、
或两小时未刷新检查心跳都会 unhealthy。宿主 cron/Docker 失败由私有状态和日志
立即可查，并在成功 signer 检查心跳超过两小时后反映为 unhealthy。
缺 cron 的开发栈同样可能因心跳陈旧而 unhealthy，服务本身仍继续运行。
该健康信号不控制其他服务启动，也不会自动重启全站。

检查 `docker compose ps`、edge 健康日志、`panel-prepare` 日志及任务目录的
`failed`/`renew.log`。修复原因后手动续签，成功会恢复签名状态；宿主成功调用会
清除私有失败状态。签名失败但旧清单仍有效时，旧面板继续可用；过期后面板503。
不得关闭插件验签绕过问题；现有缺钥安装仍由收据拒绝，不自动补钥或轮换。
