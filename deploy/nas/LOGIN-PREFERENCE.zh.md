# 部署级登录偏好

产品缺省为公司 SSO 优先；部署可以设置 `AURIGA_EDGE_LOGIN_PREFERENCE=team`，默认直接显示团队账号表单。公司 UAC、团队和应急身份源保持原状，用户仍可主动切换登录方式。手动切到公司方式会创建新的扫码挑战；默认 company 的浏览器 SSO、导航证明、MFA 和设备批准流程保持原行为。

Edge JSON 支持 `loginPreference`，值为 `company` 或 `team`；环境变量优先。缺省或空值按 company，其他值拒绝启动。Edge 将已核配置作为 `auriga-login-preference` 元信息投影到门户、原生登录页和管理入口。浏览器不能通过 URL 参数改变此部署默认值。

## 新配方

本版本固定 `docker-compose.yml` 的 `edge.environment` 将 `stack.env` 的 `AURIGA_EDGE_LOGIN_PREFERENCE` 显式传入 Edge；缺省 company。该 environment 项覆盖 env_file，因此 render 重新生成 `config/edge.env` 不会丢失偏好。修改数据根的 `stack.env` 后，按既有公开 start/Compose 流程重建 Edge 容器。

## 旧配方的最小升级

旧 `8be9394` 与当前配方还包含中转和审计等其他差异。为了只变更登录偏好，**不整份覆盖旧 Compose，也不更换原 start/render**。准备本批 Edge 镜像（包含新版门户和管理静态资源），把公开 `scripts/login-preference.py` 作为独立运维工具带到现场；不需要改变封存的 `materials/edge.config.json`。

以下路径是操作者已经核实的原固定 Compose 与活动数据根 stack.env 的绝对真实路径，不能指向另起的 recipe 或临时 overlay：

```sh
python3 /path/to/login-preference.py \
  --compose /existing/recipe/docker-compose.yml \
  --stack-env /existing/data/stack.env --preference team --check
python3 /path/to/login-preference.py \
  --compose /existing/recipe/docker-compose.yml \
  --stack-env /existing/data/stack.env --preference team
```

工具只接受本批已核的公开 `8be9394`、`de13e8f8` 两种 Compose 完整前像 SHA256（或已加精确偏好单行的同一前像），并检查唯一 edge/environment 结构。它仅新增一条 environment 映射、在 stack.env 写入偏好；不会传入 Docker socket、运行 Docker、修改镜像、轮换材料或调用认证接口。其他版本、重复项、内容漂移、符号链接和多硬链接输入均拒绝。原始文件逐字节备份为同目录 `.login-preference-<原SHA256前16位>.bak`，先校验两个输入再原子替换。可重复执行。

按已批准的镜像升级流程在同一个 stack.env 更新 `AURIGA_IMAGE_EDGE`，使用原公开 start/Compose 流程重建 Edge（只需 Edge 镜像，不需 platform/agent 升级）。标准 start 会依次读取同一个 stack.env 并 render；偏好从 Compose environment 传入，旧 render 覆盖 edge.env 不影响它。不要只执行容器 restart，因为环境变更需要 recreate。

回退展示方式可对相同文件执行 `--preference company` 再重建 Edge。需要逐文件回退本次迁移时，先核工具打印的备份路径及后续配置变更，再从本批备份恢复；不要把其他批次的新镜像或配置一并退回。工具不自动部署。

## 验收

从正常 `/login` 和 `/instance-login.html` 进入，应默认看到团队账号；团队账号完成原密码及 TOTP 流程。点击公司方式仍能创建 UAC 扫码挑战；设备批准页未登录时使用同一团队偏好，核码和批准步骤保持原样。管理台在 team 配置下默认进入团队账号表单，返回选择仍保留其他方式；公司缺省下保持其原有选择流程。再次运行原 render/start 后，默认方式仍为 team。公司网络不可达的站点，以合成 SSO/扫码/凭据测试验收 UAC；本偏好不代表真实公司服务连通。
