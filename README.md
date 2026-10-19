# Auriga

> *Auriga* — Latin for "the charioteer".

Auriga is a self-hosted platform that gives every member of a team their own
[DeepSeek Harness (DSH)](https://www.npmjs.com/package/@deepseek-ai/dsh) workspace,
and manages those workspaces for them: install, configure, start, stop, upgrade and reclaim.
On top of that it provides team identity (pluggable identity sources), roles and quotas,
a model gateway, an MCP gateway for team tools, a resource center and team knowledge.

**Status: early development (milestone M0).** Nothing here is usable yet.
See [docs/plan/roadmap.zh.md](docs/plan/roadmap.zh.md) and [docs/plan/progress.md](docs/plan/progress.md).

## Architecture at a glance

| Layer | Technology |
|---|---|
| Business services (`iam`, `org`, `workspace`, `resource`, `knowledge`, `collab`, `audit`, `ops`) | Java 25, Spring Boot, MyBatis-Plus, Flyway — one process for small installs, separate services at scale |
| Edge gateway, node agent, AI gateway, egress proxy, `ctl` | Go |
| Portal, admin console, team panel | Vue 3 + TypeScript |
| DSH plugin (`auriga-dsh` on npm) | Node.js |
| Data | PostgreSQL (bundled or external), NATS JetStream |
| Deployment | Windows installer, Linux tarball, Docker Compose, Kubernetes (later) |

Design documents (Chinese): [platform](docs/architecture/platform.zh.md) ·
[node agent](docs/architecture/node-agent.zh.md) · [identity sources](docs/architecture/identity.zh.md) ·
[decision records](docs/adr/README.md) · [contracts](contracts/README.md).

## Repository layout

```
services/   Java services (Maven multi-module)
infra/      Go services (go.work): edge, ai-gateway, egress, agent, ctl
web/        Vue apps (pnpm workspace): portal, admin, panel, shared
clients/    dsh-plugin (npm: auriga-dsh, MIT)
contracts/  OpenAPI, protobuf, event and protocol schemas — the source of truth
deploy/     Windows, Linux, Compose, Helm
docs/       architecture, ADRs, plans
```

## Development

Prerequisites: JDK 25, Go 1.27, Node 22 with pnpm 11. No Docker is needed; tests use embedded PostgreSQL.
The first build downloads Maven, Go modules and npm packages.

```
./mvnw -B verify                       # Java: contracts codegen, Spotless, ArchUnit, tests (Windows: mvnw.cmd)
cd infra && go run ./ctl dev           # local PostgreSQL (127.0.0.1:15433) + NATS (127.0.0.1:14222)
pnpm install && pnpm -r test           # web packages and the DSH plugin
gitleaks dir .                         # secret scan (config: .gitleaks.toml)
```

Details: [Java](docs/dev/java.md) · [Go](docs/dev/go.md) · [Web](docs/dev/web.md) · [CI](docs/dev/ci.md).

## License

The platform is licensed under the [GNU AGPL-3.0](LICENSE); a commercial license is also available.
The DSH plugin in `clients/dsh-plugin` is MIT-licensed. See [LICENSING.md](LICENSING.md).
