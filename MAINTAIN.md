# 现场维护

先从管理台处理成员、邀请、角色、配额、身份源、模型供应商、凭据、入口和运维策略。需要调整 Agent 行为时，先使用资源市场的插件、技能和受控配置。业务规则或界面能力超出这些入口时，再修改源码。

## 准备离线开发机

普通使用者从 **Forgejo Release** 下载源码标签和同版本 `auriga-devkit-<版本>.tar`，不需要自己准备依赖或打包；GitHub 不放这些发行附件。开发机预装 Docker Engine、Compose 2.29+、Git；不要求本地 Go、Maven、Node，工具链均在 devkit 镜像中。

联网电脑按 `DEPLOY.md` 下载发行附件，核验引导程序的独立批准 SHA-256、公钥及签名清单。下载标签源码时保留 `.git`，例如：

```sh
git clone --branch 0.2.0 https://forgejo.example.com/owner/auriga.git auriga-source
git -C auriga-source rev-parse HEAD 'refs/tags/0.2.0^{commit}'
```

将源码目录（含 `.git`）和附件送进内网。Forgejo 网页自动生成的源码 zip/tar 没有 Git 历史，不能直接用作可追溯构建目录；也可使用套件内的 `source/`，但复用套件时它属于清单记载的原始版本，须先导入目标标签历史。

用已核验的发行引导程序解包。若有分卷，把输入改为 `.tar.part001` 或 `.tar.parts.json`，同目录保存全部分卷和签名边车，无需手工拼接：

```sh
cd /media/auriga-release
./auriga unpack auriga-devkit-0.2.0.tar --out /opt/auriga-devkit-0.2.0 --verify-key /secure/release-public.pem
cd /opt/auriga-source
mkdir -p bin
cp /media/auriga-release/auriga bin/auriga-cli
chmod 0755 bin/auriga-cli
./auriga devkit verify /opt/auriga-devkit-0.2.0 --verify-key /secure/release-public.pem
git switch -c site/local-fix 0.2.0
./auriga bundle --build --offline
```

这里 `/opt/auriga-source` 是拷入的标签 checkout，操作时需对相应目录有权限。`devkit verify` 校验缓存及当前源码依赖，在源码根写被 Git 忽略的 `.auriga-devkit.json`；后续 `bundle --build --offline` 自动定位套件。也可用 `--source /opt/auriga-source` 从其他目录校验。以后仅检查已绑定套件可直接运行 `./auriga devkit verify --verify-key /secure/release-public.pem`。

版本之间依赖未变时，发行流程复用 `auriga-devkit-<锁SHA256>.tar` 的相同字节，并以每版友好附件名供下载。`devkit.json` 与发行清单均记载锁摘要；发行清单还记录当前源码与套件原始提交。总键包括 Git 可见的 Go mod/sum/work、Maven pom/wrapper 配置、Node manifests/锁、Python 依赖、Dockerfile，以及批准 inputs/recipes 摘要与工具/基础/服务镜像 digest。业务源码改动不影响依赖键。新 checkout 绑定使用自身上游标签作构建基线，避免复用套件时误报旧上游。

锁不匹配时会列出新增、缺少或不同的路径，并提示下载当前上游标签的 `auriga-devkit-<版本>.tar`。现场自行改依赖须维护者重新准备批准输入；绝不静默联网。保留 `cache/`、`devkit.json` 和根摘要清单原字节，构建逐次校验。首次解包用 `unpack --verify-key` 验完整原包；旧 `devkit --verify <目录>` 仅用于尚未修改源码的完整套件。

仅维护者重出套件时，发行索引 `build` 登记依赖分支提供的 recipes、inputs（Go proxy、Maven/npm 缓存、pnpm store、Corepack、system 包）及固定 digest 镜像，按 `DEPLOY.md` 的 `prepare-release.sh` 在维护机/NAS 执行并由主控发布，不在 CI 完整冷建。

## 修改、测试与构建

在开发机源码目录修改，按对应模块开发规范运行针对性测试，建立普通提交便于审查和回退。构建也支持尚未提交的修改及未被 Git 忽略的新文件，删除会进入源码快照。被 Git 忽略的凭据、构建缓存、部署数据不进入快照。准备归档前核对 `git status`，防止把不应公开的未忽略文件当作源码。

```sh
git status --short
./auriga bundle --build --offline --version 0.2.0-site.3 --out /exports/auriga-offline-0.2.0-site.3.tar
```

省略 `--version` 时，包含现场提交或未提交修改的源码会得到基于源码摘要的现场后缀。省略 `--out` 时输出 `auriga-offline-<版本>.tar`。每次包记录上游版本、实际提交、分支、工作区状态、是否含现场改动、源码摘要和 UTC 构建时间。doctor 及管理台侧栏“关于”可查看这些信息。

`--offline` 只从 devkit 的本地缓存读取依赖和镜像，构建容器使用 `--network none`、`--pull never`，装配复用原离线配方。缺少文件时输出对应路径，Go/Maven/pnpm 缺依赖时保留原工具的具体报错。修改依赖锁或上游 Dockerfile 后，需要回联网维护机重新准备匹配的依赖输入和 devkit。

将产物与其独立批准摘要送入服务器，在安装目录执行升级，再从正常页面实测。

```sh
sudo ./auriga upgrade /media/auriga-offline-0.2.0-site.3.tar
sudo ./auriga doctor
```

升级会停核心栈和确认归属的实例，先备份数据目录、密钥、安装状态及 Compose 当前声明且已存在的 local named volumes。卷必须没有运行消费者。恢复会核对卷名称、驱动、选项、标签和归档摘要，失败时保留恢复记录供重试。旧版本备份可能缺少卷快照，恢复时会明确提示。

## 跟进上游

在联网维护机取得新版本 devkit 和批准的标签。将其拷入内网开发机，先核验新套件，然后在旧现场分支的源码目录导入新历史。

```sh
git fetch /media/new-devkit/cache/source-history.bundle 'refs/tags/*:refs/tags/*'
git switch site/local-fix
git merge <批准的新上游标签>
```

先核验新版本同版本 devkit 和引导程序；依赖相同时可复用旧套件。合并完成后更新 CLI 并重新绑定；不要依赖复用套件内旧源码的版本标签。

```sh
cp /media/new-release/auriga bin/auriga-cli
chmod 0755 bin/auriga-cli
./auriga devkit verify /opt/auriga-devkit-0.3.0 --verify-key /secure/release-public.pem
./auriga bundle --build --offline --version 0.3.0-site.1 --out /exports/auriga-offline-0.3.0-site.1.tar
```

复用套件的 `cache/source-history.bundle` 可能只含原始版本历史。目标标签不在里面时，从联网电脑目标源码 checkout 用 `git bundle create /exports/upstream.bundle --all` 导出并拷入，内网先执行 `git fetch /media/upstream.bundle 'refs/tags/*:refs/tags/*'` 再合并；勿强制覆盖已有标签。

解决冲突、测试、审查后赋予新的现场版本并走 `upgrade`。保留原备份、源码提交和上一版本包，核对管理台“关于”与 doctor 是否显示目标版本。

禁止直接在运行容器或部署服务器上修改程序文件。服务器仅负责加载批准包和保存数据，源码改动在开发机完成并形成可追溯的安装包。
