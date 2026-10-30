# 云端插件面板的部署与自动续签

Compose 与 NAS 使用同一条制品链：`web/panel` 构建自包含的
`auriga-team-panel.js`，随 edge 镜像发布；镜像包含 `/auriga-panel`。
普通 edge、platform、agent 和实例均不接触面板私钥。

首次 bootstrap 生成独立 Ed25519 面板钥，并把以下文件纳入安装收据：

- `panel-signing.key`：0600，只读挂给无网络、短时运行的 `panel-prepare`。
- `panel-keys.json`：公开 JWKS，由 agent renderer 校验并写入实例只读材料，
  通过 `AURIGA_PANEL_KEYS_FILE` 下发。与身份头公钥独立。

`panel-prepare` 从镜像读取构建字节，生成内容寻址文件名和 SHA-384 SRI，
使用 RFC8785 JCS 字节和 Ed25519 签清单，最后原子替换 `manifest.json`。
edge 依赖首次签名成功；公开目录只读挂给 edge 的 `static.panelRoot`。
`/panel/` 在门户、实例源和控制入口提供相同制品，不提供目录索引、
SPA 回退、内部版本记录或私钥；实例控制 API 仍要求实例令牌。
旧散列脚本保留，保证已取清单的浏览器能完成加载。

插件在 Node 验清单签名、有效期和版本，浏览器再做 SRI。
缺公钥、过期、未知钥、清单/脚本篡改均拒绝加载，不关闭验签绕过故障。

## 版本由内容自动派生

不再使用 `AURIGA_PANEL_VERSION` 或镜像内的手填版本文件。
签名器根据构建脚本的 SHA-256 摘要，维护**每个安装自己的**单调版本：

- 新安装从 `0.0.1` 开始；相同字节保持当前版本。
- 新内容将当前版本的 patch 加一；旧 `0.0.0` 安装直接升级为 `0.0.1`，
  既有较高版本继续递增，不重新归零。
- 被替换的清单先保存至输出目录 `.panel-history/<version>.json`。
  每条记录仍带原签名，每次发布都校验记录、当前清单和版本高水位；
  恢复已经发布过的旧内容会拒绝，不能给旧字节分配一个新版本。
- 显式版本发布的底层检查仍拒绝降版、同版换字节、时间倒退或异常旧签名。
  对同一输出目录的发布使用操作系统文件锁串行化。

版本是安装内的发布顺序，不是跨安装通用的源码提交号。第一次见到的摘要
视为新内容，因此发布者仍须选用正确的可信镜像；不推断从未发布过的镜像
之间的源码年代。不得删除版本记录、只恢复旧清单或重新初始化来绕过回滚检查。
备份/恢复须一起保存原安装收据、面板私钥、公钥、**完整签名输出目录**。

## 启动检查和小时级自动续签

清单有效期 30 天；剩余不超过 7 天时，同版同字节刷新签发时间与有效期。
尚未到续签窗口时保持原清单；新内容立即发布。过期后也能用原钥恢复签发，
无需重启 edge 或 DSH。

部署主机需要 `crontab`、`flock` 和运行中的 `cron`/`crond`。
Compose `prepare.sh`、NAS `start.sh` 自动幂等安装当前部署用户的 cron：
每小时第 17 分钟调用对应 `renew-panel.sh --scheduled`，保留其他 cron 项。
缺少上述条件时部署报错，须先配置宿主机 cron 服务再重试。
NAS 使用宿主机定时任务，不在 installer 容器中安装调度器。

启动时 `panel-prepare` 检查并按需签发；NAS `start.sh` 还会主动重跑检查，
避免复用已退出的旧 prepare 容器。每小时任务只检查正在运行的 edge，
不会自动启动已停止的安装；原安装目录和部署脚本路径必须持续保留。
调度器不挂私钥、不起常驻 signer，也不向容器提供 Docker socket。
每次仅以 `--no-build --pull never` 运行原部署选定的短时无网络 signer。
Compose signer 使用原私钥文件的 UID/GID，NAS 使用 10001；均 `cap_drop: ALL`，
不需要 `DAC_OVERRIDE`，不修改私钥内容或放宽 0600。

手动立即检查/续签：

```sh
sh deploy/compose/scripts/renew-panel.sh
# 或 NAS 部署目录：
sh deploy/nas/scripts/renew-panel.sh
```

移动部署脚本后，重新运行公开 prepare/start 流程更新 cron；旧路径的 cron
须由部署用户从 `crontab -e` 删除（按 `auriga-panel-` 注释定位）。
卸载安装时同样移除该条目。

## 故障信号

- signer 失败写 stderr，宿主机调度失败也返回非零并写失败状态；定时日志在
  Compose `var/panel-renew.log`、NAS 数据目录 `config/panel-renew.log`。
- 成功检查刷新 `.panel-renew-checked`；失败留下 `.panel-renew-failed`。
  输出状态更新不改变安装收据，不包含秘密。
- edge 的 Docker healthcheck 只读公开目录：失败状态存在、清单过期，
  或超过两小时没有检查心跳时，容器变为 unhealthy。
  因而 cron 停止和 Docker 调用失败也能在到期前暴露。
- 查看 `docker compose ps`、edge 健康日志与 `panel-prepare` 日志，修复
  原因后手动执行上面的续签命令；成功后清除失败信号并恢复健康。
  健康检查不替代插件验签。

现有缺面板钥的安装仍由收据检查拒绝，不静默补钥或轮换。
此阶段不提供自动密钥轮换。
