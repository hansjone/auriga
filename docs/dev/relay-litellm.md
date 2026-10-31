# 中转站部署与模型接入

中转站随公开 compose / NAS 栈部署。使用 LiteLLM Proxy MIT 核心，模型、池、虚拟 Key、预算和用量均在 LiteLLM 管理。Auriga 管理台“模型”入口经 `/ai/ui/` 的一次性交接进入独立 UI Origin，默认是管理员 scheme/host 的 **8086** 端口。网络通信配置独立，不将 LiteLLM Key 用作 Auriga 登录凭据。

## 部署

使用全新的安装目录和 PostgreSQL 数据卷。compose 按 [公开入口部署](compose-access.md)运行 prepare 和 up；NAS 按 [NAS 部署](nas.md)完成镜像准备、init、render、start。旧栈的 AI schema、Key、余额不迁移；不要删除现有盐值或重建其文件来修复启动错误。

LiteLLM 固定为 `ghcr.io/berriai/litellm:v1.106.6@sha256:37e2dafb088a7723beb998590682957386dfc83ffea1848486f8e0e5cfac0b06`。该版本及 digest 来自[官方镜像目录](https://github.com/berriai/litellm/pkgs/container/litellm/versions?filters%5Bversion_type%5D=tagged)，可按[官方发行说明](https://github.com/BerriAI/litellm/releases/tag/v1.106.6)验证签名。NAS 的 start 不下载镜像，需预先在目标 Engine 准备此精确 digest（可在构建机拉取后 save/load），并通过 preflight；原三份 Auriga 制品镜像仍用现有校验流程。

离线 `save/load` 可能丢失 RepoDigests。NAS 可在 `stack.env` 设置 `AURIGA_IMAGE_LITELLM=sha256:<目标 Engine 本地 Id>`，供 LiteLLM 和初始化容器共同使用；留空保持上述固定 digest。先独立核对运输包与批准镜像的关联、实际内容和 `linux/amd64`，再从目标 Engine inspect 取得本地 Id，不能直接沿用源 Engine Id 或把 config 摘要当作所有 store 的通用 Id。preflight 对这个精确引用执行原有只读检查，拒绝可变 tag、全零占位、缺失镜像和错误平台，启动仍使用 `--pull never`。

安装器生成四个独立随机秘密：`litellm/master`、`salt`、`team`、`pg_password`，位于 compose `var` 或 NAS `config/materials` 下。不要提交或打印文件内容。数据库为现有 PostgreSQL 内的独立 `litellm` 库，角色 `auriga_litellm` 不能建库或建角色。首次启动的 `litellm-init` 只签发一个 `llm_api` 类型服务虚拟 Key；它先将 claim 提交到 `auriga_provisioning.service_key_state`，成功后置 ready。已有 ready 标记时不重建 Key，丢弃本地卷也不会恢复已吊销 Key。备份整个 LiteLLM 数据库、固定盐值及秘密目录；不再使用 `litellm-state` 卷。若停在 claimed 或秘密指纹变化，初始化失败关闭，须由部署管理员核查 Key 状态后显式恢复，不能删除标记自动重试。轮换时同步 gateway 的秘密文件，不能用 master key 替代。

compose 默认另发布 edge 的 8086 端口；首次 prepare 可加 `--litellm-ui-origin https://admin.example.com:8446`。NAS 设置 `AURIGA_LITELLM_UI_ORIGIN`，留空同样默认 8086；域名代理可用 `https://litellm.example.com`，转到 edge 的 8086 listener，并沿用 access 文件中的代理 mTLS 边界。浏览器证书 SAN 必须覆盖 UI 主机名；防火墙与反代要允许该独立入口。它不能与门户、管理台、实例控制或实例域重合，协议须与管理台一致。UI Origin 纳入安装封存输入，已有材料不能原地换地址。

独立入口只暴露 LiteLLM，不提供 Auriga API。必须先从已登录的 Auriga 管理台打开“模型”；外部 Key 不能建立 UI 会话。edge 每次检查在线 admin 会话与 `ai.providers.write`，只转发 LiteLLM cookie 白名单，UI 和数据均剥离全部 `Access-Control-*` 响应头。

若页面长期显示 `LiteLLM Loading...`，检查浏览器网络面板中每个 JavaScript 请求是否完整结束；HTML 或响应头为 200 不代表前端已就绪。edge 对 `/ai/_next/static/` 和管理台 `/assets/` 的 JS/CSS 协商 gzip，降低大脚本在慢链路上的传输量；仅压缩不超过 8 MiB 的构建资源，不压缩登录 HTML、配置 JSON、管理 API 或模型流。Range 请求保持原表示，匿名资源请求仍先经过管理员门禁。不要为加载问题放开根路径、CORS、会话或写请求 Origin。

Next.js 的 UI 分段资源名可能包含布局标记 `!`，浏览器既会发送字面字符，也会编码为 `%21`。edge 仅在 `/ai/ui/` 下固定 `__next.*.txt` 文件的 GET/HEAD 中接受这两种写法，避免客户端导航退回整页加载；其他编码、目录穿越、编码分隔符及写方法仍拒绝。这些资源同样要求有效管理员会话和同源检查。

LiteLLM 不发布宿主端口。内部健康入口为 `http://litellm:4000/health/liveliness`，compose 健康检查另读 `/health/readiness`；`litellm-init` 正常退出后 ai-relay 才启动。若数据库已有旧卷但没有 LiteLLM 库，首次初始化 hook 不会重新运行，应使用新部署目录验收。

两个 LiteLLM 容器均使用 `cap_drop: ALL`、`no-new-privileges`，无 `cap_add`。compose prepare 保留生成文件的 UID，设置文件为 0640：master/salt 属专用 GID **10003**，pg_password 属 GID **999**，team 属 GID **10001**。`litellm` 加入 10003/999，`litellm-init` 另加入 10001；PostgreSQL 和 ai-relay 沿各自原有组读取对应秘密。prepare 需要 root 或可非交互执行 chgrp 的 sudo；不要用额外 capability 或全局可读权限替代。

NAS 的 `config/materials/litellm/` 原件保持安装器 UID10001、文件0600，不放宽原材料权限。render 在私有 `config/litellm/` 生成独立运行时副本，master/salt 为 10001:10003、team 为 10001:10001、pg_password 为 999:999，文件均0640；compose secrets 只绑定这些副本。重复 render 核对内容和元数据，拒绝链接、内容漂移和权限漂移，不自动轮换或修复。

备份须保留数据库与原秘密、UID/GID 和模式。NAS 可同时保存运行时副本；恢复时先还原完整原材料与回执，再通过 render 重建缺失的运行时副本，已有副本须与原件一致。compose 恢复后重新执行 prepare 应用属组权限。更换任何被绑定的秘密文件后须重新创建消费容器。

权限回归在 Linux root 下运行 `python3 deploy/nas/litellm/permissions_test.py`；本机 Docker 真实验证运行 `python3 deploy/nas/litellm/permissions_e2e.py`。后者由 UID1001 以0600创建材料，使用生产 compose/NAS 的 LiteLLM 服务定义和随机独立 project，验证 healthy、init退出0、能力集合为零，并清理自身容器与卷，不发布宿主端口。

## 接入模型与组池

1. 登录 Auriga 管理台，打开“模型”，进入 `/ai/ui/`；LiteLLM 仍要求其自身管理员登录。由部署管理员安全取用 master 文件作为 LiteLLM 初始管理员凭据，不通过聊天、URL 或剪贴板日志传递。
2. 在 Models 添加提供商，填写供应商模型、API Base、供应商 Key 和公开模型名。至少添加两个提供商；需要组成同一池时为多个 deployment 设置同一公开模型名，配置 LiteLLM 自带路由、限流和故障切换。
3. **首次上线必做**：在 Virtual Keys 找到 `auriga-team`，显式限制团队模型/池、总预算及周期、RPM/TPM。初始化 Key 不自动替运营方决定限额；收紧前不对团队开放模型。外部专用池不要授权给服务 Key。
4. 用 Playground 验证 deployment 和模型池。费用由模型价格表或管理员自定义单价计算；未知费用不能当作预算已生效。
5. 团队 DSH 保持 Auriga 登录和 AI 短票，调用原两个数据入口。gateway 每次校验成员身份，使用服务 Key，覆盖 `user=tenantId:accountId` 与可信 metadata。在 LiteLLM End Users / Spend Logs 按该标识查询用量。

## 成员预算与限速

Auriga 配额页只保留实例资源配额，含旧 `model` 字段的配额写入会被拒绝。proto/OpenAPI 仅保留已弃用声明用于协议兼容，服务不读取也不返回模型配额。成员模型额度改在 LiteLLM **Customers / End Users** 管理，标识精确使用 `tenantId:accountId`，不要误配为 LiteLLM 内部登录用户。

固定版本开源 `/customer/new` 支持 `user_id`、`max_budget`（USD）、`rpm_limit`、`tpm_limit`。通过独立 UI Origin 的 `/ai/customer/new` 创建，例如 `{"user_id":"<tenantId>:<accountId>","max_budget":10,"rpm_limit":30,"tpm_limit":20000}`；使用已完成 Auriga 交接和 LiteLLM 登录的管理员客户端。已有成员在 UI 或 `/ai/customer/update` 更新同一标识。预算周期和重置策略按实际运营设置；服务 Key 总预算仍需另设。先用计价假模型验一次成功、累计后超限拒绝，再开放付费模型；不要把未经验证的零值或未知费用当成封禁措施。

## 给外部调用方发 Key

1. 在 LiteLLM Virtual Keys 创建 Key，设置可用模型/池、预算、预算周期、RPM/TPM、到期时间与别名。将虚拟 Key 安全交付调用方；不交付 master、salt 或团队服务 Key。
2. 客户端 OpenAI base URL 设为门户 Origin 加 `/ai/v1`，Bearer 使用该虚拟 Key，model 使用池的公开模型名。公开数据入口只开放 models 与 chat/completions，拒绝调用方指定上游 URL、凭据、fallbacks 等控制项；外部用量 user 固定为 external:Key摘要，不能伪造成团队成员。管理 UI、登录、Key 管理和静态资源只允许独立 UI Origin 且必须有在线有效的 Auriga admin 会话。
   外部 Key 入口接受无 `Origin` 的客户端，以及实际单个 `Origin` 精确命中部署
   `AURIGA_LINKS_ALLOWED_ORIGINS` 的请求。反代补写的本站 Origin 也须显式列入；默认空列表
   只接受无 Origin。跨站、重复或逗号拼接 Origin 均拒绝，`X-Forwarded-*` 不代替这项检查。
   虚拟 Key 鉴权、成员归属隔离及剥离上游 CORS 响应头保持原样。
3. 用本地假 OpenAI 上游依次验证 models、普通 chat 和流式 chat；再验证无 Key、错误 Key、吊销 Key 和超预算 Key 均拒绝。检查 LiteLLM Spend Logs，再吊销测试 Key。不调用付费模型代替验收。

参考：[虚拟 Key](https://docs.litellm.ai/docs/proxy/virtual_keys)、[用量归属](https://docs.litellm.ai/docs/proxy/cost_tracking)。

## 原生 Beta 调试

Beta 不再创建旧 AI 账本或启动 admin runtime。先准备独立的 LiteLLM 测试实例，使用数字回环 HTTP Origin 或有效 HTTPS，并安全保存仅用于该实例的服务虚拟 Key 文件。停止 Beta 后执行：

```powershell
node scripts/beta-ui/runner.mjs litellm --state D:\auriga-beta-example --origin http://127.0.0.1:4000 --team-key-file D:\auriga-beta-example\litellm-team.key
```

然后沿 Beta 原流程启动。使用新建的 Beta StateDir（含独立 UI 端口 18088），从管理台模型入口交接；外接 LiteLLM 也必须设置 `SERVER_ROOT_PATH=/ai` 和指向该 UI Origin 的 `PROXY_BASE_URL`。命令只保存 Origin 与秘密文件路径，不取用生产密钥；未配置 LiteLLM 时 Beta 不提供中转站。原生 Beta 的端口发布仅用于本机调试，公开栈无需开放 4000。

独立 LiteLLM 验收：在 Linux 上以 root 或可非交互 chgrp 的 sudo 用户运行 `node deploy/nas/litellm/verify.mjs`。脚本只接受本机 Docker socket，按部署权限脚本准备秘密，创建随机 project，使用本地假上游，并在结束时清理自己的容器和卷；这项检查不覆盖 Auriga 管理员会话或 gateway 成员身份链路。

完整入口验收使用 `deploy/compose/scripts/verify-relay.mjs`。先按公开流程准备并启动全新的一次性本机栈，再将 `scripts/relay-fixture.yml` 中的假上游加入该测试 project。运行：

```sh
node deploy/compose/scripts/verify-relay.mjs \
  --materials /path/to/test-stack/deploy/compose/var \
  --ca /path/to/test-stack/browser-ca.crt
```

`--ca` 仅供当前进程验证本机测试证书；受信任 HTTPS 可省略。脚本从该栈秘密文件读取应急账号与 master key，创建临时角色、成员、模型及虚拟 Key，只输出检查名称；它会更改测试栈数据，应在验收完成后销毁该一次性栈。检查覆盖管理员与普通成员 UI、外部调用与管理边界，以及真实 IAM 原生设备协议签发的 AI 短票和成员用量归属。该协议检查不启动 DSH 插件进程。

独立 UI 组件回归可在 `infra/edge` 执行 `AURIGA_LITELLM_UI_TEST_ORIGIN=http://127.0.0.1:<临时端口> go test -count=1 -run '^TestLiteLLMUIHandoff$' -v .`。目标须为本机临时 LiteLLM，配置 `SERVER_ROOT_PATH=/ai`；测试使用随机 edge 端口，覆盖 IP/子域 Origin 的一次性交接、真实页面和 JavaScript、撤销/过期及 CORS。此组件测试的 IAM 为夹具，真实身份链路仍由上面的整栈脚本验证。

真实浏览器就绪检查见 `infra/edge/browser-tests/README.md`：同时设置真实 LiteLLM 上游与 `AURIGA_LITELLM_CHROMIUM_TEST=1`，会等待登录表单可见，避免把服务端 Loading 壳当作页面加载成功。`PROXY_BASE_URL` 应与安装输入的独立 UI Origin 加 `/ai` 一致；域名部署使用自己的受信 UI Origin，不复用另一现场的 IP，也不信任任意请求头改写该配置。
