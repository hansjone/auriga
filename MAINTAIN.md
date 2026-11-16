# 现场维护

先从管理台处理成员、邀请、角色、配额、身份源、模型供应商、凭据、入口和运维策略。需要调整 Agent 行为时，先使用资源市场的插件、技能和受控配置。业务规则或界面能力超出这些入口时，再修改源码。

## 准备离线开发机

联网维护机取得批准的发行索引，`build` 字段登记 Forgejo 依赖分支提供的 recipes、inputs，以及 Go/Web/Java 工具和五种装配基础镜像的精确 digest。inputs 归档包含 Go proxy 文件、Maven/npm 内容缓存、pnpm store、Corepack 和各服务 system 包，保留原配方的锁文件及摘要清单。

```sh
./auriga devkit --release /secure/auriga-release.json --out /exports/auriga-devkit.tar
```

devkit 保存完整源码快照、Git 可达历史及版本标签、全部登记的构建依赖与镜像层、安装器和 `SHA256SUMS`。可以加 `--sign-key` 签名。通过独立受信渠道核对外层摘要后，把套件拷入预装 Docker/Compose 和 Git 的内网开发机并解包。Go、Maven、Node 等编译工具在随包的工具镜像中运行。

```sh
mkdir auriga-devkit
tar -xf auriga-devkit.tar -C auriga-devkit
cd auriga-devkit/source
./auriga devkit --verify ..
git switch -c site/local-fix <批准的上游标签或提交>
```

签名套件校验另加 `--verify-key /secure/devkit-public.pem`。校验通过后保留 `cache/`、`devkit.json` 和根摘要清单原字节。源码与 Git 状态可以改变，离线构建会单独校验不可变依赖缓存。包内 Git 配置不带联网机远程地址和登录凭据。

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

在旧套件源码目录提交现场改动并导出分支，再在已核验的新套件源码目录导入。新套件自带新版安装器和匹配的依赖缓存。

```sh
git bundle create /exports/site.bundle site/local-fix
cd /secure/new-devkit/source
git fetch /media/site.bundle site/local-fix:site/local-fix
git switch site/local-fix
git merge <批准的新上游标签>
./auriga bundle --build --offline --version 0.3.0-site.1 --out /exports/auriga-offline-0.3.0-site.1.tar
```

解决冲突、测试、审查后赋予新的现场版本并走 `upgrade`。保留原备份、源码提交和上一版本包，核对管理台“关于”与 doctor 是否显示目标版本。

禁止直接在运行容器或部署服务器上修改程序文件。服务器仅负责加载批准包和保存数据，源码改动在开发机完成并形成可追溯的安装包。
