# Auriga DSH

> _Auriga_：御夫座，拉丁语"驾车的人"。

Auriga DSH 是一个可以自己部署的团队平台：给团队里每个人一个自己的 DeepSeek Harness（DSH）工作空间，并替他们管好这些工作空间——安装、配置、启停、升级、回收。在此之上提供团队身份（可插拔的身份源）、角色和配额、模型网关、团队工具的 MCP 网关、资源中心和团队知识。

**当前状态**：持续开发中，已经可以使用。M0、M1 已完成，M2 核心流程已用真实 DSH 运行时通过本地验收；现场可用 Forgejo Release 的离线安装包在 Linux 上部署，也可用源码包和开发套件在 Linux 上离线构建、签名、安装自己的版本。通用自托管的完整验收仍在进行，计划见 [路线图](docs/plan/roadmap.zh.md)，进度见 [进度](docs/plan/progress.md)。

## 从哪开始

| 你是谁                                  | 看这里                                                             |
| --------------------------------------- | ------------------------------------------------------------------ |
| 只部署，不改代码                        | [DEPLOY.md](DEPLOY.md)                                             |
| 在 Linux 改代码，并自己构建、签名、升级 | [MAINTAIN.md](MAINTAIN.md)                                         |
| 只有 Windows 电脑，想在本机前后端联调   | [Windows 单机前后端联调](docs/dev/windows-local-dev.zh.md)         |
| 发布桌面客户端（Windows 开发机）        | [MAINTAIN.md 的“客户端发布”](MAINTAIN.md#客户端发布windows-开发机) |

不论走哪条路径，先看 [必备参数与保管清单](docs/site/keep-list.zh.md)（要自己准备什么、安装后必须永久保管什么），遇到报错查 [构建与部署常见报错](docs/site/troubleshooting.zh.md)。发布 Release 附件的维护者见 [docs/release/maintainer.zh.md](docs/release/maintainer.zh.md)。

- 架构：[总体](docs/architecture/platform.zh.md)、[节点代理](docs/architecture/node-agent.zh.md)、[身份源](docs/architecture/identity.zh.md)
- 架构决策记录：[docs/adr](docs/adr/README.md)
- 接口契约：[contracts](contracts/README.md)
- 日志定位：[trace、集中日志与只读采集](docs/dev/observability.md)

许可证：平台 AGPL-3.0，另提供商业许可；DSH 插件（`clients/dsh-plugin`）MIT。详见 [LICENSING.md](LICENSING.md)。
