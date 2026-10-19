# Auriga

> *Auriga*：御夫座，拉丁语"驾车的人"。

Auriga 是一个可以自己部署的团队平台：给团队里每个人一个自己的 DeepSeek Harness（DSH）工作空间，并替他们管好这些工作空间——安装、配置、启停、升级、回收。在此之上提供团队身份（可插拔的身份源）、角色和配额、模型网关、团队工具的 MCP 网关、资源中心和团队知识。

**状态：早期开发（M0 立项阶段），还不能使用。** 计划见 [docs/plan/roadmap.zh.md](docs/plan/roadmap.zh.md)，进度见 [docs/plan/progress.md](docs/plan/progress.md)。

- 架构：[总体](docs/architecture/platform.zh.md)、[节点代理](docs/architecture/node-agent.zh.md)、[身份源](docs/architecture/identity.zh.md)
- 架构决策记录：[docs/adr](docs/adr/README.md)
- 接口契约：[contracts](contracts/README.md)

许可证：平台 AGPL-3.0，另提供商业许可；DSH 插件（`clients/dsh-plugin`）MIT。详见 [LICENSING.md](LICENSING.md)。
