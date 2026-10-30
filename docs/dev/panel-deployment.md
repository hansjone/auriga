# 云端插件面板的部署与续签

Compose 与 NAS 使用同一条制品链。`web/panel` 构建自包含的
`auriga-team-panel.js`，随 edge 镜像发布。镜像也包含独立的
`/auriga-panel` 签名工具；普通 edge 进程不接触面板私钥。

首次 bootstrap 生成独立 Ed25519 面板钥：

- `panel-signing.key`：安装目录中的 0600 私钥，纳入安装收据，只读挂给
  无网络、短时运行的 `panel-prepare`；不挂给 platform、edge、agent 或实例。
- `panel-keys.json`：公开 JWKS，纳入同一安装收据，只读挂给 agent。
  renderer 验证公钥集后写入实例的只读材料 `/run/auriga/panel-keys.json`，
  并设置 `AURIGA_PANEL_KEYS_FILE`。身份头公钥继续走独立文件。

公开 prepare/render 创建签名输出目录。Compose 启动时先运行
`panel-prepare`，成功后 edge 才启动。签名工具从镜像中读取面板，生成
内容寻址文件名和 SHA-384 SRI，以 RFC8785 JCS 字节和 Ed25519 签清单，
最后原子替换 `manifest.json`。旧散列文件保留，保证已取清单的浏览器
仍能完成加载。公开输出目录只读挂给 edge 的 `static.panelRoot`。
`/panel/` 在门户、实例源和实例控制入口提供相同公开制品；不提供目录索引、
SPA 回退、私钥文件或额外 API。实例控制 API 仍要求实例令牌。

插件先在 Node 验清单签名、有效期和版本，再由浏览器按清单做 SRI。
缺公钥、过期、未知签名钥、清单或脚本篡改均拒绝加载；不能通过改身份公钥
或关闭验签修复部署问题。

## 更新与续签

清单有效期为签发后 30 天。应在到期前续签；长期保持运行不会自动续期。
Compose 在部署主机运行：

```sh
bash deploy/compose/scripts/compose.sh up --no-deps --force-recreate \
  --abort-on-container-exit --exit-code-from panel-prepare panel-prepare
```

NAS 在部署目录运行（沿既有安装模式，不要求在 NAS 安装 Node 或 Go）：

```sh
sh deploy/nas/scripts/renew-panel.sh
```

续签复用原安装钥，延长清单期限，不需要重启 edge 或 DSH。
同版本续签必须对应相同脚本字节。升级面板时，发布构建传入更大的
`AURIGA_PANEL_VERSION=major.minor.patch`（默认开发版本 `0.0.0`）；
NAS 构建通过 Docker `--build-arg` 传入，Compose 通过同名环境变量传入。
版本写入镜像，不由运行时任意指定。旧版本、时间倒退、同版本替换脚本、
已有清单签名异常均拒绝发布。签名输出更新不会修改安装收据。

现有缺面板钥的旧安装会被收据检查拒绝，不能静默补钥或伪造清单；测试栈
使用新的安装目录按公开流程部署。备份与恢复须一起保存原安装收据、面板钥、
公钥和签名输出。此阶段不提供自动密钥轮换或旧安装迁移。
